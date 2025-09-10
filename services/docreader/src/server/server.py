import os
import sys
import logging
from concurrent import futures
import traceback
import grpc
import uuid
import atexit
from grpc_health.v1 import health_pb2_grpc
from grpc_health.v1.health import HealthServicer

# Add parent directory to Python path
current_dir = os.path.dirname(os.path.abspath(__file__))
parent_dir = os.path.dirname(current_dir)
if parent_dir not in sys.path:
    sys.path.insert(0, parent_dir)

from proto.docreader_pb2 import ReadResponse, Chunk, Image
from proto import docreader_pb2_grpc
from parser import Parser, OCREngine
from parser.config import ChunkingConfig
from utils.request import request_id_context, init_logging_request_id

# --- Encoding utilities: sanitize strings to valid UTF-8 and (optionally) multi-encoding read ---
import re
from typing import Optional

try:
    # Optional dependency for charset detection; install via `pip install charset-normalizer`
    from charset_normalizer import from_bytes as _cn_from_bytes  # type: ignore
except Exception:  # pragma: no cover
    _cn_from_bytes = None  # type: ignore

# Surrogate range U+D800..U+DFFF are invalid Unicode scalar values and cannot be encoded to UTF-8
_SURROGATE_RE = re.compile(r"[\ud800-\udfff]")

def to_valid_utf8_text(s: Optional[str]) -> str:
    """Return a UTF-8 safe string for protobuf.

    - Replace any surrogate code points with U+FFFD
    - Re-encode with errors='replace' to ensure valid UTF-8
    """
    if not s:
        return ""
    s = _SURROGATE_RE.sub("\uFFFD", s)
    return s.encode("utf-8", errors="replace").decode("utf-8")

def read_text_with_fallback(file_path: str) -> str:
    """Read text from file supporting multiple encodings with graceful fallback.

    This server currently receives bytes over gRPC and delegates decoding to the parser.
    This helper is provided for future local-file reads if needed.
    """
    with open(file_path, "rb") as f:
        raw = f.read()
    if _cn_from_bytes is not None:
        try:
            result = _cn_from_bytes(raw).best()
            if result:
                return str(result)
        except Exception:
            pass
    for enc in ("utf-8", "gb18030", "latin-1"):
        try:
            return raw.decode(enc, errors="replace")
        except UnicodeDecodeError:
            continue
    return raw.decode("utf-8", errors="replace")

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

# Set max message size to 50MB
MAX_MESSAGE_LENGTH = 50 * 1024 * 1024


parser = Parser()

