# Milvus 多语言 BM25 迁移

旧版 WeKnora 的 Milvus Collection 只有一个默认文本分析器，中文内容可能没有有效 BM25 关键词。`milvus-migrate` 会保留旧 Collection，并复制已有稠密向量到新的多语言 Collection；Milvus 会依据每行的 `language` 字段重新生成 BM25 稀疏向量。

稠密向量的 metric（IP / COSINE / L2）默认从源 Collection 的 embedding 索引读取。不要改成与源 Collection 不同的值，否则同一批向量会按错误距离排序。

## 使用方法

先确保 Milvus 已启动，再在项目根目录执行：

```bash
go run ./cmd/milvus-migrate --source weknora_embeddings --target weknora_embeddings_multilingual
```

如果当前 shell 已经导入了 `MILVUS_ADDRESS` 和 `MILVUS_COLLECTION`，可以省略对应参数。仅仅把变量写在 `.env.local` 中不会自动注入 `go run` 进程；不确定时建议显式传参：

```bash
go run ./cmd/milvus-migrate \
  --address 127.0.0.1:19530 \
  --source weknora_embeddings \
  --target weknora_embeddings_multilingual
```

若要核对 metric，可传入与源 Collection 相同的 `--metric-type`（以及环境变量 `MILVUS_METRIC_TYPE`）。传入的值必须与源索引一致，否则迁移会失败。

迁移完成后，把 WeKnora 的 `MILVUS_COLLECTION` 改为目标前缀并重启服务，**保持原来的 `MILVUS_METRIC_TYPE` 不变**：

```dotenv
MILVUS_COLLECTION=weknora_embeddings_multilingual
```

检索列表按 `{前缀}_{维度}` 精确匹配，因此 `weknora_embeddings` 不会误搜到 `weknora_embeddings_multilingual_*`。切换前缀后，新写入和向量/关键词检索才会都走新 Collection。

迁移程序不会删除旧 Collection。确认中文和英文 BM25 都能召回后，再通过 Milvus 管理工具删除旧 Collection；删除前请先备份。

迁移默认每批读取 64 行，并且只读取重建目标 Collection 所需的字段，不会读取旧 Collection 中生成的 BM25 稀疏向量。若单个文本块特别长，可以显式降低批大小，例如追加 `--batch-size 32`。

## Windows PowerShell 注意事项

WeKnora 的 `internal/utils` 使用 `pg_query_go` 解析 SQL，该依赖需要 CGO。直接执行 `go run` 时，如果当前会话的 `CGO_ENABLED=0`，会出现 `undefined: pg_query.Parse` 或 `undefined: pg_query.Deparse`。

项目附带了 MSYS2 GCC，可以在当前 PowerShell 会话中临时启用 CGO 后再运行迁移。下面的设置只影响当前窗口，不会修改系统级 Go 配置：

```powershell
# 确认当前目录是包含 go.mod 的项目根目录
if (!(Test-Path -LiteralPath '.\go.mod')) { throw '请先切换到 WeKnora 项目根目录' }

# 使用项目自带的 GCC，避免 Go 找不到 C 编译器
$compilerBin = Join-Path (Get-Location) '.local-tools\msys64\ucrt64\bin'
$env:CGO_ENABLED = '1'
$env:CC = Join-Path $compilerBin 'gcc.exe'
$env:CXX = Join-Path $compilerBin 'g++.exe'
$env:PATH = "$compilerBin;$env:PATH"

# 运行迁移；metric 沿用源 Collection，不删除旧 Collection
go run ./cmd/milvus-migrate --address 127.0.0.1:19530 --source weknora_embeddings --target weknora_embeddings_multilingual
```

如果项目目录中没有 `.local-tools\msys64\ucrt64\bin`，请先安装可用的 GCC，并将 `$env:CC`、`$env:CXX` 改为对应的 `gcc.exe`、`g++.exe` 绝对路径。
