import re
from typing import Callable, Dict, List, Match, Pattern, Union

from pydantic import BaseModel, Field


class HeaderTrackerHook(BaseModel):
    """表头追踪Hook的配置类，支持多种场景的表头识别"""

    start_pattern: Pattern[str] = Field(
        description="表头开始匹配（正则表达式或字符串）"
    )
    end_pattern: Pattern[str] = Field(description="表头结束匹配（正则表达式或字符串）")
    extract_header_fn: Callable[[Match[str]], str] = Field(
        default=lambda m: m.group(0),
        description="从开始匹配结果中提取表头内容的函数（默认取匹配到的整个内容）",
    )
    priority: int = Field(default=0, description="优先级（多个配置时，高优先级先匹配）")
    case_sensitive: bool = Field(
        default=True, description="是否大小写敏感（仅当传入字符串pattern时生效）"
    )

    def __init__(
        self,
        start_pattern: Union[str, Pattern[str]],
        end_pattern: Union[str, Pattern[str]],
        **kwargs,
    ):
        flags = 0 if kwargs.get("case_sensitive", True) else re.IGNORECASE
        if isinstance(start_pattern, str):
            start_pattern = re.compile(start_pattern, flags | re.DOTALL)
        if isinstance(end_pattern, str):
            end_pattern = re.compile(end_pattern, flags | re.DOTALL)
        super().__init__(
            start_pattern=start_pattern,
            end_pattern=end_pattern,
            **kwargs,
        )


# 初始化表头Hook配置（提供默认配置：支持Markdown表格、代码块）
DEFAULT_CONFIGS = [
    # 代码块配置（```开头，```结尾）
    # HeaderTrackerHook(
    #     # 代码块开始（支持语言指定）
    #     start_pattern=r"^\s*```(\w+).*(?!```$)",
    #     # 代码块结束
    #     end_pattern=r"^\s*```.*$",
    #     extract_header_fn=lambda m: f"```{m.group(1)}" if m.group(1) else "```",
    #     priority=20,  # 代码块优先级高于表格
    #     case_sensitive=True,
    # ),
    # Markdown表格配置（表头带下划线）
    HeaderTrackerHook(
        # 表头行 + 分隔行
        start_pattern=r"^\s*(?:\|[^|\n]*)+[\r\n]+\s*(?:\|\s*:?-{3,}:?\s*)+\|?[\r\n]+$",
        # 空行或非表格内容
        end_pattern=r"^\s*$|^\s*[^|\s].*$",
        priority=15,
        case_sensitive=False,
    ),
]
DEFAULT_CONFIGS.sort(key=lambda x: -x.priority)


# 定义Hook状态数据结构
class HeaderTracker(BaseModel):
    """表头追踪 Hook 的状态类"""

    header_hook_configs: List[HeaderTrackerHook] = Field(default=DEFAULT_CONFIGS)
    active_headers: Dict[int, str] = Field(default_factory=dict)
    ended_headers: set[int] = Field(default_factory=set)

    def update(self, split: str) -> Dict[int, str]:
        """检测当前split中的表头开始/结束，更新Hook状态"""
        new_headers: Dict[int, str] = {}

        # 1. 检查是否有表头结束标记
        for config in self.header_hook_configs:
            if config.priority in self.active_headers and config.end_pattern.search(
                split
            ):
                self.ended_headers.add(config.priority)
                del self.active_headers[config.priority]

        # 2. 检查是否有新的表头开始标记（只处理未活跃且未结束的）
        for config in self.header_hook_configs:
            if (
                config.priority not in self.active_headers
                and config.priority not in self.ended_headers
            ):
                match = config.start_pattern.search(split)
                if match:
                    header = config.extract_header_fn(match)
                    self.active_headers[config.priority] = header
                    new_headers[config.priority] = header

        # 3. 检查是否所有活跃表头都已结束（清空结束标记）
        if not self.active_headers:
            self.ended_headers.clear()

        return new_headers

    def get_headers(self) -> str:
        """获取当前所有活跃表头的拼接文本（按优先级排序）"""
        # 按优先级降序排列表头
        sorted_headers = sorted(self.active_headers.items(), key=lambda x: -x[0])
        return (
            "\n".join([header for _, header in sorted_headers])
            if sorted_headers
            else ""
        )
