"""Token splitter."""

import itertools
import logging
import re
from typing import Callable, Generic, List, Pattern, Tuple, TypeVar

from pydantic import BaseModel, Field, PrivateAttr

from docreader.splitter.header_hook import (
    HeaderTracker,
)
from docreader.utils.split import split_by_char, split_by_sep

DEFAULT_CHUNK_OVERLAP = 100
DEFAULT_CHUNK_SIZE = 512

T = TypeVar("T")

logger = logging.getLogger(__name__)


class TextSplitter(BaseModel, Generic[T]):
    chunk_size: int = Field(description="The token chunk size for each chunk.")
    chunk_overlap: int = Field(
        description="The token overlap of each chunk when splitting."
    )
    separators: List[str] = Field(
        description="Default separators for splitting into words"
    )

    # Try to keep the matched characters as a whole.
    # If it's too long, the content will be further segmented.
    protected_regex: List[str] = Field(
        description="Protected regex for splitting into words"
    )
    len_function: Callable[[str], int] = Field(description="The length function.")
    # Header tracking Hook related attributes
    header_hook: HeaderTracker = Field(default_factory=HeaderTracker, exclude=True)

    _protected_fns: List[Pattern] = PrivateAttr()
    _split_fns: List[Callable] = PrivateAttr()

    def __init__(
        self,
        chunk_size: int = DEFAULT_CHUNK_SIZE,
        chunk_overlap: int = DEFAULT_CHUNK_OVERLAP,
        separators: List[str] = ["\n", "。", " "],
        protected_regex: List[str] = [
            # math formula
            r"\$\$[\s\S]*?\$\$",
            # image
            r"!\[.*?\]\(.*?\)",
            # link
            r"\[.*?\]\(.*?\)",
            # table header
            r"(?:\|[^|\n]*)+\|[\r\n]+\s*(?:\|\s*:?-{3,}:?\s*)+\|[\r\n]+",
            # table body
            r"(?:\|[^|\n]*)+\|[\r\n]+",
            # code header
            r"```(?:\w+)[\r\n]+[^\r\n]*",
        ],
        length_function: Callable[[str], int] = lambda x: len(x),
    ):
        """Initialize with parameters."""
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
        self._protected_fns = [re.compile(reg) for reg in protected_regex]
        self._split_fns = [split_by_sep(sep) for sep in separators] + [split_by_char()]

    def split_text(self, text: str) -> List[Tuple[int, int, str]]:
        """Split text into chunks."""
        if text == "":
            return []

        splits = self._split(text)
        protect = self._split_protected(text)
        splits = self._join(splits, protect)

        assert "".join(splits) == text

        chunks = self._merge(splits)
        return chunks

    def _split(self, text: str) -> List[str]:
        """Break text into splits that are smaller than chunk size.

        NOTE: the splits contain the separators.
        """
        if self.len_function(text) <= self.chunk_size:
            return [text]

        splits = []
        for split_fn in self._split_fns:
            splits = split_fn(text)
            if len(splits) > 1:
                break

        new_splits = []
        for split in splits:
            split_len = self.len_function(split)
            if split_len <= self.chunk_size:
                new_splits.append(split)
            else:
                # recursively split
                new_splits.extend(self._split(split))
        return new_splits

    def _merge(self, splits: List[str]) -> List[Tuple[int, int, str]]:
        """Merge splits into chunks.

        The high-level idea is to keep adding splits to a chunk until we
        exceed the chunk size, then we start a new chunk with overlap.

        When we start a new chunk, we pop off the first element of the previous
        chunk until the total length is less than the chunk size.
        """
        chunks: List[Tuple[int, int, str]] = []

        cur_chunk: List[Tuple[int, int, str]] = []

        cur_headers, cur_len = "", 0
        cur_start, cur_end = 0, 0
        for split in splits:
            cur_end = cur_start + len(split)
            split_len = self.len_function(split)
            if split_len > self.chunk_size:
                logger.error(
                    f"Got a split of size {split_len}, ",
                    f"larger than chunk size {self.chunk_size}.",
                )

            self.header_hook.update(split)
            cur_headers = self.header_hook.get_headers()
            cur_headers_len = self.len_function(cur_headers)

            if cur_headers_len > self.chunk_size:
                logger.error(
                    f"Got headers of size {cur_headers_len}, ",
                    f"larger than chunk size {self.chunk_size}.",
                )
                cur_headers, cur_headers_len = "", 0

            # if we exceed the chunk size after adding the new split, then
            # we need to end the current chunk and start a new one
            if cur_len + split_len + cur_headers_len > self.chunk_size:
                # end the previous chunk
                if len(cur_chunk) > 0:
                    chunks.append(
                        (
                            cur_chunk[0][0],
                            cur_chunk[-1][1],
                            "".join([c[2] for c in cur_chunk]),
                        )
                    )

                # start a new chunk with overlap
                # keep popping off the first element of the previous chunk until:
                #   1. the current chunk length is less than chunk overlap
                #   2. the total length is less than chunk size
                while cur_chunk and (
                    cur_len > self.chunk_overlap
                    or cur_len + split_len + cur_headers_len > self.chunk_size
                ):
                    # pop off the first element
                    first_chunk = cur_chunk.pop(0)
                    cur_len -= self.len_function(first_chunk[2])

                if (
                    cur_headers
                    and split_len + cur_headers_len < self.chunk_size
                    and cur_headers not in split
                ):
                    cur_chunk.insert(
                        0,
                        (
                            cur_chunk[0][0] if cur_chunk else cur_start,
                            cur_chunk[0][1] if cur_chunk else cur_end,
                            cur_headers,
                        ),
                    )
                    cur_len += cur_headers_len

            cur_chunk.append((cur_start, cur_end, split))
            cur_len += split_len
            cur_start = cur_end

        # handle the last chunk
        assert cur_chunk
        if cur_headers and cur_len < self.chunk_size:
            cur_chunk.insert(0, (cur_chunk[0][0], cur_chunk[0][1], cur_headers))
        chunks.append(
            (
                cur_chunk[0][0],
                cur_chunk[-1][1],
                "".join([c[2] for c in cur_chunk]),
            )
        )

        return chunks

    def _split_protected(self, text: str) -> List[Tuple[int, str]]:
        matches = [
            (match.start(), match.end())
            for pattern in self._protected_fns
            for match in pattern.finditer(text)
        ]
        matches.sort(key=lambda x: (x[0], -x[1]))

        res = []

        def fold(initial: int, current: Tuple[int, int]) -> int:
            if current[0] >= initial:
                if current[1] - current[0] < self.chunk_size:
                    res.append((current[0], text[current[0] : current[1]]))
                else:
                    logger.warning(f"Protected text ignore: {current}")
            return max(initial, current[1])

        # filter overlapping matches
        list(itertools.accumulate(matches, fold, initial=-1))
        return res

    def _join(self, splits: List[str], protect: List[Tuple[int, str]]) -> List[str]:
        """
        Merges and splits elements in splits array based on protected substrings.

        The function processes the input splits to ensure all protected substrings
        remain as single items. If a protected substring is concatenated with preceding
        or following content in any split element, it will be separated from
        the adjacent content. The final result maintains the original order of content
        while enforcing the integrity of protected substrings.

        Key behaviors:
        1. Preserves the complete structure of each protected substring
        2. Separates protected substrings from any adjacent non-protected content
        3. Maintains the original sequence of all content except for necessary
        4. Handles cases where protected substrings are partially concatenated
        """
        j = 0
        point, start = 0, 0
        res = []

        for split in splits:
            end = start + len(split)

            cur = split[point - start :]
            while j < len(protect):
                p_start, p_content = protect[j]
                p_end = p_start + len(p_content)

                if end <= p_start:
                    break

                if point < p_start:
                    local_end = p_start - point
                    res.append(cur[:local_end])
                    cur = cur[local_end:]
                    point = p_start

                res.append(p_content)
                j += 1

                if point < p_end:
                    local_start = p_end - point
                    cur = cur[local_start:]
                    point = p_end

                if not cur:
                    break

            if cur:
                res.append(cur)
                point = end

            start = end
        return res


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

    sp = TextSplitter(chunk_size=200, chunk_overlap=2)
    ck = sp.split_text(s)
    for c in ck:
        print("------", len(c))
        print(c)
    pass
