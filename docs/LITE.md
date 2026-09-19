# WeKnora Lite 与标准版区别

Lite 面向希望快速在本地使用、部署尽量简单的场景；标准版面向多空间协作与完整企业能力。主要差异如下。


| 维度        | Lite                                            | 标准版                               |
| --------- | ----------------------------------------------- | --------------------------------- |
| **共享空间**  | 不提供共享空间（成员邀请、跨成员共享知识库与智能体等）                     | 提供共享空间及空间隔离检索等协作能力                |
| **空间与账号** | 单空间；开箱即用，**无需注册**                               | 多空间；通常需要注册、登录与组织管理                |
| **文档解析**  | 内置仅 **Simple** 类型解析引擎；可通过 **Cloud** 等方式接入其他解析能力 | 可配置多种解析引擎（含高精度等），与完整文档处理链路集成      |
| **部署形态**  | **单应用、零依赖**（不依赖独立的数据库、消息队列等外部服务栈）               | 典型为 Docker Compose 等多服务部署，依赖与组件更多 |
| **数据归属**  | 数据**完全在本地**存储与处理                                | 私有化部署时数据也可在本地；具体取决于你的部署方式         |
| **网络暴露**  | **默认仅本机访问**；可按需配置，**选择是否放行到公网**                 | 按部署与安全策略自行绑定地址与网关                 |


若你不需要多团队协作、复杂解析流水线与多服务架构，Lite 更适合个人或小团队在本机零依赖试用；需要共享空间、多空间与完整解析引擎矩阵时，请使用标准版。

## 启动配置

将 `.env.lite.example` 复制为 `.env.lite` 后，可以直接启动 Lite 二进制。程序依次尝试加载 `.env`、`.env.lite`；如需使用其他文件，可通过 `ENV_FILE` 指定。文件中的配置不会覆盖 shell 中已经设置的环境变量。

```bash
cp .env.lite.example .env.lite
./WeKnora-lite
```

默认模板已将 `localhost`、`127.0.0.1` 与 `::1` 加入 `SSRF_WHITELIST_EXTRA`，本机 Ollama 可以直接使用。访问其他内网服务时，请将实际主机或网段追加到该配置中。

## Windows 原生构建

Windows 版包含 DuckDB 与 sqlite-vec，因此需要兼容的 CGO 工具链和 SQLite 头文件。当前验证组合为 Windows x64、[GCC 14.2 POSIX/SEH/UCRT](https://github.com/niXman/mingw-builds-binaries/releases/download/14.2.0-rt_v12-rev0/x86_64-14.2.0-release-posix-seh-ucrt-rt_v12-rev0.7z)，以及 MSYS2 UCRT64 的 SQLite 开发包。不要使用 MSVCRT 工具链，也不要使用 GCC 16 链接当前 DuckDB 预编译静态库。

在 MSYS2 UCRT64 终端安装 SQLite 头文件：

```bash
pacman -S --needed mingw-w64-ucrt-x86_64-sqlite3
```

在 PowerShell 中指定工具链并构建：

```powershell
$env:WEKNORA_GCC_BIN = 'C:\Tools\duckdb-gcc-14.2\mingw64\bin'
$env:WEKNORA_SQLITE_INCLUDE = 'C:\msys64\ucrt64\include'
.\scripts\build-lite-windows.ps1
```

脚本会在编译前检查目标架构、UCRT、GCC 版本、`g++.exe` 与 `sqlite3.h`，构建前端到 `web/`，然后用默认的 DuckDB 静态绑定生成 `WeKnora-lite.exe`。已有 `web/index.html` 时可传入 `-SkipFrontend`。Windows 不支持 `duckdb_use_lib` 动态绑定：当前依赖版本会在不同 CRT 堆之间释放 `C.CString` 内存，可能触发访问违例，因此构建会直接报错并提示使用静态绑定。工具链背景可参考 [DuckDB Go 的 Windows 构建说明](https://github.com/duckdb/duckdb-go#Windows)。
