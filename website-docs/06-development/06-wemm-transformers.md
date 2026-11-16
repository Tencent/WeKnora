# WeMM Transformers 部署

使用 [WeMM 官方 Transformers 推理代码](https://github.com/Tencent/WeMM-Embedding#transformers)加载模型，由仓库内的 `scripts/wemm-transformers/serve.py` 提供 HTTP Embedding 服务。服务可以与 WeKnora 部署在同一机器，也可以部署在独立 GPU 服务器上，通过 Base URL 接入。

CUDA、Apple MPS 和 CPU 是可选的计算设备，使用同一份脚本和请求协议。此 HTTP 适配层由 WeKnora 提供，支持文本和图片；模型本身支持的视频等能力尚未在此接口开放。官方 vLLM 部署方式见[模型配置](../03-features/06-models.md#wemm-embedding)。

## 安装依赖

以下命令在 WeKnora 仓库根目录执行，使用 Python 3.12。虚拟环境和模型文件放在仓库外：

```bash
python3.12 -m venv "$HOME/.venvs/weknora-wemm"
source "$HOME/.venvs/weknora-wemm/bin/activate"
python -m pip install --upgrade pip
```

按设备选择一组 PyTorch 安装命令。以下使用已验证的 PyTorch 2.10.0 / torchvision 0.25.0，CUDA wheel 须与驱动兼容；其他 CUDA 版本可参考 [PyTorch 安装说明](https://pytorch.org/get-started/previous-versions/#v2100)。

```bash
# NVIDIA GPU：示例为 CUDA 12.8 wheel
python -m pip install torch==2.10.0 torchvision==0.25.0 \
  --index-url https://download.pytorch.org/whl/cu128

# 或 Apple Silicon / MPS
python -m pip install torch==2.10.0 torchvision==0.25.0

# 或 Linux / Windows CPU wheel
python -m pip install torch==2.10.0 torchvision==0.25.0 \
  --index-url https://download.pytorch.org/whl/cpu
```

安装其余依赖：

```bash
python -m pip install -r scripts/wemm-transformers/requirements.txt
```

Transformers 固定为官方推荐的 `5.2.0`，`qwen-vl-utils` 固定为 `0.0.14`。不要随意替换 Transformers 版本，避免预处理行为变化。脚本会启用 `trust_remote_code=True` 来加载 WeMM 的 `embedding()` 实现，应使用官方权重或已审查的模型目录。

## 启动服务

以 CUDA 上的 2B 为例：

```bash
python scripts/wemm-transformers/serve.py \
  --model tencent/WeMM-Embedding-2B \
  --revision bbd6cd4bf52cfc6716f752a2df80b2706720bd95 \
  --device cuda \
  --host 127.0.0.1 \
  --port 18080
```

首次启动会从 Hugging Face 下载模型到标准缓存，后续复用缓存。`--revision` 可固定模型代码与权重版本；示例 SHA 属于 2B，切换 4B、9B 时应选取对应仓库的版本，不能沿用此 SHA。未指定时使用该仓库默认版本。

已有模型目录时可以离线启动，并指定 WeKnora 调用时使用的模型名称：

```bash
python scripts/wemm-transformers/serve.py \
  --model /srv/models/WeMM-Embedding-2B \
  --served-model-name tencent/WeMM-Embedding-2B \
  --local-files-only \
  --device cuda
```

也可以提前下载并固定版本，例如：

```bash
hf download tencent/WeMM-Embedding-2B \
  --revision bbd6cd4bf52cfc6716f752a2df80b2706720bd95 \
  --local-dir /srv/models/WeMM-Embedding-2B
```

其他设备只需更换参数：

```bash
# Apple Silicon，允许不支持的 MPS 算子回退到 CPU
python scripts/wemm-transformers/serve.py \
  --model tencent/WeMM-Embedding-2B --device mps --mps-fallback

# CPU，默认使用 float32
python scripts/wemm-transformers/serve.py \
  --model tencent/WeMM-Embedding-2B --device cpu
```

4B、9B 将 `--model` 换成对应 ID 或路径。服务从模型配置读取原生维度及 Matryoshka 可选维度，不会将其他规格强制按 2048 维处理：

| 模型 ID | 原生维度 |
| --- | --- |
| `tencent/WeMM-Embedding-2B` | 2048 |
| `tencent/WeMM-Embedding-4B` | 2560 |
| `tencent/WeMM-Embedding-9B` | 4096 |

服务以前台方式运行，按 Ctrl+C 停止。常驻部署时由 systemd、容器或其他进程管理器托管同一命令。一个进程加载一份完整模型到一个设备，不提供多 GPU 切分；选择规格前应预留权重、视觉处理和推理激活所需的显存/内存。

### 独立服务器与鉴权

跨机器部署时使用 `--host 0.0.0.0`，并在服务进程环境中设置 `WEMM_API_KEY`。`/v1/models` 与 `/v1/embeddings` 会验证 `Authorization: Bearer <key>`；WeKnora 的 API Key 填写相同值。服务不直接终止 TLS，跨不可信网络时通过 HTTPS 反向代理访问。`/health` 仅提供运行状态和非敏感模型元数据，不要求 API Key。

默认监听回环地址、未设置 API Key。WeKnora 若运行在 Docker 中，`127.0.0.1` 指向 WeKnora 容器自身，应填写容器能够访问的推理服务地址，例如同一容器网络的服务名，或已配置的宿主机地址。私网地址还需符合 WeKnora 的 SSRF 访问策略，配置方法见[安全说明](../01-getting-started/04-configuration.md)。

## WeKnora 接入

在「设置 → 模型管理」添加：

| 字段 | 填写 |
| --- | --- |
| 类型 | Embedding |
| 来源 / 服务商 | API / 自定义（OpenAI 兼容接口） |
| 模型名称 | `/v1/models` 返回的 ID，推荐保留 `tencent/WeMM-Embedding-2B` 等官方名称 |
| Base URL | `http://推理服务地址:18080/v1`，使用 HTTPS 代理时填写对应地址 |
| API Key | 设置了 `WEMM_API_KEY` 时填写相同值 |
| 图片输入 | 官方名称由预设识别；自定义别名需手动开启 |
| 自定义输出维度 | 初次接入关闭，让连接检测识别原生维度；需要降维时再开启 |
| 后台并发上限 | 建议从 1 开始 |

现有 WeMM 预设的 `messages` 格式同时适用于此服务。推荐在「高级 → 协议兼容覆盖」设置较小批量和较长超时：

```json
{
  "api": "openai-embeddings",
  "image_format": "messages",
  "text_as_messages": true,
  "batch_messages": true,
  "accepts_truncate_prompt_tokens": false,
  "max_batch_size": 4,
  "max_image_batch_size": 2,
  "request_timeout_seconds": 180
}
```

完整配置也适用于尚未包含 WeMM 预设的旧版本或自定义模型别名；此时应手动打开图片输入。点击「测试连接」，确认实际维度后保存。知识库还需开启图片向量索引，配置方式见[模型配置](../03-features/06-models.md)。切换模型或输出维度需要重建已有索引。

## 请求格式与推理流程

接口为 `POST /v1/embeddings`。纯文本可以使用 `input`：

```json
{
  "model": "tencent/WeMM-Embedding-2B",
  "input": ["第一段文本", "第二段文本"],
  "encoding_format": "float"
}
```

文本和图片也可使用 `messages`。以下二维数组包含两个独立输入，分别返回一个文本向量和一个图片向量：

```json
{
  "model": "tencent/WeMM-Embedding-2B",
  "dimensions": 1024,
  "messages": [
    [{"role": "user", "content": [{"type": "text", "text": "白色背景上的红色方块"}]}],
    [{"role": "user", "content": [{"type": "image_url", "image_url": {"url": "data:image/png;base64,图片Base64"}}]}]
  ]
}
```

`input` 和 `messages` 只能选一个。单条 conversation 可以使用一维消息数组；文字与图片放在同一 conversation 中会得到一个联合向量。返回值采用 OpenAI Embedding 的 `data[].embedding`、`data[].index` 和 `usage` 结构，编码支持 `float` 与 float32 小端字节的 `base64`。

服务会将文本包装成消息，把内联图片解码成 PIL 图片，使用官方 processor 的聊天模板，再转换为张量。Tokenizer 自动追加 `<embedding>`，调用方无需手动添加。推理调用 `model.embedding(..., use_cache=False)`，随后按请求维度截取并做 L2 归一化。这里使用 Transformers 自带的模型 tokenizer/processor；vLLM 的 `embedding_chat_template.jinja` 不作为本脚本的额外模板传入。

HTTP 支持批量输入，但模型逐条执行，并串行处理推理请求，以隔离模型内部的可变位置状态。`batch_messages: true` 表示合并 HTTP 请求，不代表 GPU 张量批处理，也不提供 vLLM 的连续批处理调度。

## 参数与限制

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--device` | `auto` | 自动按 CUDA → MPS → CPU 选择；可显式指定 `cuda:N`、`mps`、`cpu`，指定不可用设备会报错 |
| `--dtype` | `auto` | CUDA 支持 BF16 时用 BF16，否则 FP16；MPS 用 BF16；CPU 用 FP32。可显式指定 `bfloat16`、`float16`、`float32` |
| `--max-tokens` | 8192 | 每个输入经过模板和图片处理后的总 token 上限，超限拒绝，不静默截断 |
| `--max-pixels` | 1048576 | 每张图片缩放后的像素上限，至少 65536；较小上限会影响高分辨率文档效果 |
| `--max-batch-size` | 32 | 每个 HTTP 请求的独立输入数上限；WeKnora 的批量设置不应超过它 |
| `--cpu-threads` | 8 | PyTorch CPU 线程数 |
| `--mps-fallback` | 关闭 | 允许 PyTorch 将不支持的 MPS 算子放到 CPU 执行 |
| `--local-files-only` | 关闭 | 仅使用本机文件或已有缓存，不下载权重 |
| `--host` / `--port` | `127.0.0.1` / `18080` | 监听地址与端口 |

每个请求体最多 32 MiB，包含分块传输；每张图片的 data URI 最多 16 MiB，解码前原图最多 4000 万像素，每个 conversation 最多 5 张图片。图片只接受内联 Base64，不抓取远程 URL 或文件路径。`image_url.detail` 仅为格式兼容，实际分辨率由 `--max-pixels` 控制。所有上限都是此部署脚本的资源设置，不代表 WeMM 模型本身的上限。

## 验证与排障

运行无需下载权重的接口测试：

```bash
python -m unittest discover -s scripts/wemm-transformers -v
```

对运行中的服务做实际图文推理测试：

```bash
python scripts/wemm-transformers/smoke_test.py \
  --base-url http://127.0.0.1:18080
```

设置了鉴权时，测试进程同样读取 `WEMM_API_KEY`。测试覆盖批量图文、输出维度与归一化、图文匹配、图片后文本状态隔离及降维；输出耗时和结果到标准输出，不修改知识库。

当前在 Apple M4 Pro 上完成了 **WeMM 2B / MPS / BF16** 和 **WeMM 2B / CPU / FP32** 的完整图文推理测试。2B、4B、9B 的接口维度处理及 CUDA 精度选择有自动化覆盖；4B/9B 的完整权重推理和 CUDA 部署仍需在目标设备运行上述验证脚本。不要将接口测试视为这些设备和规格的性能验证。

常见问题：

- **CUDA/MPS 不可用**：检查 PyTorch wheel、驱动与设备，或显式改为 `--device cpu`；脚本不会将显式设备要求静默降级。
- **MPS 算子不支持**：尝试 `--mps-fallback`；BF16 不可用时可使用 `--dtype float32`，同时检查内存余量。
- **显存不足或超时**：减小模型规格、图片像素、输入长度和 WeKnora 批量。该服务为单 worker，增加 HTTP 并发不会自动增加推理吞吐。
- **模型加载缓慢**：首次需要下载权重；先预下载并固定版本，离线启动时检查目录中同时包含权重、tokenizer、processor 和官方 Python 模型代码。
- **模型名不匹配**：检查 `/v1/models`；使用自定义目录时建议显式设置 `--served-model-name`。
