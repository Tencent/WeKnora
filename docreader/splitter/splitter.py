"""Token splitter.

This module provides text splitting functionality with support for:
- Configurable chunk size and overlap
- Protected regex patterns (e.g., math formulas, images, links, tables)
- Header tracking for context preservation
- Smart merging with overlap handling
"""

import itertools
import logging
import re
from typing import Callable, Generic, List, Pattern, Tuple, TypeVar

from pydantic import BaseModel, Field, PrivateAttr

from docreader.splitter.header_hook import (
    HeaderTracker,
)
from docreader.utils.split import split_by_char, split_by_sep

# Default configuration for text chunking
DEFAULT_CHUNK_OVERLAP = 100  # Number of tokens to overlap between chunks
DEFAULT_CHUNK_SIZE = 512  # Maximum size of each chunk in tokens

T = TypeVar("T")

logger = logging.getLogger(__name__)


class TextSplitter(BaseModel, Generic[T]):
    """Text splitter with support for protected patterns and header tracking.

    This class splits text into chunks while:
    - Respecting chunk size and overlap constraints
    - Preserving protected patterns (formulas, tables, code blocks)
    - Tracking headers for context preservation
    - Maintaining text integrity with smart merging
    """

    chunk_size: int = Field(description="The token chunk size for each chunk.")
    chunk_overlap: int = Field(
        description="The token overlap of each chunk when splitting."
    )
    separators: List[str] = Field(
        description="Default separators for splitting into words"
    )

    # Try to keep the matched characters as a whole.
    # If it's too long, the content will be further segmented.
    # 尝试将匹配的字符作为整体保留，如果太长则进一步分段
    protected_regex: List[str] = Field(
        description="Protected regex for splitting into words"
    )
    len_function: Callable[[str], int] = Field(description="The length function.")
    # Header tracking Hook related attributes
    # 标题跟踪钩子相关属性
    header_hook: HeaderTracker = Field(default_factory=HeaderTracker, exclude=True)

    # Compiled regex patterns for protected content
    _protected_fns: List[Pattern] = PrivateAttr()
    # Split functions for different separators
    _split_fns: List[Callable] = PrivateAttr()

    def __init__(
        self,
        chunk_size: int = DEFAULT_CHUNK_SIZE,
        chunk_overlap: int = DEFAULT_CHUNK_OVERLAP,
        separators: List[str] = ["\n", "。", " "],
        protected_regex: List[str] = [
            # math formula - LaTeX style formulas enclosed in $$
            r"\$\$[\s\S]*?\$\$",
            # image - Markdown image syntax ![alt](url)
            r"!\[.*?\]\(.*?\)",
            # link - Markdown link syntax [text](url)
            r"\[.*?\]\(.*?\)",
            # table header - Markdown table header with separator line
            r"[ ]*(?:\|[^|\n]*)+\|[\r\n]+\s*(?:\|\s*:?-{3,}:?\s*)+\|[\r\n]+",
            # table body - Markdown table rows
            r"[ ]*(?:\|[^|\n]*)+\|[\r\n]+",
            # code header - Code block start with language identifier
            r"```(?:\w+)[\r\n]+[^\r\n]*",
        ],
        length_function: Callable[[str], int] = lambda x: len(x),
    ):
        """Initialize with parameters.

        Args:
            chunk_size: Maximum size of each chunk
            chunk_overlap: Number of tokens to overlap between chunks
            separators: List of separators to use for splitting (in priority order)
            protected_regex: Regex patterns for content that should be kept intact
            length_function: Function to calculate text length (default: character count)

        Raises:
            ValueError: If chunk_overlap is larger than chunk_size
        """
        if chunk_overlap > chunk_size:
            raise ValueError(
                f"Got a larger chunk overlap ({chunk_overlap}) than chunk size "
                f"({chunk_size}), should be smaller."
            )

        super().__init__(
            chunk_size=chunk_size,
            chunk_overlap=chunk_overlap,
            separators=separators,
            protected_regex=protected_regex,
            len_function=length_function,
        )
        # Compile all protected regex patterns for efficient matching
        self._protected_fns = [re.compile(reg) for reg in protected_regex]
        # Create split functions: one for each separator, plus character-level splitting as fallback
        self._split_fns = [split_by_sep(sep) for sep in separators] + [split_by_char()]

    def split_text(self, text: str) -> List[Tuple[int, int, str]]:
        """Split text into chunks with overlap and protected pattern handling.

        Args:
            text: The input text to split

        Returns:
            List of tuples (start_pos, end_pos, chunk_text) representing each chunk
        """
        if text == "":
            return []

        # Step 1: Split text by separators recursively
        splits = self._split(text)
        # Step 2: Extract protected content positions
        protect = self._split_protected(text)
        # Step 3: Merge splits with protected content to ensure integrity
        splits = self._join(splits, protect)

        # Verify that joining all splits reconstructs the original text
        assert "".join(splits) == text

        # Step 4: Merge splits into final chunks with overlap
        chunks = self._merge(splits)

        # Step 5: Validate chunks and test restoration
        # self._validate_chunks(chunks, text)

        return chunks

    def _split(self, text: str) -> List[str]:
        """Break text into splits that are smaller than chunk size.

        This method recursively splits text using separators in priority order.
        It tries each separator until it finds one that can split the text,
        then recursively processes any splits that are still too large.

        NOTE: the splits contain the separators.

        Args:
            text: The text to split

        Returns:
            List of text splits, each smaller than chunk_size
        """
        # If text is already small enough, return as-is
        if self.len_function(text) <= self.chunk_size:
            return [text]

        # Try each split function in order until one successfully splits the text
        splits = []
        for split_fn in self._split_fns:
            splits = split_fn(text)
            if len(splits) > 1:
                break

        # Process each split: keep if small enough, otherwise recursively split further
        new_splits = []
        for split in splits:
            split_len = self.len_function(split)
            if split_len <= self.chunk_size:
                new_splits.append(split)
            else:
                # Recursively split oversized chunks
                new_splits.extend(self._split(split))
        return new_splits

    def _merge(self, splits: List[str]) -> List[Tuple[int, int, str]]:
        """Merge splits into chunks with overlap and header tracking.

        The high-level idea is to keep adding splits to a chunk until we
        exceed the chunk size, then we start a new chunk with overlap.

        When we start a new chunk, we pop off the first element of the previous
        chunk until the total length is less than the chunk size.

        Headers are tracked and prepended to chunks for context preservation.

        Args:
            splits: List of text splits to merge

        Returns:
            List of tuples (start_pos, end_pos, chunk_text) representing merged chunks
        """
        # Final list of chunks with their positions
        chunks: List[Tuple[int, int, str]] = []

        # Current chunk being built: list of (start, end, text) tuples
        cur_chunk: List[Tuple[int, int, str]] = []

        # Track current headers and chunk length
        cur_headers, cur_len = "", 0
        # Track position in original text
        cur_start, cur_end = 0, 0

        for split in splits:
            # Calculate position of current split in original text
            cur_end = cur_start + len(split)
            split_len = self.len_function(split)

            # Warn if a single split exceeds chunk size (shouldn't happen after _split)
            if split_len > self.chunk_size:
                logger.error(
                    f"Got a split of size {split_len}, ",
                    f"larger than chunk size {self.chunk_size}.",
                )

            # Update header tracking with current split
            self.header_hook.update(split)
            cur_headers = self.header_hook.get_headers()
            cur_headers_len = self.len_function(cur_headers)

            # If headers are too large, skip them to avoid oversized chunks
            if cur_headers_len > self.chunk_size:
                logger.error(
                    f"Got headers of size {cur_headers_len}, ",
                    f"larger than chunk size {self.chunk_size}.",
                )
                cur_headers, cur_headers_len = "", 0

            # Check if adding this split would exceed chunk size
            # If so, finalize current chunk and start a new one with overlap
            if cur_len + split_len + cur_headers_len > self.chunk_size:
                # Finalize the previous chunk if it has content
                if len(cur_chunk) > 0:
                    chunks.append(
                        (
                            cur_chunk[0][0],  # Start position of first element
                            cur_chunk[-1][1],  # End position of last element
                            "".join([c[2] for c in cur_chunk]),  # Concatenated text
                        )
                    )

                # Start a new chunk with overlap from previous chunk
                # Keep popping off the first element of the previous chunk until:
                #   1. the current chunk length is less than chunk overlap
                #   2. the total length is less than chunk size
                while cur_chunk and (
                    cur_len > self.chunk_overlap
                    or cur_len + split_len + cur_headers_len > self.chunk_size
                ):
                    # Remove the first element to reduce overlap.
                    # If the first element is a prepended header (start==end), also remove it.
                    first_chunk = cur_chunk.pop(0)
                    cur_len -= self.len_function(first_chunk[2])

                    # If we just popped a real content piece, there may be a header right after it
                    # (depending on previous iterations). Pop it only if it is actually a header.
                    if cur_chunk and first_chunk[0] == first_chunk[1]:
                        first_chunk = cur_chunk.pop(0)
                        cur_len -= self.len_function(first_chunk[2])

                # Prepend headers to new chunk if:
                # 1. Headers exist
                # 2. Headers + split fit in chunk size
                # 3. Headers are not already in the split
                if (
                    cur_headers
                    and split_len + cur_headers_len < self.chunk_size
                    and cur_headers not in split
                ):
                    next_start = cur_chunk[0][0] if cur_chunk else cur_start

                    cur_chunk.insert(0, (next_start, next_start, cur_headers))
                    cur_len += cur_headers_len

            # Add current split to the chunk
            cur_chunk.append((cur_start, cur_end, split))
            cur_len += split_len
            cur_start = cur_end

        # Handle the last chunk (there should always be at least one)
        assert cur_chunk
        chunks.append(
            (
                cur_chunk[0][0],
                cur_chunk[-1][1],
                "".join([c[2] for c in cur_chunk]),
            )
        )

        return chunks

    def _split_protected(self, text: str) -> List[Tuple[int, str]]:
        """Extract protected content from text based on regex patterns.

        Args:
            text: The input text to scan for protected patterns

        Returns:
            List of tuples (start_position, protected_text) for each protected match
        """
        # Find all matches for all protected patterns
        matches = [
            (match.start(), match.end())
            for pattern in self._protected_fns
            for match in pattern.finditer(text)
        ]
        # Sort by start position (ascending), then by length (descending) to handle overlaps
        matches.sort(key=lambda x: (x[0], -x[1]))

        res = []

        def fold(initial: int, current: Tuple[int, int]) -> int:
            """Accumulator function to filter overlapping matches."""
            # Only process if match starts after previous match ended
            if current[0] >= initial:
                # Only keep protected content if it fits within chunk size
                if current[1] - current[0] < self.chunk_size:
                    res.append((current[0], text[current[0] : current[1]]))
                else:
                    logger.warning(f"Protected text ignore: {current}")
            # Return the end position of the furthest match so far
            return max(initial, current[1])

        # Filter overlapping matches using accumulate
        list(itertools.accumulate(matches, fold, initial=-1))
        return res

    def _join(self, splits: List[str], protect: List[Tuple[int, str]]) -> List[str]:
        """Merge splits with protected content to ensure protected patterns remain intact.

        Merges and splits elements in splits array based on protected substrings.

        The function processes the input splits to ensure all protected substrings
        remain as single items. If a protected substring is concatenated with preceding
        or following content in any split element, it will be separated from
        the adjacent content. The final result maintains the original order of content
        while enforcing the integrity of protected substrings.

        Key behaviors:
        1. Preserves the complete structure of each protected substring
        2. Separates protected substrings from any adjacent non-protected content
        3. Maintains the original sequence of all content
        4. Handles cases where protected substrings are partially concatenated

        Args:
            splits: List of text splits from _split()
            protect: List of (position, text) tuples for protected content

        Returns:
            List of text splits with protected content properly isolated
        """
        j = 0  # Index for protected content list
        point, start = 0, 0  # Track current position in original text
        res = []  # Result list of merged splits

        for split in splits:
            # Calculate end position of current split
            end = start + len(split)

            # Get the portion of split starting from current point
            cur = split[point - start :]

            # Process all protected content that overlaps with current split
            while j < len(protect):
                p_start, p_content = protect[j]
                p_end = p_start + len(p_content)

                # If protected content is beyond current split, move to next split
                if end <= p_start:
                    break

                # Add content before protected section
                if point < p_start:
                    local_end = p_start - point
                    res.append(cur[:local_end])
                    cur = cur[local_end:]
                    point = p_start

                # Add the protected content as a single unit
                res.append(p_content)
                j += 1

                # Skip content that's part of the protected section
                if point < p_end:
                    local_start = p_end - point
                    cur = cur[local_start:]
                    point = p_end

                # If no more content in current split, break
                if not cur:
                    break

            # Add any remaining content from current split
            if cur:
                res.append(cur)
                point = end

            # Move to next split
            start = end
        return res

    def _validate_chunks(
        self, chunks: List[Tuple[int, int, str]], original_text: str
    ) -> None:
        """Validate chunks order and test text restoration.

        This method performs two validations:
        1. Checks if chunk start positions are in ascending order
        2. Tests if the original text can be restored from chunks

        If validation fails, saves debug information to /tmp/chunk_error_<timestamp>.md

        Args:
            chunks: List of tuples (start_pos, end_pos, chunk_text) to validate
            original_text: The original text that was split
        """
        import datetime

        errors = []

        # Validation 1: Check if start positions are in ascending order
        for i in range(1, len(chunks)):
            prev_start = chunks[i - 1][0]
            curr_start = chunks[i][0]
            if curr_start < prev_start:
                error_msg = (
                    f"Chunk order error: chunk[{i}] start position ({curr_start}) "
                    f"is less than chunk[{i - 1}] start position ({prev_start})"
                )
                errors.append(error_msg)
                logger.error(error_msg)

        # Validation 2: Test text restoration
        try:
            restored_text = self.restore_text(chunks)
            if restored_text != original_text:
                error_msg = (
                    f"Restoration failed: restored text differs from original. "
                    f"Original length: {len(original_text)}, "
                    f"Restored length: {len(restored_text)}"
                )
                errors.append(error_msg)
                logger.error(error_msg)

                # Find first difference position
                min_len = min(len(original_text), len(restored_text))
                diff_pos = -1
                for i in range(min_len):
                    if original_text[i] != restored_text[i]:
                        diff_pos = i
                        break

                if diff_pos >= 0:
                    context_start = max(0, diff_pos - 50)
                    context_end = min(len(original_text), diff_pos + 50)
                    errors.append(
                        f"First difference at position {diff_pos}:\n"
                        f"Original: {repr(original_text[context_start:context_end])}\n"
                        f"Restored: {repr(restored_text[context_start:context_end])}"
                    )
                elif len(original_text) != len(restored_text):
                    errors.append(
                        f"Texts match up to position {min_len}, but lengths differ"
                    )
        except Exception as e:
            error_msg = f"Restoration exception: {str(e)}"
            errors.append(error_msg)
            logger.error(error_msg)

        # If there are errors, save debug information to file
        if errors:
            timestamp = datetime.datetime.now().strftime("%Y%m%d_%H%M%S")
            error_file = f"/tmp/chunk_error_{timestamp}.md"

            with open(error_file, "w", encoding="utf-8") as f:
                f.write("# Chunk Validation Error Report\n\n")
                f.write(f"Timestamp: {timestamp}\n\n")

                f.write("## Errors\n\n")
                for error in errors:
                    f.write(f"- {error}\n\n")

                f.write("\n## Original Text\n\n")
                f.write(f"Length: {len(original_text)}\n\n")
                f.write("```\n")
                f.write(original_text)
                f.write("\n```\n\n")

                f.write("\n## Chunks Information\n\n")
                f.write(f"Total chunks: {len(chunks)}\n\n")
                for i, (start, end, chunk_text) in enumerate(chunks):
                    f.write(f"### Chunk {i}\n\n")
                    f.write(f"- Position: [{start}:{end}]\n")
                    f.write(f"- Length: {len(chunk_text)}\n")
                    f.write(f"- Content:\n\n```\n{chunk_text}\n```\n\n")

                try:
                    restored_text = self.restore_text(chunks)
                    f.write("\n## Restored Text\n\n")
                    f.write(f"Length: {len(restored_text)}\n\n")
                    f.write("```\n")
                    f.write(restored_text)
                    f.write("\n```\n")
                except Exception as e:
                    f.write("\n## Restoration Failed\n\n")
                    f.write(f"Error: {str(e)}\n")

            logger.error(f"Validation errors saved to: {error_file}")

    def restore_text(self, chunks: List[Tuple[int, int, str]]) -> str:
        """Restore original text from chunks with overlap handling.

        This method reconstructs the original text from chunks that may contain:
        - Overlapping content between consecutive chunks
        - Prepended headers that were added during merging (headers have start==end position)

        The algorithm:
        1. Sort chunks by their start position (and end position as tiebreaker)
        2. Track the maximum end position seen so far
        3. For each chunk, extract only the new content (after max_end_pos)
        4. Concatenate all new content pieces

        Args:
            chunks: List of tuples (start_pos, end_pos, chunk_text) from split_text()

        Returns:
            The restored original text

        Example:
            >>> splitter = TextSplitter(chunk_size=10, chunk_overlap=3)
            >>> chunks = splitter.split_text("Hello World!")
            >>> restored = splitter.restore_text(chunks)
            >>> assert restored == "Hello World!"
        """
        if not chunks:
            return ""

        # Sort chunks by start position, then by end position
        sorted_chunks = sorted(chunks, key=lambda x: (x[1], x[0]))

        result_parts = []
        last_end = 0

        for start_pos, end_pos, chunk_text in sorted_chunks:
            result_parts.append(chunk_text[last_end - end_pos :])
            last_end = end_pos

        return "".join(result_parts)


if __name__ == "__main__":
    s = """
    这是一些普通文本。

    | 姓名 | 年龄 | 城市 |
    |------|------|------|
    | 张三 | 25   | 北京 |
    | 李四 | 30   | 上海 |
    | 王五 | 28   | 广州 |
    | 张三 | 25   | 北京 |
    | 李四 | 30   | 上海 |
    | 王五 | 28   | 广州 |

    这是文本结束。

"""

    sp = TextSplitter(
        chunk_size=200,
        chunk_overlap=10,
        separators=["\n\n", "\n", "。", "？", "！", "，", "；", "："],
    )
    ck = sp.split_text(s)
    for c in ck:
        print("------", len(c))
        print(c)
    pass
