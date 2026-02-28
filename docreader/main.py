import logging
import os
import re
import sys
import traceback
import uuid
from concurrent import futures
from typing import Optional

import grpc
from grpc_health.v1 import health_pb2_grpc
from grpc_health.v1.health import HealthServicer

from docreader import config
from docreader.config import CONFIG
from docreader.parser import Parser
from docreader.proto import docreader_pb2_grpc
from docreader.parser.registry import registry
from docreader.proto.docreader_pb2 import (
    ReadRequest,
    ReadResponse,
    ImageRef,
    ListEnginesResponse,
    ParserEngineInfo,
)
from docreader.utils.request import init_logging_request_id, request_id_context

_SURROGATE_RE = re.compile(r"[\ud800-\udfff]")


def to_valid_utf8_text(s: Optional[str]) -> str:
    if not s:
        return ""
    s = _SURROGATE_RE.sub("\ufffd", s)
    return s.encode("utf-8", errors="replace").decode("utf-8")


for handler in logging.root.handlers[:]:
    logging.root.removeHandler(handler)

handler = logging.StreamHandler(sys.stdout)
logging.root.addHandler(handler)

_level_name = (os.environ.get("LOG_LEVEL") or "INFO").upper()
_level = getattr(logging, _level_name, logging.INFO)
logging.root.setLevel(_level)

logger = logging.getLogger(__name__)
logger.info("Initializing server logging, level=%s", _level_name)

init_logging_request_id()


def _resolve_images(images: dict, request_id: str, storage_map: dict | None = None) -> tuple[str, list]:
    """Resolve document images with priority: shared storage > temp dir > inline bytes.

    ``images`` is a dict of {relative_path: raw_data} where raw_data is
    base64-encoded string or raw bytes.

    ``storage_map`` is a dict from the request's config.image_storage map.
    When it contains a valid "provider" key, images are uploaded to shared object
    storage and storage_key is set (no inline bytes, saving gRPC message size).

    Returns (image_dir_path, list[ImageRef]).
    """
    import base64

    if not images:
        return "", []

    mime_map = {
        ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
        ".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
    }

    storage_client = None
    use_storage = False
    provider = (storage_map or {}).get("provider", "")
    if provider:
        try:
            from docreader.parser.storage import create_storage
            storage_client = create_storage(storage_map)
            use_storage = True
            logger.info("Using shared storage (%s) for image upload", provider)
        except Exception as e:
            logger.warning("Failed to init shared storage, falling back to inline bytes: %s", e)

    base_dir = CONFIG.image_output_dir
    output_dir = os.path.join(base_dir, request_id, "images")
    wrote_to_dir = False
    if not use_storage:
        try:
            os.makedirs(output_dir, exist_ok=True)
            wrote_to_dir = True
        except OSError:
            logger.warning("Cannot write to image output dir %s", output_dir)

    refs = []
    for ref_path, b64data in images.items():
        try:
            img_bytes = base64.b64decode(b64data)
        except Exception:
            img_bytes = b64data.encode("utf-8") if isinstance(b64data, str) else b64data

        fname = os.path.basename(ref_path) or f"{uuid.uuid4().hex}.png"
        ext = os.path.splitext(fname)[1].lower()
        mime = mime_map.get(ext, "application/octet-stream")

        if use_storage and storage_client:
            storage_key = storage_client.upload_bytes(img_bytes, ext)
            if storage_key:
                refs.append(ImageRef(
                    filename=fname,
                    original_ref=ref_path,
                    mime_type=mime,
                    storage_key=storage_key,
                ))
                continue
            logger.warning("Storage upload failed for %s, falling back to inline bytes", fname)

        if wrote_to_dir:
            dest = os.path.join(output_dir, fname)
            try:
                with open(dest, "wb") as f:
                    f.write(img_bytes)
            except OSError as e:
                logger.warning("Failed to write image %s: %s", dest, e)

        refs.append(ImageRef(
            filename=fname,
            original_ref=ref_path,
            mime_type=mime,
            image_data=img_bytes,
        ))

    image_dir = os.path.join(base_dir, request_id) if wrote_to_dir else ""
    mode = "storage" if use_storage else ("dir+inline" if wrote_to_dir else "inline")
    logger.info("Resolved %d images (mode=%s)", len(refs), mode)
    return image_dir, refs


