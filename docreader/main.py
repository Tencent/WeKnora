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
from docreader.models.read_config import ChunkingConfig
from docreader.parser import Parser
from docreader.proto import docreader_pb2_grpc
from docreader.proto.docreader_pb2 import (
    Chunk,
    Image,
    ReadConfig,
    ReadFromFileRequest,
    ReadFromURLRequest,
    ReadResponse,
    StorageProvider,
)
from docreader.utils.request import init_logging_request_id, request_id_context

# Surrogate range U+D800..U+DFFF are invalid Unicode scalar values
# cannot be encoded to UTF-8
_SURROGATE_RE = re.compile(r"[\ud800-\udfff]")


def to_valid_utf8_text(s: Optional[str]) -> str:
    """Return a UTF-8 safe string for protobuf.

    - Replace any surrogate code points with U+FFFD
    - Re-encode with errors='replace' to ensure valid UTF-8
    """
    if not s:
        return ""
    s = _SURROGATE_RE.sub("\ufffd", s)
    return s.encode("utf-8", errors="replace").decode("utf-8")


# Ensure no existing handlers
for handler in logging.root.handlers[:]:
    logging.root.removeHandler(handler)

# Configure logging - use stdout
handler = logging.StreamHandler(sys.stdout)
logging.root.addHandler(handler)

logger = logging.getLogger(__name__)
logger.setLevel(logging.DEBUG)
logger.info("Initializing server logging")

# Initialize request ID logging
init_logging_request_id()


parser = Parser()


def create_chunking_config(read_config: ReadConfig):
    """Create ChunkingConfig from ReadConfig request.

    Args:
        read_config: The read_config from the gRPC request

    Returns:
        ChunkingConfig: Configured chunking configuration object
    """
    # Extract chunking parameters
    chunk_size = read_config.chunk_size or 512
    chunk_overlap = read_config.chunk_overlap or 50
    # Convert protobuf RepeatedScalarFieldContainer to list for type compatibility
    separators = (
        list(read_config.separators) if read_config.separators else ["\n\n", "\n", "。"]
    )
    enable_multimodal = read_config.enable_multimodal or False

    logger.info(
        f"Using chunking config: size={chunk_size}, "
        f"overlap={chunk_overlap}, multimodal={enable_multimodal}"
    )

    # Extract storage config
    sc = read_config.storage_config
    storage_config = {
        "provider": StorageProvider.Name(sc.provider),
        "region": sc.region,
        "bucket_name": sc.bucket_name,
        "access_key_id": sc.access_key_id,
        "secret_access_key": sc.secret_access_key,
        "app_id": sc.app_id,
        "path_prefix": sc.path_prefix,
    }
    logger.info(
        f"Using Storage config: provider={storage_config.get('provider')}, "
        f"bucket={storage_config['bucket_name']}"
    )

    # Extract VLM config
    vlm_config = {
        "model_name": read_config.vlm_config.model_name,
        "base_url": read_config.vlm_config.base_url,
        "api_key": read_config.vlm_config.api_key,
        "interface_type": read_config.vlm_config.interface_type or "openai",
    }

    logger.info(
        f"Using VLM config: model={vlm_config['model_name']}, "
        f"base_url={vlm_config['base_url']}, "
        f"interface_type={vlm_config['interface_type']}"
    )

    # Create and return ChunkingConfig
    return ChunkingConfig(
        chunk_size=chunk_size,
        chunk_overlap=chunk_overlap,
        separators=separators,
        enable_multimodal=enable_multimodal,
        storage_config=storage_config,
        vlm_config=vlm_config,
    )


