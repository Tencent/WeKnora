from dataclasses import dataclass, field


@dataclass
class ChunkingConfig:
    """
    Configuration for text chunking process.
    Controls how documents are split into smaller pieces for processing.
    """

    chunk_size: int = 512  # Maximum size of each chunk in tokens/chars
    chunk_overlap: int = 50  # Number of tokens/chars to overlap between chunks
    separators: list = field(
        default_factory=lambda: ["\n\n", "\n", "。"]
    )  # Text separators in order of priority
    enable_multimodal: bool = (
        False  # Whether to enable multimodal processing (text + images)
    )
    storage_config: dict = None  # Preferred field name going forward
    vlm_config: dict = None  # VLM configuration for image captioning

