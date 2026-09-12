# PaddleOCR-VL 自托管解析服务

本服务在 Linux x64 容器中执行 PaddleOCR-VL-1.6 完整文档解析管线。PP-DocLayoutV3 负责版式分析与阅读顺序，视觉语言模型（Vision-Language Model，VLM）PaddleOCR-VL-1.6-0.9B 负责区域识别。接口使用超文本传输协议（Hypertext Transfer Protocol，HTTP）发送同步请求，消息采用 JavaScript 对象表示法（JavaScript Object Notation，JSON），返回逐页 Markdown。

## 环境与模型

| 项目 | 固定值 |
| --- | --- |
| Python 基础镜像 | `python:3.11-slim-bookworm@sha256:528257d48c1da0dcecc2e725d1ae34498d60c965f1241e39cd6a85a8859bdf84` |
| PaddlePaddle | 中央处理器（Central Processing Unit，CPU）包 `3.3.1` |
| PaddleOCR | `3.7.0`，`doc-parser` 扩展 |
| PaddleX | `3.7.2`，`serving` 扩展 |
| NumPy | `1.26.4` |
| 版式模型 | `PaddlePaddle/PP-DocLayoutV3`，revision `7b48a7566925fa464281f930c58eee04fe2c862a` |
| 识别模型 | `PaddlePaddle/PaddleOCR-VL-1.6`，revision `c5630abae1d940eafe0697512a0325494b02ab42` |
| 管线配置来源 | PaddleX `v3.7.2`，commit `ffb64904d23708863ff5b8da312a5cbd52a7f462` 的 `PaddleOCR-VL-1.6.yaml` |
| 批大小 | 每层 `1` |
| 容器资源上限 | 6 个 CPU，9 GiB 内存，1 GiB 共享内存 |
| 缓存目录 | 本目录 `.runtime/`，绑定到容器 `/runtime` |

完整 Python 依赖版本保存在 `requirements.lock`，镜像构建会安装并检查该锁定清单。基础镜像固定到内容摘要，模型固定到仓库 revision 和文件摘要。

`models.lock.json` 保存固定 Hugging Face revision 的官方文件清单与内容摘要。`prepare.py` 依据该清单从同一发布者的 ModelScope 镜像分段下载大模型文件，并逐一核验 SHA-256 摘要；普通文件核验 Git blob 摘要。成功后输出 `.runtime/model-manifest.json`。缓存完整时，校验过程可以离线执行。下载的模型、断点文件和运行证据由本目录的 Git 忽略规则排除。

## 启动

在仓库根目录执行：

```powershell
./scripts/parser-benchmark-paddle.ps1 build
./scripts/parser-benchmark-paddle.ps1 prepare
./scripts/parser-benchmark-paddle.ps1 start
./scripts/parser-benchmark-paddle.ps1 status
```

停止此服务执行 `./scripts/parser-benchmark-paddle.ps1 stop`。停止操作保留模型与结果。受主机内存约束，测评时需要由调度器串行运行 MinerU 和 PaddleOCR-VL 的重推理。

| 调用位置 | 服务地址 |
| --- | --- |
| Windows 主机 | `http://127.0.0.1:18082` |
| `weknora-parser-benchmark` Docker 网络 | `http://weknora-parser-paddle:8080` |

端口仅绑定主机回环地址。容器服务需要完整的版式分析入口 `/layout-parsing`。WeKnora 的自建端点字段填写上表中的基础地址。

## 请求与响应

请求方法为 `POST /layout-parsing`，内容类型为 `application/json`。`file` 是文件字节的 Base64 编码，`fileType=0` 表示 PDF，`fileType=1` 表示图像。

```json
{
  "file": "BASE64_DOCUMENT_BYTES",
  "fileType": 0,
  "useDocOrientationClassify": false,
  "useDocUnwarping": false,
  "useLayoutDetection": true,
  "useChartRecognition": false,
  "useSealRecognition": true,
  "visualize": false
}
```

成功响应包含 `errorCode=0` 和 `result.layoutParsingResults[]`。每一页的 `markdown.text` 保存识别文本，`markdown.images` 保存图像路径到图像数据的映射。WeKnora 的适配器使用这些字段合并页面和处理图片，同时将可转换的 HTML 表格归一为 Markdown 表格。

`smoke.py` 使用与 WeKnora 适配器一致的请求字段执行单文档检查，保存原始响应、合并文本和调用摘要。例如：

```powershell
python deploy/parser-benchmark/paddle/smoke.py path/to/public-document.pdf --output deploy/parser-benchmark/paddle/.runtime/smoke
```