class DocReaderServicer(docreader_pb2_grpc.DocReaderServicer):
    def __init__(self):
        super().__init__()
        self.parser = Parser()

    def ReadFromFile(self, request: ReadFromFileRequest, context):
        # Get or generate request ID
        request_id = (
            request.request_id
            if hasattr(request, "request_id") and request.request_id
            else str(uuid.uuid4())
        )

        # Use request ID context
        with request_id_context(request_id):
            try:
                # Get file type
                file_type = (
                    request.file_type or os.path.splitext(request.file_name)[1][1:]
                )
                logger.info(
                    f"ReadFromFile for file: {request.file_name}, type: {file_type}"
                )
                logger.info(f"File content size: {len(request.file_content)} bytes")

                # Create chunking config
                chunking_config = create_chunking_config(request.read_config)

                # Parse file
                logger.info("Starting file parsing process")
                result = self.parser.parse_file(
                    request.file_name, file_type, request.file_content, chunking_config
                )

                if not result:
                    error_msg = "Failed to parse file"
                    logger.error(error_msg)
                    context.set_code(grpc.StatusCode.INTERNAL)
                    context.set_details(error_msg)
                    return ReadResponse()

                # Convert to protobuf message
                logger.info(
                    f"Parsed file {request.file_name}, with {len(result.chunks)} chunks"
                )

                # Build response, including image info
                response = ReadResponse(
                    chunks=[
                        self._convert_chunk_to_proto(chunk) for chunk in result.chunks
                    ]
                )
                logger.info(f"Response size: {response.ByteSize()} bytes")
                return response

            except Exception as e:
                error_msg = f"Error reading file: {str(e)}"
                logger.error(error_msg)
                logger.info(f"Detailed traceback: {traceback.format_exc()}")
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details(str(e))
                return ReadResponse(error=str(e))

    def ReadFromURL(self, request: ReadFromURLRequest, context):
        # Get or generate request ID
        request_id = (
            request.request_id
            if hasattr(request, "request_id") and request.request_id
            else str(uuid.uuid4())
        )

        # Use request ID context
        with request_id_context(request_id):
            try:
                logger.info(f"Received ReadFromURL request for URL: {request.url}")

                # Create chunking config
                chunking_config = create_chunking_config(request.read_config)

                # Parse URL
                logger.info("Starting URL parsing process")
                result = self.parser.parse_url(
                    request.url, request.title, chunking_config
                )
                if not result:
                    error_msg = "Failed to parse URL"
                    logger.error(error_msg)
                    context.set_code(grpc.StatusCode.INTERNAL)
                    context.set_details(error_msg)
                    return ReadResponse(error=error_msg)

                # Convert to protobuf message, including image info
                logger.info(
                    f"Parsed URL {request.url}, returning {len(result.chunks)} chunks"
                )

                response = ReadResponse(
                    chunks=[
                        self._convert_chunk_to_proto(chunk) for chunk in result.chunks
                    ]
                )
                logger.info(f"Response size: {response.ByteSize()} bytes")
                return response

            except Exception as e:
                error_msg = f"Error reading URL: {str(e)}"
                logger.error(error_msg)
                logger.info(f"Detailed traceback: {traceback.format_exc()}")
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details(str(e))
                return ReadResponse(error=str(e))

    def _convert_chunk_to_proto(self, chunk):
        """Convert internal Chunk object to protobuf Chunk message
        Ensures all string fields are valid UTF-8 for protobuf (no lone surrogates).
        """
        # Clean helper for strings
        _c = to_valid_utf8_text

        proto_chunk = Chunk(
            content=_c(getattr(chunk, "content", None)),
            seq=getattr(chunk, "seq", 0),
            start=getattr(chunk, "start", 0),
            end=getattr(chunk, "end", 0),
        )

        # If chunk has images attribute and is not empty, add image info
        if hasattr(chunk, "images") and chunk.images:
            logger.info(
                f"Adding {len(chunk.images)} images to chunk {getattr(chunk, 'seq', 0)}"
            )
            for img_info in chunk.images:
                # img_info expected as dict
                proto_image = Image(
                    url=_c(img_info.get("cos_url", "")),
                    caption=_c(img_info.get("caption", "")),
                    ocr_text=_c(img_info.get("ocr_text", "")),
                    original_url=_c(img_info.get("original_url", "")),
                    start=int(img_info.get("start", 0) or 0),
                    end=int(img_info.get("end", 0) or 0),
                )
                proto_chunk.images.append(proto_image)

        return proto_chunk


def main():
    # Print effective env/config at startup
    config.print_config()

    # Create server
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=CONFIG.grpc_max_workers),
        options=[
            ("grpc.max_send_message_length", CONFIG.grpc_max_file_size_mb),
            ("grpc.max_receive_message_length", CONFIG.grpc_max_file_size_mb),
        ],
    )

    # Register services
    docreader_pb2_grpc.add_DocReaderServicer_to_server(DocReaderServicer(), server)

    # Register health check service
    health_servicer = HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    # Set listen address
    server.add_insecure_port(f"[::]:{CONFIG.grpc_port}")

    # Start service
    server.start()

    logger.info(f"Server started on port {CONFIG.grpc_port}")
    logger.info("Server is ready to accept connections")

    try:
        # Wait for service termination
        server.wait_for_termination()
    except KeyboardInterrupt:
        logger.info("Received termination signal, shutting down server")
        server.stop(0)


if __name__ == "__main__":
    main()
