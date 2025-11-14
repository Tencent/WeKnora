from dataclasses import dataclass, field


@dataclass
class ChunkingConfig:
    """
    Configuration for text chunking process.
    Controls how documents are split into smaller pieces for processing.
    """

    # Maximum size of each chunk in tokens/chars
    chunk_size: int = 512

    # Number of tokens/chars to overlap between chunks
    chunk_overlap: int = 50

    # Text separators in order of priority
    separators: list = field(default_factory=lambda: ["\n\n", "\n", "。"])

    # Whether to enable multimodal processing (text + images)
    enable_multimodal: bool = False

    # Preferred field name going forward
    storage_config: dict[str, str] = field(default_factory=dict)

    # VLM configuration for image captioning
    vlm_config: dict[str, str] = field(default_factory=dict)