class DocReaderServicer(docreader_pb2_grpc.DocReaderServicer):
    def __init__(self):
        super().__init__()
        self.parser = Parser()

    def ReadFromFile(self, request, context):
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
                    f"Received ReadFromFile request for file: {request.file_name}, type: {file_type}"
                )
                logger.info(f"File content size: {len(request.file_content)} bytes")

                # Create chunking config
                chunk_size = request.read_config.chunk_size or 512
                chunk_overlap = request.read_config.chunk_overlap or 50
                separators = request.read_config.separators or ["\n\n", "\n", "。"]
                enable_multimodal = request.read_config.enable_multimodal or False

                logger.info(
                    f"Using chunking config: size={chunk_size}, overlap={chunk_overlap}, "
                    f"multimodal={enable_multimodal}"
                )

                # Get Storage and VLM config from request
                storage_config = None
                vlm_config = None
                
                sc = request.read_config.storage_config
                # Keep parser-side key name as cos_config for backward compatibility
                storage_config = {
                    'provider': 'minio' if sc.provider == 2 else 'cos',
                    'region': sc.region,
                    'bucket_name': sc.bucket_name,
                    'access_key_id': sc.access_key_id,
                    'secret_access_key': sc.secret_access_key,
                    'app_id': sc.app_id,
                    'path_prefix': sc.path_prefix,
                }
                logger.info(f"Using Storage config: provider={storage_config.get('provider')}, bucket={storage_config['bucket_name']}")
                
                vlm_config = {
                    'model_name': request.read_config.vlm_config.model_name,
                    'base_url': request.read_config.vlm_config.base_url,
                    'api_key': request.read_config.vlm_config.api_key or '',
                    'interface_type': request.read_config.vlm_config.interface_type or 'openai',
                }
                logger.info(f"Using VLM config: model={vlm_config['model_name']}, "
                                f"base_url={vlm_config['base_url']}, "
                                f"interface_type={vlm_config['interface_type']}")

                chunking_config = ChunkingConfig(
                    chunk_size=chunk_size,
                    chunk_overlap=chunk_overlap,
                    separators=separators,
                    enable_multimodal=enable_multimodal,
                    storage_config=storage_config,
                    vlm_config=vlm_config,
                )

                # Parse file
                logger.info(f"Starting file parsing process")
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
                    f"Successfully parsed file {request.file_name}, returning {len(result.chunks)} chunks"
                )
                
                # Build response, including image info
                response = ReadResponse(
                    chunks=[self._convert_chunk_to_proto(chunk) for chunk in result.chunks]
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

    def ReadFromURL(self, request, context):
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
                chunk_size = request.read_config.chunk_size or 512
                chunk_overlap = request.read_config.chunk_overlap or 50
                separators = request.read_config.separators or ["\n\n", "\n", "。"]
                enable_multimodal = request.read_config.enable_multimodal or False

                logger.info(
                    f"Using chunking config: size={chunk_size}, overlap={chunk_overlap}, "
                    f"multimodal={enable_multimodal}"
                )

                # Get Storage and VLM config from request
                storage_config = None
                vlm_config = None
                
                sc = request.read_config.storage_config
                storage_config = {
                    'provider': 'minio' if sc.provider == 2 else 'cos',
                    'region': sc.region,
                    'bucket_name': sc.bucket_name,
                    'access_key_id': sc.access_key_id,
                    'secret_access_key': sc.secret_access_key,
                    'app_id': sc.app_id,
                    'path_prefix': sc.path_prefix,
                }
                logger.info(f"Using Storage config: provider={storage_config.get('provider')}, bucket={storage_config['bucket_name']}") 

                vlm_config = {
                    'model_name': request.read_config.vlm_config.model_name,
                    'base_url': request.read_config.vlm_config.base_url,
                    'api_key': request.read_config.vlm_config.api_key or '',
                    'interface_type': request.read_config.vlm_config.interface_type or 'openai',
                }
                logger.info(f"Using VLM config: model={vlm_config['model_name']}, "
                                f"base_url={vlm_config['base_url']}, "
                                f"interface_type={vlm_config['interface_type']}")
                    
                chunking_config = ChunkingConfig(
                    chunk_size=chunk_size,
                    chunk_overlap=chunk_overlap,
                    separators=separators,
                    enable_multimodal=enable_multimodal,
                    storage_config=storage_config,
                    vlm_config=vlm_config,
                )

                # Parse URL
                logger.info(f"Starting URL parsing process")
                result = self.parser.parse_url(request.url, request.title, chunking_config)
                if not result:
                    error_msg = "Failed to parse URL"
                    logger.error(error_msg)
                    context.set_code(grpc.StatusCode.INTERNAL)
                    context.set_details(error_msg)
                    return ReadResponse(error=error_msg)

                # Convert to protobuf message, including image info
                logger.info(
                    f"Successfully parsed URL {request.url}, returning {len(result.chunks)} chunks"
                )
                
                response = ReadResponse(
                    chunks=[self._convert_chunk_to_proto(chunk) for chunk in result.chunks]
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
            logger.info(f"Adding {len(chunk.images)} images to chunk {getattr(chunk, 'seq', 0)}")
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

def init_ocr_engine(ocr_backend, ocr_config):
    """Initialize OCR engine"""
    try:
        logger.info(f"Initializing OCR engine with backend: {ocr_backend}")
        ocr_engine = OCREngine.get_instance(backend_type=ocr_backend, **ocr_config)
        if ocr_engine:
            logger.info("OCR engine initialized successfully")
            return True
        else:
            logger.error("OCR engine initialization failed")
            return False
    except Exception as e:
        logger.error(f"Error initializing OCR engine: {str(e)}")
        return False


def serve():
    
    init_ocr_engine(os.getenv("OCR_BACKEND", "paddle"), {
        "OCR_API_BASE_URL": os.getenv("OCR_API_BASE_URL", ""),
    })
    
    # Set max number of worker threads
    max_workers = int(os.environ.get("GRPC_MAX_WORKERS", "4"))
    logger.info(f"Starting DocReader service with {max_workers} worker threads")
    
    # Get port number
    port = os.environ.get("GRPC_PORT", "50051")
    
    # Create server
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=max_workers),
        options=[
            ('grpc.max_send_message_length', MAX_MESSAGE_LENGTH),
            ('grpc.max_receive_message_length', MAX_MESSAGE_LENGTH),
        ],
    )
    
    # Register services
    docreader_pb2_grpc.add_DocReaderServicer_to_server(DocReaderServicer(), server)
    
    # Register health check service
    health_servicer = HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)
    
    # Set listen address
    server.add_insecure_port(f"[::]:{port}")
    
    # Start service
    server.start()
    
    logger.info(f"Server started on port {port}")
    logger.info("Server is ready to accept connections")
    
    try:
        # Wait for service termination
        server.wait_for_termination()
    except KeyboardInterrupt:
        logger.info("Received termination signal, shutting down server")
        server.stop(0)

if __name__ == "__main__":
    serve()
