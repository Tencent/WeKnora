import re
from typing import Callable, List


def split_text_keep_separator(text: str, separator: str) -> List[str]:
    """Split text with separator and keep the separator at the end of each split.
    
    Args:
        text: The input text to split
        separator: The separator string to split by
        
    Returns:
        List of text chunks with separator preserved at the start of each chunk (except first)
        
    Example:
        >>> split_text_keep_separator("Hello\nWorld\nTest", "\n")
        ["Hello", "\nWorld", "\nTest"]
    """
    # Split text by separator
    parts = text.split(separator)
    # Add separator back to the beginning of each part (except the first one)
    result = [separator + s if i > 0 else s for i, s in enumerate(parts)]
    # Filter out empty strings
    return [s for s in result if s]


def split_by_sep(sep: str, keep_sep: bool = True) -> Callable[[str], List[str]]:
    """Create a function that splits text by a given separator.
    
    Args:
        sep: The separator string to split by
        keep_sep: If True, keep the separator in the result; if False, discard it
        
    Returns:
        A callable function that takes text and returns a list of split strings
    """
    if keep_sep:
        return lambda text: split_text_keep_separator(text, sep)
    else:
        return lambda text: text.split(sep)


def split_by_char() -> Callable[[str], List[str]]:
    """Create a function that splits text into individual characters.
    
    Returns:
        A callable function that takes text and returns a list of characters
    """
    return lambda text: list(text)


def split_by_regex(regex: str) -> Callable[[str], List[str]]:
    """Create a function that splits text by a regex pattern.
    
    Args:
        regex: The regular expression pattern to split by
        
    Returns:
        A callable function that takes text and returns a list of split strings
        The full separator matches are included in the result.
    """
    # Preserve capture numbering and inline flags in the supplied pattern.
    pattern = re.compile(regex)

    def split(text: str) -> List[str]:
        parts = []
        start = 0
        for match in pattern.finditer(text):
            parts.extend((text[start:match.start()], match.group()))
            start = match.end()
        parts.append(text[start:])
        return list(filter(None, parts))

    return split


def match_by_regex(regex: str) -> Callable[[str], bool]:
    """Create a function that checks if text matches a regex pattern.
    
    Args:
        regex: The regular expression pattern to match against
        
    Returns:
        A callable function that takes text and returns True if it matches the pattern
    """
    # Compile the regex pattern for efficient reuse
    pattern = re.compile(regex)
    # Return a function that checks if text matches the pattern from the start
    return lambda text: bool(pattern.match(text))
