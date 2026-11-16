"""Common result types for incremental document parsing."""

from dataclasses import dataclass, field
from typing import Callable, Iterator

from docreader.models.document import Document


@dataclass(frozen=True)
class StreamedImage:
    """One image payload emitted by a parser without retaining the full set."""

    ref_path: str
    image_data: bytes


@dataclass
class ParsedDocumentStream:
    """A document header plus images that may be produced incrementally.

    ``document.images`` contains any small images already required to build the
    document. ``images`` yields the remaining payloads lazily. ``image_count``
    is the total count across both sources and is suitable for the gRPC header.
    Call ``close`` when the consumer stops before exhausting ``images`` so
    parser resources and temporary files are released.
    """

    document: Document
    image_count: int
    images: Iterator[StreamedImage] = field(default_factory=lambda: iter(()))
    _cleanup: Callable[[], None] | None = field(default=None, repr=False)
    _closed: bool = field(default=False, init=False, repr=False)

    def close(self) -> None:
        """Close the lazy image iterator and release parser-owned resources."""
        if self._closed:
            return
        self._closed = True
        try:
            close_iterator = getattr(self.images, "close", None)
            if callable(close_iterator):
                close_iterator()
        finally:
            if self._cleanup is not None:
                self._cleanup()

    def __enter__(self) -> "ParsedDocumentStream":
        return self

    def __exit__(self, exc_type, exc, traceback) -> None:
        self.close()
