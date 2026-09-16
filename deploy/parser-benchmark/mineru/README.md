# MinerU CPU 解析服务

该服务在独立 Linux 容器内运行 MinerU 的 `pipeline` 后端，通过 `/file_parse` 接收 PDF，返回 Markdown、图像和内容列表。程序依赖保存在容器文件系统，模型、下载缓存和解析输出保存在 D 盘目录。

| 配置项 | 配置值 |
| --- | --- |
| MinerU | 3.4.5 |
| Python | 3.10.18 |
| PyTorch | 2.8.0+cpu，CUDA 为 `None` |
| torchvision | 0.23.0+cpu |
| Transformers | 4.57.3 |
| 后端 | pipeline |
| 容器 | weknora-parser-mineru |
| 宿主机端点 | http://127.0.0.1:18081 |
| 容器网络端点 | http://weknora-parser-mineru:8000 |
| Docker 网络 | weknora-parser-benchmark |
| 资源限制 | 6 个 CPU，8 GiB 内存，内存与交换空间合计 10 GiB |
| 并发与页窗口 | 1 个请求，1 页处理窗口 |
| 分批参数 | 虚拟显存参数为 2，用于控制分批；计算设备固定 cpu |
| 公式与表格 | 请求中启用 |
| 语言 | ch；en 在上游接口中映射为 ch，覆盖本次中英文样本 |
| 数据目录 | `artifacts/parser-benchmark/mineru-state` |

`requirements.lock.txt` 固定实际安装的全部 Python 依赖。基础镜像通过摘要固定，复用项目文档解析镜像内的 Python 和系统库。模型来源为 `OpenDataLab/PDF-Extract-Kit-1.0`，提交版本为 `05eaf85cc4ddab92c2be61e10abec4586d25c1a6`。下载器获取当前中英文配置需要的 15 个文件，共 1,082,446,509 字节，包含 PP-DocLayoutV2、Unimernet、PP-OCRv6 small 检测与识别模型，以及三个表格模型。下载完成后生成 `models-manifest.json`，记录模型文件大小和 SHA-256（安全散列算法 256 位，Secure Hash Algorithm 256-bit）摘要。

超过 16 MiB 的模型文件采用六路分段下载，每段 16 MiB。下载器检查状态码、返回区间和文件长度，并按官方固定提交提供的摘要校验模型。已完成分段保存在数据目录中，可以在中断后复用。当前服务固定默认公式模型 Unimernet，其余语系与替代公式模型需要单独准备和验证。

## 启动与验证

在仓库根目录使用 PowerShell 7 执行：

```powershell
pwsh -NoProfile -File scripts/parser-benchmark-mineru.ps1 -Action Install
pwsh -NoProfile -File scripts/parser-benchmark-mineru.ps1 -Action DownloadModels
pwsh -NoProfile -File scripts/parser-benchmark-mineru.ps1 -Action Start
pwsh -NoProfile -File scripts/parser-benchmark-mineru.ps1 -Action Status
```

`Install` 创建独立容器并安装依赖。`DownloadModels` 下载并校验模型。`Start` 启动接口并等待 `/health` 返回版本 3.4.5 和 `healthy` 状态，最长等待 45 秒。已有健康接口时会保持当前进程。`Stop` 仅停止 `weknora-parser-mineru`，数据目录仍保留。

Docker 或 Windows 重启后，先启动 Docker Desktop，再执行 `Start`。容器的主进程为保持运行的 shell，接口进程由 `Start` 单独启动；Docker 的容器运行状态与接口健康状态分别检查。当前部署通过上述命令恢复服务。服务器日志追加写入数据目录的 `server.log`。

三个基础解析引擎的文档读取服务与人工核验页面由 [`scripts/parser-benchmark-service.ps1`](../../../scripts/parser-benchmark-service.ps1) 启动。该脚本等待文档读取服务的 gRPC（远程过程调用框架，gRPC Remote Procedure Calls）健康探针及报告 HTTP 接口就绪后返回，超时以非零状态退出。它与本页的 MinerU 启动脚本分别管理对应服务。

服务设置 `MINERU_API_TASK_RETENTION_SECONDS=0`，保留本次测评生成的解析输出。`/health` 返回服务版本、协议版本、任务状态计数及并发配置。

运行一页冒烟测试：

```powershell
pwsh -NoProfile -File scripts/parser-benchmark-mineru-smoke.ps1 `
  -PdfPath artifacts/parser-benchmark/data/pdfs/omni-e18bd3f8e3304ce3.pdf
```

脚本发送与项目 `MinerUReader` 一致的表单字段，并要求响应包含 `results.<文件名>.md_content`。原始响应、Markdown 和测量记录写入数据目录的 `smoke` 子目录。记录包含输入摘要、后端版本、耗时、图像数量和容器控制组的内存峰值。内存峰值覆盖该次容器启动以来的全部进程活动。

`validation.json` 记录 2026-09-11 的真实单页验证：中英教材页 `omni-8739ae8d4f502788.pdf` 返回状态码 200，生成 717 个字符的 Markdown、1 张图像和内容列表。冷模型请求耗时 22.043 秒，容器内存峰值 1,969,602,560 字节，约 1.83 GiB。该验证确认接口契约和非空输出；正式质量评分使用统一测评清单及参考标注。

`/docs` 返回成功表示 HTTP（超文本传输协议，Hypertext Transfer Protocol）接口已启动；真实文档解析成功由冒烟测试结果确认。完整测评应在模型下载结束后运行，以保持推理计时的含义一致。服务从本地模型目录读取权重。

## 接入 WeKnora

解析引擎配置使用 `http://weknora-parser-mineru:8000`，Backend 选择 `pipeline`，解析方式选择 `auto`。应用侧 SSRF（服务器端请求伪造，Server-Side Request Forgery）出站地址校验需要允许这个明确的容器域名。测试运行器和应用配置分别管理该白名单。

容器服务通过宿主机回环地址发布，网络内的应用通过 Docker 服务名访问。页面输出使用 MinerU 原生 Markdown；响应中的图像采用数据 URI（统一资源标识符，Uniform Resource Identifier）携带 Base64 内容。

## 来源

- [MinerU CPU 后端安装说明](https://opendatalab.github.io/MinerU/quick_start/)
- [MinerU 官方源代码](https://github.com/opendatalab/MinerU)
- [MinerU 3.4.5 软件包](https://pypi.org/project/mineru/3.4.5/)
- [模型仓库](https://modelscope.cn/models/OpenDataLab/PDF-Extract-Kit-1.0)

CPU 测量对应上述固定资源配置。不同硬件、不同后端和云服务返回版本的结果需要分别说明。
