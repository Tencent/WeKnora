from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class StorageProvider(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    STORAGE_PROVIDER_UNSPECIFIED: _ClassVar[StorageProvider]
    COS: _ClassVar[StorageProvider]
    MINIO: _ClassVar[StorageProvider]
STORAGE_PROVIDER_UNSPECIFIED: StorageProvider
COS: StorageProvider
MINIO: StorageProvider

class StorageConfig(_message.Message):
    __slots__ = ("provider", "region", "bucket_name", "access_key_id", "secret_access_key", "app_id", "path_prefix")
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    REGION_FIELD_NUMBER: _ClassVar[int]
    BUCKET_NAME_FIELD_NUMBER: _ClassVar[int]
    ACCESS_KEY_ID_FIELD_NUMBER: _ClassVar[int]
    SECRET_ACCESS_KEY_FIELD_NUMBER: _ClassVar[int]
    APP_ID_FIELD_NUMBER: _ClassVar[int]
    PATH_PREFIX_FIELD_NUMBER: _ClassVar[int]
    provider: StorageProvider
    region: str
    bucket_name: str
    access_key_id: str
    secret_access_key: str
    app_id: str
    path_prefix: str
    def __init__(self, provider: _Optional[_Union[StorageProvider, str]] = ..., region: _Optional[str] = ..., bucket_name: _Optional[str] = ..., access_key_id: _Optional[str] = ..., secret_access_key: _Optional[str] = ..., app_id: _Optional[str] = ..., path_prefix: _Optional[str] = ...) -> None: ...

class VLMConfig(_message.Message):
    __slots__ = ("model_name", "base_url", "api_key", "interface_type")
    MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    API_KEY_FIELD_NUMBER: _ClassVar[int]
    INTERFACE_TYPE_FIELD_NUMBER: _ClassVar[int]
    model_name: str
    base_url: str
    api_key: str
    interface_type: str
    def __init__(self, model_name: _Optional[str] = ..., base_url: _Optional[str] = ..., api_key: _Optional[str] = ..., interface_type: _Optional[str] = ...) -> None: ...

class ReadConfig(_message.Message):
    __slots__ = ("chunk_size", "chunk_overlap", "separators", "enable_multimodal", "storage_config", "vlm_config")
    CHUNK_SIZE_FIELD_NUMBER: _ClassVar[int]
    CHUNK_OVERLAP_FIELD_NUMBER: _ClassVar[int]
    SEPARATORS_FIELD_NUMBER: _ClassVar[int]
    ENABLE_MULTIMODAL_FIELD_NUMBER: _ClassVar[int]
    STORAGE_CONFIG_FIELD_NUMBER: _ClassVar[int]
    VLM_CONFIG_FIELD_NUMBER: _ClassVar[int]
    chunk_size: int
    chunk_overlap: int
    separators: _containers.RepeatedScalarFieldContainer[str]
    enable_multimodal: bool
    storage_config: StorageConfig
    vlm_config: VLMConfig
    def __init__(self, chunk_size: _Optional[int] = ..., chunk_overlap: _Optional[int] = ..., separators: _Optional[_Iterable[str]] = ..., enable_multimodal: bool = ..., storage_config: _Optional[_Union[StorageConfig, _Mapping]] = ..., vlm_config: _Optional[_Union[VLMConfig, _Mapping]] = ...) -> None: ...

class ReadFromFileRequest(_message.Message):
    __slots__ = ("file_content", "file_name", "file_type", "read_config", "request_id")
    FILE_CONTENT_FIELD_NUMBER: _ClassVar[int]
    FILE_NAME_FIELD_NUMBER: _ClassVar[int]
    FILE_TYPE_FIELD_NUMBER: _ClassVar[int]
    READ_CONFIG_FIELD_NUMBER: _ClassVar[int]
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    file_content: bytes
    file_name: str
    file_type: str
    read_config: ReadConfig
    request_id: str
    def __init__(self, file_content: _Optional[bytes] = ..., file_name: _Optional[str] = ..., file_type: _Optional[str] = ..., read_config: _Optional[_Union[ReadConfig, _Mapping]] = ..., request_id: _Optional[str] = ...) -> None: ...

class ReadFromURLRequest(_message.Message):
    __slots__ = ("url", "title", "read_config", "request_id")
    URL_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    READ_CONFIG_FIELD_NUMBER: _ClassVar[int]
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    url: str
    title: str
    read_config: ReadConfig
    request_id: str
    def __init__(self, url: _Optional[str] = ..., title: _Optional[str] = ..., read_config: _Optional[_Union[ReadConfig, _Mapping]] = ..., request_id: _Optional[str] = ...) -> None: ...

class Image(_message.Message):
    __slots__ = ("url", "caption", "ocr_text", "original_url", "start", "end")
    URL_FIELD_NUMBER: _ClassVar[int]
    CAPTION_FIELD_NUMBER: _ClassVar[int]
    OCR_TEXT_FIELD_NUMBER: _ClassVar[int]
    ORIGINAL_URL_FIELD_NUMBER: _ClassVar[int]
    START_FIELD_NUMBER: _ClassVar[int]
    END_FIELD_NUMBER: _ClassVar[int]
    url: str
    caption: str
    ocr_text: str
    original_url: str
    start: int
    end: int
    def __init__(self, url: _Optional[str] = ..., caption: _Optional[str] = ..., ocr_text: _Optional[str] = ..., original_url: _Optional[str] = ..., start: _Optional[int] = ..., end: _Optional[int] = ...) -> None: ...

class Chunk(_message.Message):
    __slots__ = ("content", "seq", "start", "end", "images")
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    SEQ_FIELD_NUMBER: _ClassVar[int]
    START_FIELD_NUMBER: _ClassVar[int]
    END_FIELD_NUMBER: _ClassVar[int]
    IMAGES_FIELD_NUMBER: _ClassVar[int]
    content: str
    seq: int
    start: int
    end: int
    images: _containers.RepeatedCompositeFieldContainer[Image]
    def __init__(self, content: _Optional[str] = ..., seq: _Optional[int] = ..., start: _Optional[int] = ..., end: _Optional[int] = ..., images: _Optional[_Iterable[_Union[Image, _Mapping]]] = ...) -> None: ...

class ReadResponse(_message.Message):
    __slots__ = ("chunks", "error")
    CHUNKS_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    chunks: _containers.RepeatedCompositeFieldContainer[Chunk]
    error: str
    def __init__(self, chunks: _Optional[_Iterable[_Union[Chunk, _Mapping]]] = ..., error: _Optional[str] = ...) -> None: ...