class DocReaderServicer(docreader_pb2_grpc.DocReaderServicer):
    def __init__(self):
        super().__init__()
        self.parser = Parser()

    def Read(self, request: ReadRequest, context):
        """Unified read: file mode (file_content set) or URL mode (url set)."""
        request_id = request.request_id or str(uuid.uuid4())
        is_url = bool(request.url)

        with request_id_context(request_id):
            try:
                cfg = request.config
                parser_engine = cfg.parser_engine if cfg else ""
                engine_overrides = dict(cfg.parser_engine_overrides) if cfg else {}
                storage_map = dict(cfg.image_storage) if cfg and cfg.image_storage else None

                if is_url:
                    logger.info("Read(URL): url=%s", request.url)
                    result = self.parser.parse_url(
                        request.url,
                        request.title,
                        parser_engine=parser_engine,
                        engine_overrides=engine_overrides,
                    )
                    source_desc = request.url
                else:
                    file_type = (
                        request.file_type or os.path.splitext(request.file_name)[1][1:]
                    )
                    logger.info(
                        "Read(File): file=%s, type=%s, size=%d bytes",
                        request.file_name, file_type, len(request.file_content),
                    )
                    result = self.parser.parse_file(
                        request.file_name,
                        file_type,
                        request.file_content,
                        parser_engine=parser_engine,
                        engine_overrides=engine_overrides,
                    )
                    source_desc = request.file_name

                if not result or not result.content:
                    error_msg = f"Failed to parse: {source_desc}"
                    logger.error(error_msg)
                    return ReadResponse(error=error_msg)

                _c = to_valid_utf8_text
                image_dir, image_refs = _resolve_images(
                    result.images, request_id, storage_map=storage_map
                )

                response = ReadResponse(
                    markdown_content=_c(result.content),
                    image_refs=image_refs,
                    image_dir_path=image_dir,
                )
                logger.info(
                    "Read response: content_len=%d, images=%d",
                    len(result.content), len(image_refs),
                )
                return response

            except Exception as e:
                error_msg = f"Error reading document: {e}"
                logger.error(error_msg)
                logger.info("Traceback: %s", traceback.format_exc())
                return ReadResponse(error=str(e))

    def ListEngines(self, request, context):
        overrides = dict(getattr(request, "config_overrides", None) or {})
        engines_data = registry.list_engines(overrides=overrides or None)
        engines = [
            ParserEngineInfo(
                name=e["name"],
                description=e["description"],
                file_types=e["file_types"],
                available=e.get("available", True),
                unavailable_reason=e.get("unavailable_reason", ""),
            )
            for e in engines_data
        ]
        return ListEnginesResponse(engines=engines)


def main():
    config.print_config()

    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=CONFIG.grpc_max_workers),
        options=[
            ("grpc.max_send_message_length", CONFIG.grpc_max_file_size_mb),
            ("grpc.max_receive_message_length", CONFIG.grpc_max_file_size_mb),
        ],
    )

    docreader_pb2_grpc.add_DocReaderServicer_to_server(DocReaderServicer(), server)

    health_servicer = HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    server.add_insecure_port(f"[::]:{CONFIG.grpc_port}")
    server.start()

    logger.info("Server started on port %d", CONFIG.grpc_port)
    logger.info("Server is ready to accept connections")

    try:
        server.wait_for_termination()
    except KeyboardInterrupt:
        logger.info("Received termination signal, shutting down server")
        server.stop(0)


if __name__ == "__main__":
    main()
