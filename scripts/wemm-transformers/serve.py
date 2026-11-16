#!/usr/bin/env python3
"""Serve WeMM text/image embeddings with the official Transformers model code."""
import argparse
import base64
import binascii
import hmac
import io
import logging
import os
import threading
import time
from contextlib import asynccontextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Literal

from fastapi import Depends, FastAPI, Header, HTTPException
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from PIL import Image, UnidentifiedImageError
from pydantic import BaseModel, ConfigDict, Field, StrictInt

log = logging.getLogger("uvicorn.error")


@dataclass(frozen=True)
class Settings:
    model: str = "tencent/WeMM-Embedding-2B"
    served_model_name: str | None = None
    revision: str | None = None
    device: str = "auto"
    dtype: str = "auto"
    max_tokens: int = 8192
    max_pixels: int = 1048576
    max_batch_size: int = 32
    cpu_threads: int = 8
    local_files_only: bool = False
    api_key: str = ""

    def __post_init__(self):
        if min(self.max_tokens, self.max_pixels, self.max_batch_size, self.cpu_threads) <= 0:
            raise ValueError("Resource limits and cpu_threads must be positive.")
        if self.max_pixels < 65536:
            raise ValueError("max_pixels must be at least 65536 (the model processor's minimum).")

    @property
    def name(self):
        if self.served_model_name:
            return self.served_model_name
        return Path(self.model).name if Path(self.model).is_dir() else self.model


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class TextPart(StrictModel):
    type: Literal["text"]
    text: str


class ImageURL(StrictModel):
    url: str = Field(max_length=16 * 1024 * 1024)
    detail: Literal["auto", "low", "high"] = "auto"


class ImagePart(StrictModel):
    type: Literal["image_url"]
    image_url: ImageURL


class Message(StrictModel):
    role: Literal["system", "user", "assistant"]
    content: str | list[TextPart | ImagePart] = Field(min_length=1)


class EmbedRequest(StrictModel):
    model: str
    input: str | list[str] | None = None
    messages: list[Message] | list[list[Message]] | None = None
    dimensions: StrictInt | None = None
    encoding_format: Literal["float", "base64"] = "float"
    user: str | None = None


def bad(message):
    raise HTTPException(400, message)


def parse_inputs(request, settings, dimensions, default_dimension):
    if request.model != settings.name:
        raise HTTPException(404, f"Model not found: {request.model}")
    dimension = default_dimension if request.dimensions is None else request.dimensions
    if dimension not in dimensions:
        bad(f"dimensions must be one of {dimensions}.")
    if (request.input is None) == (request.messages is None):
        bad("Supply exactly one of input or messages.")
    if request.input is not None:
        texts = [request.input] if isinstance(request.input, str) else request.input
        conversations = [[Message(role="user", content=[TextPart(type="text", text=text)])] for text in texts]
    else:
        messages = request.messages
        conversations = [messages] if messages and isinstance(messages[0], Message) else messages
    if not conversations or len(conversations) > settings.max_batch_size:
        bad(f"Supply between 1 and {settings.max_batch_size} independent inputs.")
    for conversation in conversations:
        if not conversation:
            bad("Each conversation must contain at least one message.")
        chars, images = 0, 0
        for message in conversation:
            parts = message.content
            if isinstance(parts, str):
                chars += len(parts)
                continue
            for part in parts:
                if isinstance(part, TextPart):
                    chars += len(part.text)
                else:
                    images += 1
                    if not part.image_url.url.startswith("data:image/") or ";base64," not in part.image_url.url:
                        bad("Images must be base64 data:image/... URLs; network URLs and file paths are unsupported.")
        if images > 5:
            bad("At most 5 images per conversation.")
        if chars > settings.max_tokens * 16:
            bad("Text exceeds the character guard; reduce the input size.")
    return conversations, dimension


def decode_image(url):
    try:
        data = base64.b64decode(url.split(";base64,", 1)[1], validate=True)
        with Image.open(io.BytesIO(data)) as image:
            if image.width * image.height > 40_000_000:
                bad("Image exceeds 40 megapixels before resizing.")
            # Preserve alpha until qwen-vl-utils composites RGBA onto white,
            # matching the official inference path instead of exposing hidden RGB.
            return image.copy()
    except (ValueError, binascii.Error, UnidentifiedImageError, OSError, Image.DecompressionBombError):
        bad("Invalid image data.")


def prepare_messages(conversation, max_pixels):
    messages = []
    for message in conversation:
        parts = message.content
        if isinstance(parts, str):
            parts = [TextPart(type="text", text=parts)]
        content = []
        for part in parts:
            if isinstance(part, TextPart):
                content.append({"type": "text", "text": part.text})
            else:
                content.append({"type": "image", "image": decode_image(part.image_url.url), "max_pixels": max_pixels})
        messages.append({"role": message.role, "content": content})
    return messages


