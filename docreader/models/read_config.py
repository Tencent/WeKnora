from dataclasses import dataclass


@dataclass
class ChunkingConfig:
    """Legacy config kept for backward compatibility.

    After the lightweight refactoring, chunking is done in Go.
    This class is only kept so existing parser constructors don't break.
    """

    chunk_size: int = 512
    chunk_overlap: int = 50
    separators: list[str] | None = None
    enable_multimodal: bool = False
    storage_config: dict[str, str] | None = None
    vlm_config: dict[str, str] | None = None
