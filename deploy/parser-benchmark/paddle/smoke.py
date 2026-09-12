"""Exercise the complete PaddleOCR-VL HTTP contract with a local public document."""
import argparse
import base64
import hashlib
import json
import time
from datetime import datetime, timezone
from pathlib import Path

import requests

parser = argparse.ArgumentParser()
parser.add_argument("document", type=Path)
parser.add_argument("--endpoint", default="http://127.0.0.1:18082")
parser.add_argument("--output", type=Path, required=True)
parser.add_argument("--timeout", type=int, default=1000)
args = parser.parse_args()
args.output.mkdir(parents=True, exist_ok=True)
document = args.document.read_bytes()
payload = {
    "file": base64.b64encode(document).decode(),
    "fileType": 0 if args.document.suffix.lower() == ".pdf" else 1,
    "markdownIgnoreLabels": ["header", "header_image", "footer", "footer_image", "number", "footnote", "aside_text"],
    "useDocOrientationClassify": False,
    "useDocUnwarping": False,
    "useLayoutDetection": True,
    "useChartRecognition": False,
    "useSealRecognition": True,
    "useOcrForImageBlock": False,
    "mergeTables": True,
    "relevelTitles": True,
    "restructurePages": True,
    "layoutShapeMode": "auto",
    "promptLabel": "ocr",
    "layoutNms": True,
    "repetitionPenalty": 1,
    "temperature": 0,
    "topP": 1,
    "minPixels": 147384,
    "maxPixels": 2822400,
    "visualize": False,
}
started = time.monotonic()
summary = {
    "started_at": datetime.now(timezone.utc).isoformat(),
    "document": str(args.document),
    "sha256": hashlib.sha256(document).hexdigest(),
    "endpoint": args.endpoint,
    "request_options": {key: value for key, value in payload.items() if key != "file"},
    "success": False,
}
try:
    session = requests.Session()
    session.trust_env = False
    response = session.post(args.endpoint.rstrip("/") + "/layout-parsing", json=payload, timeout=(20, args.timeout))
    summary["http_status"] = response.status_code
    (args.output / "response.json").write_bytes(response.content)
    response.raise_for_status()
    result = response.json()
    pages = result.get("result", {}).get("layoutParsingResults", [])
    text = "\n\n".join(page.get("markdown", {}).get("text", "") for page in pages)
    summary.update(error_code=result.get("errorCode"), pages=len(pages), markdown_characters=len(text))
    summary["success"] = result.get("errorCode") == 0 and bool(pages) and bool(text.strip())
    (args.output / "output.md").write_text(text, encoding="utf-8")
except Exception as error:
    summary["error"] = f"{type(error).__name__}: {error}"
finally:
    summary["elapsed_seconds"] = round(time.monotonic() - started, 3)
    (args.output / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(summary, ensure_ascii=False, indent=2))
raise SystemExit(0 if summary["success"] else 1)
