#!/usr/bin/env python3
"""调用豆包 Seedream 5.0 Pro API 生成图片

用法:
  python generate_image.py --prompt "提示词" --size "2K" --output "output.jpg"

环境变量:
  ARK_API_KEY  火山方舟 API Key（必需）
"""

import sys
import os
import json
import argparse
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError

API_ENDPOINT = "https://ark.cn-beijing.volces.com/api/v3/images/generations"
MODEL_ID = "doubao-seedream-5-0-pro-260628"
TIMEOUT_SECONDS = 120


def generate_image(prompt, size="2K", output_path=None, watermark=False,
                   response_format="url"):
    """调用 Seedream API 生成图片，返回结果字典。"""

    api_key = os.environ.get("ARK_API_KEY")
    if not api_key:
        print("错误：未设置 ARK_API_KEY 环境变量", file=sys.stderr)
        sys.exit(1)

    payload = {
        "model": MODEL_ID,
        "prompt": prompt,
        "size": size,
        "response_format": response_format,
        "watermark": watermark,
        "sequential_image_generation": "disabled",
        "stream": False,
    }

    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {api_key}",
    }

    data = json.dumps(payload).encode("utf-8")
    req = Request(API_ENDPOINT, data=data, headers=headers, method="POST")

    try:
        with urlopen(req, timeout=TIMEOUT_SECONDS) as resp:
            result = json.loads(resp.read().decode("utf-8"))
    except HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")
        print(f"HTTP {e.code}: {body}", file=sys.stderr)
        sys.exit(1)
    except URLError as e:
        print(f"网络错误: {e.reason}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"未知错误: {e}", file=sys.stderr)
        sys.exit(1)

    if "error" in result:
        err = result["error"]
        print(f"API 错误 [{err.get('code', 'unknown')}]: "
              f"{err.get('message', '')}", file=sys.stderr)
        sys.exit(1)

    images = result.get("data", [])
    if not images:
        print("未生成图片", file=sys.stderr)
        sys.exit(1)

    image_info = images[0]

    # 下载或保存图片
    if output_path:
        if response_format == "b64_json":
            import base64
            img_data = base64.b64decode(image_info["b64_json"])
        else:
            img_url = image_info.get("url")
            if not img_url:
                print("响应中未包含图片 URL", file=sys.stderr)
                sys.exit(1)
            img_req = Request(img_url)
            with urlopen(img_req, timeout=60) as img_resp:
                img_data = img_resp.read()

        out = Path(output_path)
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_bytes(img_data)
        print(f"图片已保存至: {out}", file=sys.stderr)

    # 输出结构化结果
    usage = result.get("usage", {})
    summary = {
        "status": "success",
        "image_url": image_info.get("url"),
        "image_size": image_info.get("size"),
        "model": result.get("model"),
        "generated_images": usage.get("generated_images"),
        "output_tokens": usage.get("output_tokens"),
        "output_path": output_path,
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return summary


def main():
    parser = argparse.ArgumentParser(
        description="调用豆包 Seedream 5.0 Pro API 生成图片")
    parser.add_argument("--prompt", required=True, help="图片生成提示词")
    parser.add_argument("--size", default="2K",
                        help="图片尺寸：1K/2K/4K 或 WxH（如 2560x1440）")
    parser.add_argument("--output", default=None,
                        help="输出文件路径（如 illustration_ILL001.jpg）")
    parser.add_argument("--watermark", action="store_true",
                        help="添加 AI 生成水印（默认关闭）")
    parser.add_argument("--b64", action="store_true",
                        help="返回 base64 格式而非 URL")
    args = parser.parse_args()

    response_format = "b64_json" if args.b64 else "url"
    generate_image(
        prompt=args.prompt,
        size=args.size,
        output_path=args.output,
        watermark=args.watermark,
        response_format=response_format,
    )


if __name__ == "__main__":
    main()
