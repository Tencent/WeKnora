"""Contract tests without downloading weights: python -m unittest discover -s scripts/wemm-transformers."""
import asyncio
import base64
import io
import struct
import unittest
from contextlib import nullcontext
from unittest.mock import patch

import torch
from fastapi import HTTPException
from fastapi.testclient import TestClient
from PIL import Image
from serve import BodyLimit, Message, Settings, create_app, decode_image, prepare_messages, resolve_device_dtype


class FakeEngine:
    device, dtype = "cpu", "float32"

    def __init__(self, settings, dimensions=(64, 128, 256, 512, 1024, 2048)):
        self.dimensions = dimensions
        self.default_dimension = dimensions[-1]
        self.calls = []

    def encode(self, conversations, dimension):
        self.calls.append(conversations)
        vectors = []
        for index, _ in enumerate(conversations):
            vector = torch.zeros(dimension)
            vector[index] = 1
            vectors.append(vector)
        return vectors, len(conversations) * 10


class ServiceTests(unittest.TestCase):
    def setUp(self):
        self.settings = Settings()
        self.engine = FakeEngine(self.settings)
        self.client = self.enterContext(TestClient(create_app(self.settings, lambda _: self.engine)))

    def post(self, **payload):
        return self.client.post("/v1/embeddings", json={"model": self.settings.name, **payload})

    def test_input_and_single_or_batched_messages(self):
        message = {"role": "user", "content": [{"type": "text", "text": "hello"}]}
        for payload, count in [({"input": "hello"}, 1), ({"input": ["a", "b"]}, 2),
                               ({"messages": [message]}, 1), ({"messages": [[message], [message]]}, 2)]:
            with self.subTest(payload=payload):
                response = self.post(**payload)
                self.assertEqual(response.status_code, 200, response.text)
                data = response.json()["data"]
                self.assertEqual([x["index"] for x in data], list(range(count)))
                self.assertTrue(all(len(x["embedding"]) == 2048 for x in data))
                self.assertEqual([x["embedding"][i] for i, x in enumerate(data)], [1] * count)

    def test_all_model_sizes_use_their_own_default_and_allowed_dimensions(self):
        for size, dimensions in [("2B", (64, 128, 256, 512, 1024, 2048)),
                                 ("4B", (64, 128, 256, 512, 1024, 2560)),
                                 ("9B", (64, 128, 256, 512, 1024, 2048, 4096))]:
            settings = Settings(model="tencent/WeMM-Embedding-" + size)
            with self.subTest(size=size), TestClient(create_app(settings, lambda s: FakeEngine(s, dimensions))) as client:
                for requested in (None, 1024):
                    payload = {"model": settings.name, "input": "hello"}
                    if requested is not None:
                        payload["dimensions"] = requested
                    response = client.post("/v1/embeddings", json=payload)
                    self.assertEqual(response.status_code, 200, response.text)
                    self.assertEqual(len(response.json()["data"][0]["embedding"]), requested or dimensions[-1])
                payload["dimensions"] = 2049
                self.assertEqual(client.post("/v1/embeddings", json=payload).status_code, 400)
                self.assertEqual(client.get("/health").json()["default_dimension"], dimensions[-1])

    def test_base64_is_little_endian_float32(self):
        response = self.post(input="hello", dimensions=64, encoding_format="base64")
        vector = struct.unpack("<64f", base64.b64decode(response.json()["data"][0]["embedding"]))
        self.assertEqual(vector, (1,) + (0,) * 63)

    def test_invalid_requests_never_reach_inference(self):
        for payload in [dict(input="a", messages=[]), dict(input=[]), dict(messages=[[]]),
                        dict(input="a", dimensions=True), dict(input="a", dimensions=0),
                        dict(input="a", temperature=0), dict(input=["a"] * 33),
                        dict(messages=[{"role": "user", "content": [{"type": "video", "video": "x"}]}]),
                        dict(messages=[{"role": "user", "content": [{"type": "image_url", "image_url": {"url": "file:///etc/passwd"}}]}])]:
            with self.subTest(payload=payload):
                self.assertEqual(self.post(**payload).status_code, 400)
        self.assertEqual(self.post(model="wrong", input="a").status_code, 404)
        self.assertEqual(self.engine.calls, [])

    def test_authentication_on_models_and_embeddings(self):
        settings = Settings(api_key="test-only-key")
        with TestClient(create_app(settings, lambda _: self.engine)) as client:
            self.assertEqual(client.get("/health").status_code, 200)
            self.assertEqual(client.get("/v1/models").status_code, 401)
            for token, status in [("Bearer wrong", 401), ("Bearer test-only-key", 200)]:
                response = client.post("/v1/embeddings", json={"model": settings.name, "input": "a"},
                                       headers={"Authorization": token})
                self.assertEqual(response.status_code, status)
            self.assertEqual(len(self.engine.calls), 1)

    def test_image_decoding_and_invalid_bytes(self):
        buffer = io.BytesIO()
        Image.new("RGB", (64, 64), "red").save(buffer, format="PNG")
        uri = "data:image/png;base64," + base64.b64encode(buffer.getvalue()).decode()
        self.assertEqual(decode_image(uri).size, (64, 64))
        for data in ["!invalid!", base64.b64encode(b"not an image").decode()]:
            with self.assertRaises(HTTPException) as error:
                decode_image("data:image/png;base64," + data)
            self.assertEqual(error.exception.status_code, 400)

    def test_transparent_images_match_official_white_background(self):
        from qwen_vl_utils import process_vision_info
        image = Image.new("RGBA", (256, 256), (0, 0, 0, 0))
        image.putpixel((128, 128), (255, 0, 0, 128))
        buffer = io.BytesIO()
        image.save(buffer, format="PNG")
        uri = "data:image/png;base64," + base64.b64encode(buffer.getvalue()).decode()
        conversation = [Message.model_validate({"role": "user", "content": [
            {"type": "image_url", "image_url": {"url": uri}},
        ]})]
        actual, _ = process_vision_info(prepare_messages(conversation, 1048576), image_patch_size=16)
        expected, _ = process_vision_info([{"role": "user", "content": [
            {"type": "image", "image": image, "max_pixels": 1048576},
        ]}], image_patch_size=16)
        self.assertEqual(actual[0].getpixel((0, 0)), (255, 255, 255))
        self.assertEqual(actual[0].getpixel((128, 128)), (255, 127, 127))
        self.assertEqual(actual[0].tobytes(), expected[0].tobytes())

    def test_cpu_defaults_and_explicit_unavailable_device(self):
        with patch.object(torch.cuda, "is_available", return_value=False), patch.object(torch.backends.mps, "is_available", return_value=False):
            device, dtype = resolve_device_dtype(torch, Settings())
            self.assertEqual(str(device), "cpu")
            self.assertEqual(dtype, torch.float32)
            for requested in ("cuda", "mps"):
                with self.assertRaises(ValueError):
                    resolve_device_dtype(torch, Settings(device=requested))

    def test_cuda_auto_dtype_and_explicit_bf16_guard(self):
        with patch.object(torch.cuda, "is_available", return_value=True), patch.object(torch.cuda, "device", return_value=nullcontext()):
            for supported, expected in [(True, torch.bfloat16), (False, torch.float16)]:
                with patch.object(torch.cuda, "is_bf16_supported", return_value=supported):
                    device, dtype = resolve_device_dtype(torch, Settings())
                    self.assertEqual(str(device), "cuda")
                    self.assertEqual(dtype, expected)
                    if not supported:
                        with self.assertRaises(ValueError):
                            resolve_device_dtype(torch, Settings(device="cuda", dtype="bfloat16"))

    def test_body_limit_counts_chunked_bodies(self):
        async def run(chunks):
            received, sent = [], []
            events = iter({"type": "http.request", "body": chunk, "more_body": i < len(chunks) - 1}
                          for i, chunk in enumerate(chunks))
            async def receive():
                return next(events)
            async def send(event):
                sent.append(event)
            async def downstream(scope, receive, send):
                received.append((await receive())["body"])
            await BodyLimit(downstream, limit=4)({"type": "http", "method": "POST"}, receive, send)
            return received, sent
        received, _ = asyncio.run(run([b"12", b"34"]))
        self.assertEqual(received, [b"1234"])
        received, sent = asyncio.run(run([b"12", b"345"]))
        self.assertEqual(received, [])
        self.assertEqual(sent[0]["status"], 413)


if __name__ == "__main__":
    unittest.main()
