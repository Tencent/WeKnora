#!/usr/bin/env python3
"""Check a running WeMM service with actual text and image inference."""
import argparse
import base64
import io
import json
import math
import os
import time

import httpx
from PIL import Image, ImageDraw


def dot(a, b):
    return sum(x * y for x, y in zip(a, b))


def text_message(text):
    return [{"role": "user", "content": [{"type": "text", "text": text}]}]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:18080", help="Service root URL, without /v1")
    parser.add_argument("--timeout", type=float, default=180)
    args = parser.parse_args()
    headers = {"Authorization": "Bearer " + os.environ["WEMM_API_KEY"]} if os.environ.get("WEMM_API_KEY") else {}
    results = []
    with httpx.Client(base_url=args.base_url.rstrip("/"), timeout=args.timeout, headers=headers) as client:
        health_response = client.get("/health")
        health_response.raise_for_status()
        health = health_response.json()
        model = health["model"]
        default_dimension = health["default_dimension"]
        response = client.get("/v1/models")
        response.raise_for_status()
        assert model in [row["id"] for row in response.json()["data"]]

        def run(name, **payload):
            started = time.monotonic()
            response = client.post("/v1/embeddings", json={"model": model, **payload})
            response.raise_for_status()
            data = response.json()
            vectors = [row["embedding"] for row in data["data"]]
            assert [row["index"] for row in data["data"]] == list(range(len(vectors)))
            for vector in vectors:
                assert len(vector) == payload.get("dimensions", default_dimension)
                assert all(math.isfinite(x) for x in vector)
                assert abs(math.sqrt(dot(vector, vector)) - 1) < 1e-4
            results.append({"test": name, "seconds": round(time.monotonic() - started, 3),
                            "vectors": len(vectors), "dimension": len(vectors[0])})
            print(json.dumps(results[-1]), flush=True)
            return vectors

        texts = ["A red square on a white background.", "A blue circle on a black background."]
        text_vectors = run("batched text messages", messages=[text_message(text) for text in texts])
        images = []
        for color, shape, background in [("red", "rectangle", "white"), ("blue", "ellipse", "black")]:
            image = Image.new("RGB", (256, 256), background)
            getattr(ImageDraw.Draw(image), shape)((40, 40, 216, 216), fill=color)
            buffer = io.BytesIO()
            image.save(buffer, format="PNG")
            images.append([{"role": "user", "content": [{"type": "image_url", "image_url": {
                "url": "data:image/png;base64," + base64.b64encode(buffer.getvalue()).decode(),
            }}]}])
        image_vectors = run("batched image messages", messages=images)
        scores = [[dot(text, image) for image in image_vectors] for text in text_vectors]
        assert scores[0][0] > scores[0][1] and scores[1][1] > scores[1][0], scores
        repeated = run("text after image state isolation", messages=text_message(texts[0]))[0]
        assert dot(repeated, text_vectors[0]) > 0.9999
        dimension = min(health["dimensions"])
        short = run("Matryoshka dimensions", messages=text_message(texts[0]), dimensions=dimension)[0]
        prefix = text_vectors[0][:dimension]
        norm = math.sqrt(dot(prefix, prefix))
        assert dot(short, [value / norm for value in prefix]) > 0.9999
        raw = run("raw text compatibility", input=texts[0])[0]
        assert dot(raw, text_vectors[0]) > 0.9999
        for payload in ({"input": "hello", "dimensions": 3}, {"input": "a", "messages": []},
                        {"messages": [{"role": "user", "content": [{"type": "image_url", "image_url": {
                            "url": "https://example.com/image.png",
                        }}]}]}):
            response = client.post("/v1/embeddings", json={"model": model, **payload})
            assert response.status_code == 400, response.text
        print(json.dumps({"status": "passed", "health": health, "tests": results, "text_image_scores": scores}), flush=True)


if __name__ == "__main__":
    main()