def resolve_device_dtype(torch, settings):
    name = settings.device
    if name == "auto":
        name = "cuda" if torch.cuda.is_available() else "mps" if torch.backends.mps.is_available() else "cpu"
    device = torch.device(name)
    if device.type not in ("cuda", "mps", "cpu"):
        raise ValueError("device must be auto, cpu, mps, cuda or cuda:N.")
    if device.type == "cuda":
        if not torch.cuda.is_available():
            raise ValueError("CUDA is unavailable; install a CUDA-enabled PyTorch build or select another device.")
        if device.index is not None and device.index >= torch.cuda.device_count():
            raise ValueError("The requested CUDA device does not exist.")
        with torch.cuda.device(device):
            bf16 = torch.cuda.is_bf16_supported()
    elif device.type == "mps":
        if not torch.backends.mps.is_available():
            raise ValueError("MPS is unavailable on this machine.")
        bf16 = True
    else:
        bf16 = False
    dtype = settings.dtype
    if dtype == "auto":
        dtype = "float32" if device.type == "cpu" else "bfloat16" if bf16 else "float16"
    if device.type == "cuda" and dtype == "bfloat16" and not bf16:
        raise ValueError("This CUDA device does not support bfloat16; use --dtype float16.")
    return device, getattr(torch, dtype)


class Engine:
    def __init__(self, settings):
        # Import after CLI setup so --mps-fallback takes effect before torch loads.
        import torch
        from transformers import AutoConfig, AutoModel, AutoProcessor
        self.settings, self.torch = settings, torch
        self.device, self.dtype = resolve_device_dtype(torch, settings)
        self.lock = threading.Lock()
        torch.set_num_threads(settings.cpu_threads)
        options = {"trust_remote_code": True, "local_files_only": settings.local_files_only}
        if settings.revision:
            options["revision"] = settings.revision
        config = AutoConfig.from_pretrained(settings.model, **options)
        self.dimensions = tuple(getattr(config, "matryoshka_dimensions", ()))
        self.default_dimension = config.text_config.hidden_size
        if not self.dimensions or self.default_dimension not in self.dimensions:
            raise ValueError("The model must provide WeMM matryoshka_dimensions including its hidden size.")
        self.processor = AutoProcessor.from_pretrained(settings.model, **options)
        self.model = AutoModel.from_pretrained(
            settings.model, dtype=self.dtype, attn_implementation="sdpa", **options,
        ).to(self.device).eval()
        if not callable(getattr(self.model, "embedding", None)):
            raise ValueError("The model must expose WeMM's embedding() method.")

    def encode(self, conversations, dimension):
        from qwen_vl_utils import process_vision_info
        torch = self.torch
        vectors, total_tokens = [], 0
        # WeMM's model stores rope_deltas. Keep independent conversations serialized,
        # and decode images only for the current input rather than the entire batch.
        with self.lock, torch.inference_mode():
            for conversation in conversations:
                messages = prepare_messages(conversation, self.settings.max_pixels)
                text = self.processor.apply_chat_template(messages, tokenize=False, add_generation_prompt=False)
                images, _ = process_vision_info(messages, image_patch_size=16)
                inputs = self.processor(text=text, images=images, return_tensors="pt")
                count = inputs["input_ids"].shape[-1]
                if count > self.settings.max_tokens:
                    bad(f"Input has {count} tokens; limit is {self.settings.max_tokens}. Input was not truncated.")
                last = self.processor.tokenizer.convert_ids_to_tokens(inputs["input_ids"][0, -1].item())
                if last != "<embedding>":
                    raise RuntimeError("Processor did not append the official <embedding> token.")
                inputs = inputs.to(self.device)
                # Reset explicitly as well, so older official code revisions are safe
                # when a text request follows a multimodal request.
                self.model.model.rope_deltas = None
                vector = self.model.embedding(**inputs, use_cache=False).float()
                vector = torch.nn.functional.normalize(vector[:, :dimension], dim=-1).cpu()[0]
                if vector.numel() != dimension or not torch.isfinite(vector).all() or vector.norm() < 0.99:
                    raise RuntimeError("Invalid embedding output.")
                vectors.append(vector)
                total_tokens += count
                del inputs, images, messages
        return vectors, total_tokens


class BodyLimit:
    """Bound JSON bodies, including chunked requests, before parsing/base64 decoding."""
    def __init__(self, app, limit=32 * 1024 * 1024):
        self.app, self.limit = app, limit

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http" or scope["method"] != "POST":
            return await self.app(scope, receive, send)
        chunks, size = [], 0
        while True:
            message = await receive()
            if message["type"] == "http.disconnect":
                return
            chunk = message.get("body", b"")
            size += len(chunk)
            if size > self.limit:
                response = JSONResponse({"error": {"message": "Request body exceeds 32 MiB."}}, status_code=413)
                return await response(scope, receive, send)
            chunks.append(chunk)
            if not message.get("more_body", False):
                break
        body = b"".join(chunks)

        async def replay():
            nonlocal body
            if body is not None:
                value, body = body, None
                return {"type": "http.request", "body": value, "more_body": False}
            return await receive()

        await self.app(scope, replay, send)


