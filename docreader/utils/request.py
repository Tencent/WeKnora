import contextlib
import logging
import time
import uuid
from contextvars import ContextVar
from logging import LogRecord
from typing import Optional

# 配置日志
logger = logging.getLogger(__name__)

# 定义上下文变量
request_id_var = ContextVar("request_id", default=None)
_request_start_time_ctx = ContextVar("request_start_time", default=None)


def set_request_id(request_id: str) -> None:
    """设置当前上下文的请求ID"""
    request_id_var.set(request_id)


def get_request_id() -> Optional[str]:
    """获取当前上下文的请求ID"""
    return request_id_var.get()


class MillisecondFormatter(logging.Formatter):
    """自定义日志格式化器，只显示毫秒级时间戳(3位数字)而不是微秒(6位)"""

    def formatTime(self, record, datefmt=None):
        """重写formatTime方法，将微秒格式化为毫秒"""
        # 先获取标准的格式化时间
        result = super().formatTime(record, datefmt)

        # 如果使用了包含.%f的格式，则将微秒(6位)截断为毫秒(3位)
        if datefmt and ".%f" in datefmt:
            # 格式化的时间字符串应该在最后有6位微秒数
            parts = result.split(".")
            if len(parts) > 1 and len(parts[1]) >= 6:
                # 只保留前3位作为毫秒
                millis = parts[1][:3]
                result = f"{parts[0]}.{millis}"

        return result


def init_logging_request_id():
    """
    Initialize logging to include request ID in log messages.
    Add the custom filter to all existing handlers
    """
    logger.info("Initializing request ID logging")
    root_logger = logging.getLogger()

    # 添加自定义过滤器到所有处理器
    for handler in root_logger.handlers:
        # 添加请求ID过滤器
        handler.addFilter(RequestIdFilter())

        # 更新格式化器以包含请求ID，调整格式使其更紧凑整齐
        formatter = logging.Formatter(
            fmt="%(asctime)s.%(msecs)03d [%(request_id)s] %(levelname)-5s %(name)-20s | %(message)s",
            datefmt="%Y-%m-%d %H:%M:%S",
        )
        handler.setFormatter(formatter)

    logger.info(
        f"Updated {len(root_logger.handlers)} handlers with request ID formatting"
    )

    # 如果没有处理器，添加一个标准输出处理器
    if not root_logger.handlers:
        handler = logging.StreamHandler()
        formatter = logging.Formatter(
            fmt="%(asctime)s.%(msecs)03d [%(request_id)s] %(levelname)-5s %(name)-20s | %(message)s",
            datefmt="%Y-%m-%d %H:%M:%S",
        )
        handler.setFormatter(formatter)
        handler.addFilter(RequestIdFilter())
        root_logger.addHandler(handler)
        logger.info("Added new StreamHandler with request ID formatting")


class RequestIdFilter(logging.Filter):
    """Filter that adds request ID to log messages"""

    def filter(self, record: LogRecord) -> bool:
        request_id = request_id_var.get()
        if request_id is not None:
            # 为日志记录添加请求ID属性，使用短格式
            if len(request_id) > 8:
                # 截取ID的前8个字符，确保显示整齐
                short_id = request_id[:8]
                if "-" in request_id:
                    # 尝试保留格式，例如 test-req-1-XXX
                    parts = request_id.split("-")
                    if len(parts) >= 3:
                        # 如果格式是 xxx-xxx-n-randompart
                        short_id = f"{parts[0]}-{parts[1]}-{parts[2]}"
                record.request_id = short_id
            else:
                record.request_id = request_id

            # 添加执行时间属性
            start_time = _request_start_time_ctx.get()
            if start_time is not None:
                elapsed_ms = int((time.time() - start_time) * 1000)
                record.elapsed_ms = elapsed_ms
                # 添加执行时间到消息中
                if not hasattr(record, "message_with_elapsed"):
                    record.message_with_elapsed = True
                    record.msg = f"{record.msg} (elapsed: {elapsed_ms}ms)"
        else:
            # 如果没有请求ID，使用占位符
            record.request_id = "no-req-id"

        return True


@contextlib.contextmanager
def request_id_context(request_id: str = None):
    """Context manager that sets a request ID for the current context

    Args:
        request_id: 要使用的请求ID，如果为None则自动生成

    Example:
        with request_id_context("req-123"):
            # 在这个代码块中的所有日志都会包含请求ID req-123
            logging.info("Processing request")
    """
    # Generate or use provided request ID
    req_id = request_id or str(uuid.uuid4())

    # Set start time and request ID
    start_time = time.time()
    req_token = request_id_var.set(req_id)
    time_token = _request_start_time_ctx.set(start_time)

    logger.info(f"Starting new request with ID: {req_id}")

    try:
        yield request_id_var.get()
    finally:
        # Log completion and reset context vars
        elapsed_ms = int((time.time() - start_time) * 1000)
        logger.info(f"Request {req_id} completed in {elapsed_ms}ms")
        request_id_var.reset(req_token)
        _request_start_time_ctx.reset(time_token)
