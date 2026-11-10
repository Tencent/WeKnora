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

_TABLE_ROW_PATTERN = re.compile(r"^\s*(?:\|[^|\n]*)+\|\s*$", re.MULTILINE)
_MARKDOWN_TABLE_PRIORITY = 15


def _is_empty_table_header_row(header: str) -> bool:
    """True when the column-name line is only pipes/whitespace (MarkItDown quirk)."""
    newline = header.find("\n")
    if newline < 0:
        return False
    row = header[:newline].strip()
    return bool(row) and all(ch in "| \t" for ch in row)


def _extract_separator_line(header: str) -> str:
    for line in header.split("\n"):
        if "---" in line:
            return line + "\n"
    return ""


def _table_row_column_count(line: str) -> int:
    line = line.strip()
    if not line.startswith("|"):
        return 0
    parts = line.split("|")
    if parts and parts[0].strip() == "":
        parts = parts[1:]
    if parts and parts[-1].strip() == "":
        parts = parts[:-1]
    return len(parts)


def _first_table_row_column_count(text: str) -> int:
    for line in text.split("\n"):
        line = line.strip()
        if line and _TABLE_ROW_PATTERN.match(line):
            return _table_row_column_count(line)
    return 0


def _header_table_column_count(header: str) -> int:
    for line in header.split("\n"):
        line = line.strip()
        if not line or "---" in line:
            continue
        count = _table_row_column_count(line)
        if count > 0:
            return count
    return 0


def _split_ends_with_paragraph_break(split: str) -> bool:
    trimmed = split.rstrip(" \t\r")
    return trimmed.endswith("\n\n") or trimmed.endswith("\r\n\r\n")


def header_column_mismatch(headers: str, next_unit: str) -> bool:
    header_cols = _header_table_column_count(headers)
    row_cols = _first_table_row_column_count(next_unit)
    return header_cols > 0 and row_cols > 0 and header_cols != row_cols


# 定义Hook状态数据结构
class HeaderTracker(BaseModel):
    """表头追踪 Hook 的状态类"""

    header_hook_configs: List[HeaderTrackerHook] = Field(default=DEFAULT_CONFIGS)
    active_headers: Dict[int, str] = Field(default_factory=dict)
    ended_headers: set[int] = Field(default_factory=set)
    pending_extend: Dict[int, bool] = Field(default_factory=dict)
    pending_table_break: bool = Field(default=False)
    header_ended_this_unit: bool = Field(default=False)

    def _clear_table_header(self) -> None:
        self.ended_headers.add(_MARKDOWN_TABLE_PRIORITY)
        self.active_headers.pop(_MARKDOWN_TABLE_PRIORITY, None)
        self.pending_extend.pop(_MARKDOWN_TABLE_PRIORITY, None)

    def update(self, split: str) -> Dict[int, str]:
        """检测当前split中的表头开始/结束，更新Hook状态"""
        new_headers: Dict[int, str] = {}
        self.header_ended_this_unit = False

        if self.pending_table_break:
            self.pending_table_break = False
            if _MARKDOWN_TABLE_PRIORITY in self.active_headers:
                if _first_table_row_column_count(split) > 0:
                    self._clear_table_header()
                    self.header_ended_this_unit = True
                else:
                    self._clear_table_header()

        # 1. 检查是否有表头结束标记
        for config in self.header_hook_configs:
            if config.priority in self.active_headers and config.end_pattern.search(
                split
            ):
                self.ended_headers.add(config.priority)
                del self.active_headers[config.priority]
                self.pending_extend.pop(config.priority, None)

        # 1b. \n\n 分块会吞掉表间空行：段尾 \n\n 或列数变化时结束表头追踪
        if (
            _MARKDOWN_TABLE_PRIORITY in self.active_headers
            and not self.pending_extend.get(_MARKDOWN_TABLE_PRIORITY)
        ):
            if _split_ends_with_paragraph_break(split):
                self.pending_table_break = True
            else:
                header = self.active_headers[_MARKDOWN_TABLE_PRIORITY]
                row_cols = _first_table_row_column_count(split)
                header_cols = _header_table_column_count(header)
                if row_cols > 0 and header_cols > 0 and row_cols != header_cols:
                    self._clear_table_header()
                    self.header_ended_this_unit = True

        # 2. 空表头行：用首个数据行补全列名（与 Go header_tracker 一致）
        for priority in list(self.pending_extend.keys()):
            if priority in self.active_headers and _TABLE_ROW_PATTERN.search(split):
                sep = _extract_separator_line(self.active_headers[priority])
                self.active_headers[priority] = split + sep
            self.pending_extend.pop(priority, None)

        # 3. 检查是否有新的表头开始标记（只处理未活跃且未结束的）
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
                    if _is_empty_table_header_row(header):
                        self.pending_extend[config.priority] = True

        # 4. 检查是否所有活跃表头都已结束（清空结束标记）
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