PaddleX 原生推理后端忽略请求中的 `repetitionPenalty`、`temperature` 和 `topP` 字段。测评记录需要注明该后端行为，云端与自托管的相同请求字段不能证明二者使用完全相同的采样参数。

性能和质量结论以实际测评结果为依据。仅完成容器启动、依赖检查或端口可达性检查，均不等同于文档解析成功。

## 实际验证记录

2026 年 9 月 11 日，服务在 6 个 CPU、9 GiB 内存限额下完成 OmniDocBench 样本 `omni-8739ae8d4f502788` 的解析。该输入是固定清单内的单页中英混合教材 PDF。下表记录完整管线的实际结果。

| 指标 | 实测值 |
| --- | --- |
| 输入 SHA-256 | `edde64ca28971957a9a0aa992eab8b28b39dafa7b02fd22c68c6cfa0ba010ac2` |
| 容器启动至 HTTP 监听日志 | 71.331 秒 |
| HTTP 状态 / 业务错误码 | `200` / `0` |
| 返回页数 / 文本块数 | 1 / 15 |
| 合并 Markdown 字符数 | 647 |
| HTTP 调用耗时 | 478.938 秒 |
| 推理块开始日志间隔 | 14 个间隔，中位数 30.828 秒，范围 24.859–41.384 秒 |
| 返回文本块字符数 | 中位数 39，范围 23–72，总计 619 |
| 冷启动及容器累计内存峰值 | 9,198,104,576 字节，约 8.57 GiB |
| 调用结束容器内存 | 6,614,499,328 字节，约 6.16 GiB |
| 内存耗尽终止 | 无 |

推理块间隔按日志时间顺序统计，文本长度按返回顺序统计，两者没有建立逐块对应关系。上述峰值来自 Linux 控制组的 `memory.peak`，包含初始化阶段；它不是独立测得的推理阶段峰值。调用时间包含本机 HTTP 传输、PDF 渲染、版式分析、识别和结果处理。该次试跑与 MinerU 正式解析同时运行。

将该样本的耗时机械外推到 100 页约为 13.3 小时，该数值只表示当前样本的时间量级。长页和复杂版式需要单独观测，600 秒不能保证覆盖所有页面。客户端超时后，需要等待同步推理排空或受控重启服务，再提交下一页。

证据位于 `.runtime/smoke-8739/`、`.runtime/cold-start.json`、`.runtime/after-smoke.json`、`.runtime/block-timing.json`、`.runtime/output-block-statistics.json` 和 `.runtime/service.log`。这些运行产物由 Git 忽略规则排除。

## 原生 CPU 参数边界

当前 PaddleX 原生预测器将 PaddleOCR-VL 的本地批大小固定为 `1`。`use_hpip=True` 在该模型分支中触发不支持提示，因此 HPI（高性能推理，High-Performance Inference）和 OpenVINO 不能作为当前视觉语言模型的直接加速开关。生成包装器默认启用键值缓存（Key-Value Cache，KV Cache）。服务的线程配置环境变量 `OMP_NUM_THREADS` 和 `MKL_NUM_THREADS` 均为 `6`。

现有验证没有发现可直接保证显著提速的原生参数组合。调整线程数需要在同一输入上另做对照；更换执行后端需要独立验证接口、内存和输出一致性。测评保持输入、模型权重、图像分辨率上限和生成长度约束一致。

- [固定版本原生预测器的批大小与 HPI 行为](https://github.com/PaddlePaddle/PaddleX/blob/ffb64904d23708863ff5b8da312a5cbd52a7f462/paddlex/inference/models/doc_vlm/predictor.py)
- [固定版本生成包装器的 KV Cache 默认值](https://github.com/PaddlePaddle/PaddleX/blob/ffb64904d23708863ff5b8da312a5cbd52a7f462/paddlex/inference/models/doc_vlm/modeling/paddleocr_vl/_paddleocr_vl.py)

## 官方依据

- [PaddleOCR-VL 使用与 CPU 服务部署说明](https://www.paddleocr.ai/latest/en/version3.x/pipeline_usage/PaddleOCR-VL.html)
- [固定版本完整管线配置](https://github.com/PaddlePaddle/PaddleX/blob/ffb64904d23708863ff5b8da312a5cbd52a7f462/paddlex/configs/pipelines/PaddleOCR-VL-1.6.yaml)
- [版式模型](https://huggingface.co/PaddlePaddle/PP-DocLayoutV3/tree/7b48a7566925fa464281f930c58eee04fe2c862a)
- [识别模型](https://huggingface.co/PaddlePaddle/PaddleOCR-VL-1.6/tree/c5630abae1d940eafe0697512a0325494b02ab42)
