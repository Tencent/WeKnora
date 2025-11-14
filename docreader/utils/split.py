import re
from typing import Callable, List


def split_text_keep_separator(text: str, separator: str) -> List[str]:
    """Split text with separator and keep the separator at the end of each split."""
    parts = text.split(separator)
    result = [separator + s if i > 0 else s for i, s in enumerate(parts)]
    return [s for s in result if s]


def split_by_sep(sep: str, keep_sep: bool = True) -> Callable[[str], List[str]]:
    """Split text by separator."""
    if keep_sep:
        return lambda text: split_text_keep_separator(text, sep)
    else:
        return lambda text: text.split(sep)


def split_by_char() -> Callable[[str], List[str]]:
    """Split text by character."""
    return lambda text: list(text)


def split_by_regex(regex: str) -> Callable[[str], List[str]]:
    """Split text by regex."""
    pattern = re.compile(f"({regex})")
    return lambda text: list(filter(None, pattern.split(text)))


def match_by_regex(regex: str) -> Callable[[str], bool]:
    """Split text by regex."""
    pattern = re.compile(regex)
    return lambda text: bool(pattern.match(text))
