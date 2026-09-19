# Windows 原生构建注意事项

本文面向在 Windows 上不使用 Docker、直接用 Go 工具链构建 WeKnora 的开发者
（含 Lite 版 `make build-lite`，或等价的
`go build -tags sqlite_fts5 -o WeKnora-lite.exe ./cmd/server`），汇总三个常见
的构建/运行问题及规避方式。Linux 与 macOS 不受影响。

## 前置要求

- Go 1.26+（与 go.mod 保持一致）；
- 一套 GCC/Clang 工具链（构建含 CGO，`CGO_ENABLED=1`）：
  推荐 **LLVM-MinGW（UCRT 运行时）**，例如
  [llvm-mingw](https://github.com/mstorsjo/llvm-mingw/releases) 发行版或
  MSYS2 的 **UCRT64** 环境。w64devkit 等 MSVCRT 运行时的工具链会导致下述
  DuckDB 静态链接失败；
- 前端产物：二进制启动时需要 `web/` 目录（可先
  `cd frontend && npm ci && npm run build`，打包流程见
  `scripts/package-lite.sh`）。

## DuckDB 预编译静态库要求 UCRT 工具链

Windows/amd64 下默认静态链接 DuckDB（预编译静态库取自 DuckDB 官方 release，
使用 LLVM-MinGW/UCRT 构建）。用 MSVCRT 运行时的工具链（如 w64devkit）链接
会报缺失符号：

- `__stdio_common_vsnprintf_s` / `__stdio_common_vswprintf`（UCRT）；
- `__emutls_v._ZSt11__once_call` / `__emutls_v._ZSt15__once_callable`（emutls）；
- `std::basic_streambuf` 等 C++ 符号。

解决方式：改用 LLVM-MinGW（UCRT）工具链，或改走下节的动态链接方式。

## `duckdb_use_lib` 动态链接与 duckdb_free 跨堆释放

使用 `-tags duckdb_use_lib` 动态链接 `duckdb.dll` 时，
`github.com/duckdb/duckdb-go-bindings v0.10502.0` 的 `bindings.go` 把 cgo
分配的内存（`C.CString`，走 Go 侧 CRT 的 `malloc`）统一交给 `Free()`（即
`duckdb_free`，走 DLL 内部 CRT 的 `free`）释放。两侧 CRT 堆不共享时会触发
访问违例（`Exception 0xc0000005`），典型表现为调用 `SetConfig` / `Open` 等
接口后进程崩溃。

正确的释放路径应当区分两类内存：cgo 分配的字符串用 `C.free`；DuckDB 返回、
由 DuckDB 自行分配的错误消息（如 `duckdb_get_or_create_from_cache` /
`duckdb_open_ext` 的出参）才使用 `duckdb_free`。上游修复前可任选：

- 使用 LLVM-MinGW（UCRT）工具链 + 默认静态构建（推荐，无需改动依赖）；或
- 自行给 `duckdb-go-bindings` 打补丁：把 `bindings.go` 中与 `C.CString`
  对应的 `defer Free(...)` 改为 `defer C.free(...)`（保留 DuckDB 错误串的
  少量 `Free` 不变），并在 go.mod 中用 `replace` 指向补丁版本。

## `sqlite_fts5` 构建需要 sqlite3.h

`-tags sqlite_fts5`（Lite 构建默认启用）会编译 cgo 包
`github.com/asg017/sqlite-vec-go-bindings`，其 `sqlite-vec.h` 引用了
`sqlite3.h`，但该依赖包不自带此头文件。Linux/macOS 通常由系统提供
（libsqlite3-dev / Xcode SDK），Windows 的 MinGW 工具链一般没有，会报
找不到 `sqlite3.h`。构建前从
[SQLite 下载页](https://www.sqlite.org/download.html) 取 amalgamation 中的
`sqlite3.h`，并通过 `CGO_CFLAGS` 指定所在目录：

```powershell
$env:CGO_ENABLED = "1"
$env:CGO_CFLAGS  = "-IC:\path\to\sqlite-amalgamation"
go build -tags sqlite_fts5 -o WeKnora-lite.exe ./cmd/server
```

## 运行时配置加载

`cmd/server` 构建出的二进制（含 WeKnora-lite.exe）启动时会依次尝试加载
工作目录下的 `.env.lite` 与 `.env`（也可用 `ENV_FILE` 环境变量指定具体文件），
无需再逐个 `$env:` 设置环境变量；进程环境中已存在的变量始终优先。