def create_app(settings, engine_factory=Engine):
    @asynccontextmanager
    async def lifespan(app):
        started = time.monotonic()
        app.state.engine = engine_factory(settings)
        log.info("Loaded %s on %s/%s in %.1fs", settings.name, app.state.engine.device,
                 app.state.engine.dtype, time.monotonic() - started)
        yield
        del app.state.engine

    app = FastAPI(title="WeMM Transformers embedding service", lifespan=lifespan)
    app.add_middleware(BodyLimit)

    def authorize(authorization: str | None = Header(default=None)):
        expected = "Bearer " + settings.api_key
        if settings.api_key and not hmac.compare_digest((authorization or "").encode(), expected.encode()):
            raise HTTPException(401, "Invalid API key.", headers={"WWW-Authenticate": "Bearer"})

    @app.exception_handler(HTTPException)
    async def api_error(_request, exc):
        return JSONResponse(status_code=exc.status_code, headers=exc.headers,
                            content={"error": {"message": exc.detail, "code": exc.status_code}})

    @app.exception_handler(RequestValidationError)
    async def validation_error(_request, exc):
        return JSONResponse(status_code=400, content={"error": {
            "message": "Invalid request: " + exc.errors()[0]["msg"], "code": 400,
        }})

    @app.get("/health")
    def health():
        engine = app.state.engine
        return {"status": "ok", "model": settings.name, "backend": "transformers",
                "device": str(engine.device), "dtype": str(engine.dtype), "revision": settings.revision,
                "dimensions": engine.dimensions, "default_dimension": engine.default_dimension,
                "max_tokens": settings.max_tokens, "max_image_pixels": settings.max_pixels}

    @app.get("/v1/models", dependencies=[Depends(authorize)])
    def models():
        return {"object": "list", "data": [{"id": settings.name, "object": "model", "created": 0, "owned_by": "tencent"}]}

    @app.post("/v1/embeddings", dependencies=[Depends(authorize)])
    def embeddings(request: EmbedRequest):
        engine = app.state.engine
        conversations, dimension = parse_inputs(request, settings, engine.dimensions, engine.default_dimension)
        started = time.monotonic()
        try:
            vectors, tokens = engine.encode(conversations, dimension)
        except HTTPException:
            raise
        except Exception:
            log.exception("WeMM inference failed")
            raise HTTPException(500, "WeMM inference failed; inspect the server log.") from None
        data = []
        for index, vector in enumerate(vectors):
            value = (base64.b64encode(vector.numpy().astype("<f4").tobytes()).decode()
                     if request.encoding_format == "base64" else vector.tolist())
            data.append({"object": "embedding", "index": index, "embedding": value})
        log.info("Embedded %d inputs, %d tokens, %d dimensions in %.2fs", len(data), tokens,
                 dimension, time.monotonic() - started)
        return {"object": "list", "data": data, "model": settings.name,
                "usage": {"prompt_tokens": tokens, "total_tokens": tokens}}

    return app


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", default=Settings.model, help="WeMM Hugging Face ID or model directory")
    parser.add_argument("--served-model-name", help="API model ID; defaults to the repo ID or directory basename")
    parser.add_argument("--revision", help="Hugging Face revision (prefer a commit SHA for reproducibility)")
    parser.add_argument("--device", default="auto", help="auto, cuda, cuda:N, mps or cpu")
    parser.add_argument("--dtype", choices=("auto", "bfloat16", "float16", "float32"), default="auto")
    parser.add_argument("--max-tokens", type=int, default=8192)
    parser.add_argument("--max-pixels", type=int, default=1048576)
    parser.add_argument("--max-batch-size", type=int, default=32)
    parser.add_argument("--cpu-threads", type=int, default=8)
    parser.add_argument("--local-files-only", action="store_true")
    parser.add_argument("--mps-fallback", action="store_true", help="Allow PyTorch CPU fallback for unsupported MPS ops")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=18080)
    args = vars(parser.parse_args())
    host, port = args.pop("host"), args.pop("port")
    if args.pop("mps_fallback"):
        os.environ["PYTORCH_ENABLE_MPS_FALLBACK"] = "1"
    settings = Settings(**args, api_key=os.environ.get("WEMM_API_KEY", ""))
    import uvicorn
    # A single process owns the model; do not add workers without separate GPUs.
    uvicorn.run(create_app(settings), host=host, port=port, workers=1)


if __name__ == "__main__":
    main()
