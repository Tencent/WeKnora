"""Chunk document schema."""

import json
from typing import Any, Dict, List

from pydantic import BaseModel, Field


class Chunk(BaseModel):
    """Document Chunk including chunk content, chunk metadata."""

    content: str = Field(default="", description="chunk text content")
    seq: int = Field(default=0, description="Chunk sequence number")
    start: int = Field(default=0, description="Chunk start position")
    end: int = Field(description="Chunk end position")
    images: List[Dict[str, Any]] = Field(
        default_factory=list, description="Images in the chunk"
    )

    metadata: Dict[str, Any] = Field(
        default_factory=dict,
        description="metadata fields",
    )

    def to_dict(self, **kwargs: Any) -> Dict[str, Any]:
        """Convert Chunk to dict."""

        data = self.model_dump()
        data.update(kwargs)
        data["class_name"] = self.__class__.__name__
        return data

    def to_json(self, **kwargs: Any) -> str:
        """Convert Chunk to json."""
        data = self.to_dict(**kwargs)
        return json.dumps(data)

    def __hash__(self):
        """Hash function."""
        return hash((self.content,))

    def __eq__(self, other):
        """Equal function."""
        return self.content == other.content

    @classmethod
    def from_dict(cls, data: Dict[str, Any], **kwargs: Any):  # type: ignore
        """Create Chunk from dict."""
        if isinstance(kwargs, dict):
            data.update(kwargs)

        data.pop("class_name", None)
        return cls(**data)

    @classmethod
    def from_json(cls, data_str: str, **kwargs: Any):  # type: ignore
        """Create Chunk from json."""
        data = json.loads(data_str)
        return cls.from_dict(data, **kwargs)


class Document(BaseModel):
    """Document including document content, document metadata."""

    model_config = {"arbitrary_types_allowed": True}

    content: str = Field(default="", description="document text content")
    images: Dict[str, str] = Field(
        default_factory=dict, description="Images in the document"
    )

    chunks: List[Chunk] = Field(default_factory=list, description="document chunks")
    metadata: Dict[str, Any] = Field(
        default_factory=dict,
        description="metadata fields",
    )

    def set_content(self, content: str) -> None:
        """Set document content."""
        self.content = content

    def get_content(self) -> str:
        """Get document content."""
        return self.content

    def is_valid(self) -> bool:
        return self.content != ""
