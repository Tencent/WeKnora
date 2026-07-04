# 向量检索性能基准测试报告

日期：2026-07-04

## 背景

issue1418 分支为 MySQL 检索器增加了 MySQL 9.0+ 原生 `VECTOR(N)` 存储支持。本次基准测试在原 MySQL 8 JSON、MySQL 9 VECTOR 结果基础上，补充了项目原本支持的 `pgvector` 和 SQLite `sqlite-vec`，用于横向比较不同后端的向量检索和批量写入表现。

当前实现路径：

- PostgreSQL/pgvector：`halfvec` 存储，HNSW 索引，数据库侧 cosine distance 排序。
- SQLite/sqlite-vec：`vec0` 虚拟表，数据库侧 cosine distance 排序。
- MySQL 9.4 Community：原生 `VECTOR(dim)` 存储，但 Community Server 未提供 `DISTANCE()` / `VECTOR_DISTANCE()`，排序仍走 Go 端余弦排序。
- MySQL 8.0.37：`JSON` 存储，Go 端余弦排序。
- 纯 Go 回退路径：不含数据库 I/O，仅衡量 Go 端排序成本。

## 测试环境

| 项目 | 值 |
| --- | --- |
| 日期 | 2026-07-04 |
| 运行方式 | Docker 容器内运行 Go 基准测试 |
| Go 镜像 | `golang:1.26.0` |
| OS/Arch | `linux/amd64` |
| CPU | Intel(R) Core(TM) Ultra 9 275HX |
| MySQL 8 镜像 | `mysql:8.0.37` |
| MySQL 9 镜像 | `mysql:9.4` |
| MySQL 9 版本 | `9.4.0 MySQL Community Server - GPL` |
| PostgreSQL 镜像 | `pgvector/pgvector:pg16` |
| SQLite | 文件型临时库，启用 `sqlite-vec` CGO 扩展 |

MySQL 9.4 冒烟测试结果：

- `CREATE TABLE ... embedding VECTOR(3)` 成功。
- `STRING_TO_VECTOR('[1,2,3]')` 写入成功。
- `VECTOR_TO_STRING(embedding)` 读取成功。
- `DISTANCE(...)` 失败：`ERROR 1305 (42000): FUNCTION ... DISTANCE does not exist`。

SQLite 基准测试需要 `libsqlite3-dev`，本次容器通过 Aliyun Debian 镜像安装依赖后运行。

## 测试方法

新增或补充的基准测试文件：

```text
internal/application/repository/retriever/mysql/benchmark_test.go
internal/application/repository/retriever/postgres/benchmark_test.go
internal/application/repository/retriever/sqlite/benchmark_test.go
```

数据规模：

| 参数 | 值 |
| --- | ---: |
| 向量嵌入条数 | 1000 |
| 向量维度 | 768 |
| TopK | 10 |
| Threshold | 0 |
| 基准测试重复次数 | 5 |

主要命令：

```bash
go test ./internal/application/repository/retriever/mysql \
  -run '^$' \
  -bench 'BenchmarkMySQLRetriever' \
  -benchmem \
  -count=5

go test ./internal/application/repository/retriever/postgres \
  -run '^$' \
  -bench 'BenchmarkPostgresRetriever' \
  -benchmem \
  -count=5

go test ./internal/application/repository/retriever/sqlite \
  -run '^$' \
  -bench 'BenchmarkSQLiteRetriever' \
  -benchmem \
  -count=5
```

## 检索结果汇总

| 场景 | 平均耗时 | 内存分配 | 分配次数 | 说明 |
| --- | ---: | ---: | ---: | --- |
| pgvector 检索 | 0.661 ms/op | 160.0 KB/op | 510 allocs/op | `halfvec` + HNSW + 数据库侧排序 |
| SQLite sqlite-vec 检索 | 0.721 ms/op | 29.8 KB/op | 444 allocs/op | `vec0` + 数据库侧排序 |
| 纯 Go 回退排序 | 65.96 ms/op | 16.06 MB/op | 15003 allocs/op | 不含 DB I/O |
| MySQL 9 VECTOR 检索 | 75.12 ms/op | 13.93 MB/op | 25357 allocs/op | `VECTOR` 存储 + DB 读取 + Go 排序 |
| MySQL 8 JSON 检索 | 105.97 ms/op | 13.92 MB/op | 25335 allocs/op | `JSON` 存储 + DB 读取 + Go 排序 |

相对 MySQL 9 VECTOR 检索：

| 场景 | 相对速度 |
| --- | ---: |
| pgvector 检索 | 约 113.6 倍更快 |
| SQLite sqlite-vec 检索 | 约 104.2 倍更快 |
| MySQL 8 JSON 检索 | 约 1.41 倍更慢 |

## 批量写入结果汇总

| 场景 | 平均耗时 | 内存分配 | 分配次数 | 说明 |
| --- | ---: | ---: | ---: | --- |
| pgvector 批量保存 | 116.47 ms/op | 112.03 MB/op | 38524 allocs/op | 1000 条批量 upsert，写入 `halfvec` |
| MySQL 8 JSON 批量保存 | 287.07 ms/op | 51.03 MB/op | 24353 allocs/op | 1000 条批量 upsert，写入 `JSON` |
| MySQL 9 VECTOR 批量保存 | 294.65 ms/op | 55.80 MB/op | 26961 allocs/op | 1000 条批量 upsert，写入时使用 `STRING_TO_VECTOR(?)` |
| SQLite sqlite-vec 批量保存 | 4958.76 ms/op | 12.03 MB/op | 105782 allocs/op | GORM 批量建元数据后，逐条同步 FTS5 和 `vec0` |

## 原始补充结果

pgvector：

```text
BenchmarkPostgresRetrieverVectorRetrieve-24  650755 ns/op  160900 B/op  516 allocs/op
BenchmarkPostgresRetrieverVectorRetrieve-24  672201 ns/op  159493 B/op  512 allocs/op
BenchmarkPostgresRetrieverVectorRetrieve-24  641734 ns/op  159693 B/op  511 allocs/op
BenchmarkPostgresRetrieverVectorRetrieve-24  673276 ns/op  160370 B/op  516 allocs/op
BenchmarkPostgresRetrieverVectorRetrieve-24  668356 ns/op  159629 B/op  497 allocs/op

BenchmarkPostgresRetrieverBatchSave-24  115984790 ns/op  112031170 B/op  38497 allocs/op
BenchmarkPostgresRetrieverBatchSave-24  123926541 ns/op  112033768 B/op  38542 allocs/op
BenchmarkPostgresRetrieverBatchSave-24  113189692 ns/op  112033744 B/op  38542 allocs/op
BenchmarkPostgresRetrieverBatchSave-24  104273225 ns/op  112031130 B/op  38497 allocs/op
BenchmarkPostgresRetrieverBatchSave-24  124978579 ns/op  112033766 B/op  38542 allocs/op
```

SQLite：

```text
BenchmarkSQLiteRetrieverVectorRetrieve-24  700673 ns/op  29742 B/op  444 allocs/op
BenchmarkSQLiteRetrieverVectorRetrieve-24  729218 ns/op  29753 B/op  444 allocs/op
BenchmarkSQLiteRetrieverVectorRetrieve-24  716495 ns/op  29743 B/op  444 allocs/op
BenchmarkSQLiteRetrieverVectorRetrieve-24  725570 ns/op  29769 B/op  444 allocs/op
BenchmarkSQLiteRetrieverVectorRetrieve-24  731501 ns/op  29756 B/op  444 allocs/op

BenchmarkSQLiteRetrieverBatchSave-24  5047784787 ns/op  12026976 B/op  105788 allocs/op
BenchmarkSQLiteRetrieverBatchSave-24  4947261360 ns/op  12026576 B/op  105783 allocs/op
BenchmarkSQLiteRetrieverBatchSave-24  5018423052 ns/op  12025936 B/op  105775 allocs/op
BenchmarkSQLiteRetrieverBatchSave-24  4847003026 ns/op  12027120 B/op  105779 allocs/op
BenchmarkSQLiteRetrieverBatchSave-24  4933342205 ns/op  12027456 B/op  105783 allocs/op
```

## 对比分析

### 检索性能

pgvector 和 SQLite 都把向量距离计算放在数据库扩展内部完成，在 1000 条、768 维、TopK=10 的数据规模下，检索耗时都在 `0.7 ms/op` 左右。

MySQL 9 VECTOR 检索平均 `75.12 ms/op`，虽然比 MySQL 8 JSON 检索的 `105.97 ms/op` 快约 `29.11%`，但仍明显慢于 pgvector 和 sqlite-vec。主要原因不是 MySQL 9 的 `VECTOR` 存储本身，而是 MySQL 9.4 Community 当前无法在数据库侧执行距离排序，现有实现必须读取候选向量嵌入后在 Go 端解析和排序。

纯 Go 回退排序平均 `65.96 ms/op`。MySQL 9 VECTOR 检索与它的差值约 `9.16 ms/op`，可视为当前 1000 条候选下 DB 读取、`VECTOR_TO_STRING` 转换、扫描和对象分配的综合开销。

### 写入性能

pgvector 批量保存平均 `116.47 ms/op`，是本次写入测试最快的数据库后端。

MySQL 9 VECTOR 批量保存平均 `294.65 ms/op`，比 MySQL 8 JSON 批量保存的 `287.07 ms/op` 慢约 `2.64%`。这部分开销主要来自写入时对每条向量嵌入调用 `STRING_TO_VECTOR(?)`。

SQLite sqlite-vec 批量保存平均 `4958.76 ms/op`，明显慢于其他后端。这里反映的是当前 SQLite 检索器实现的写入路径：元数据使用 GORM 批量创建，但 FTS5 和 `vec0` 同步仍逐条执行。SQLite 检索性能很好，但批量索引构建路径需要单独优化。

### 内存与分配

pgvector 检索分配约 `510 allocs/op`，SQLite 检索约 `444 allocs/op`，均明显低于 MySQL 检索的约 `25k allocs/op`。MySQL 当前需要把向量嵌入从数据库列读出，再解析为 Go 向量并排序，分配成本较高。

pgvector 写入内存分配较高，主要来自批量构造 1000 条 768 维向量写入参数。SQLite 写入分配次数最高，主要来自逐条同步 FTS5 和 `vec0` 的实现方式。

## 结论

1. 如果目标是在线向量检索性能，pgvector 仍是当前最优后端：检索约 `0.661 ms/op`，写入约 `116.47 ms/op`。
2. SQLite sqlite-vec 的检索性能接近 pgvector，约 `0.721 ms/op`，适合本地、轻量、读多写少场景。
3. SQLite 当前批量写入很慢，约 `4.96 s/op`，不适合大批量索引导入路径直接作为性能基线，需要优化事务和 vec/FTS 批量同步。
4. MySQL 9.4 Community 已确认支持原生 `VECTOR(N)` 存储，但不支持数据库侧距离函数；相比 MySQL 8 JSON，检索提升约 `29.11%`，但性能仍主要受 Go 端排序限制。
5. MySQL 9 VECTOR 写入比 MySQL 8 JSON 慢约 `2.64%`，属于可接受范围，但不是性能优势点。

## 建议

- 生产级向量检索优先使用 pgvector。
- SQLite 保留为本地和轻量部署方案，但建议后续优化 `BatchSave`：显式事务、批量写入 `vec0`、减少 FTS5 逐条同步开销。
- MySQL 检索器推荐在 MySQL 9.0+ 使用原生 `VECTOR(N)` 存储，同时继续保留 JSON 回退路径兼容 MySQL 8.x。
- MySQL 仍需保留 Go 侧余弦回退路径，直到目标 MySQL 发行版明确提供可用的数据库侧距离函数。
- 后续可调研直接扫描 MySQL `VECTOR` 二进制表示，避免 `VECTOR_TO_STRING` + JSON parse，降低检索分配次数和 CPU 成本。

## 复跑命令

pgvector：

```bash
docker run --name weknora-pgvector-bench \
  -e POSTGRES_PASSWORD=123456 \
  -e POSTGRES_DB=weknora_bench \
  -p 127.0.0.1:5433:5432 \
  -d pgvector/pgvector:pg16

docker run --rm --network host \
  -v "D:\项目\WeKnora:/src" \
  -w /src \
  -e LOG_LEVEL=error \
  -e GOMODCACHE=/src/.codex_tmp/gomodcache \
  -e GOCACHE=/src/.codex_tmp/gocache \
  -e WEKNORA_POSTGRES_BENCH_DSN='host=127.0.0.1 port=5433 user=postgres password=123456 dbname=weknora_bench sslmode=disable TimeZone=UTC' \
  golang:1.26.0 \
  go test ./internal/application/repository/retriever/postgres \
    -run '^$' \
    -bench 'BenchmarkPostgresRetriever' \
    -benchmem \
    -count=5

docker stop weknora-pgvector-bench
```

SQLite：

```bash
docker run --name weknora-go-sqlite-bench \
  -v "D:\项目\WeKnora:/src" \
  -w /src \
  -e LOG_LEVEL=error \
  -e GOMODCACHE=/src/.codex_tmp/gomodcache \
  -e GOCACHE=/src/.codex_tmp/gocache \
  -d golang:1.26.0 sleep infinity

docker exec weknora-go-sqlite-bench bash -lc \
  "sed -i 's|https://deb.debian.org/debian-security|https://mirrors.aliyun.com/debian-security|g; s|https://deb.debian.org/debian|https://mirrors.aliyun.com/debian|g' /etc/apt/sources.list.d/debian.sources && apt-get update && apt-get install -y --no-install-recommends libsqlite3-dev"

docker exec -w /src \
  -e LOG_LEVEL=error \
  -e GOMODCACHE=/src/.codex_tmp/gomodcache \
  -e GOCACHE=/src/.codex_tmp/gocache \
  weknora-go-sqlite-bench \
  /usr/local/go/bin/go test ./internal/application/repository/retriever/sqlite \
    -run '^$' \
    -bench 'BenchmarkSQLiteRetriever' \
    -benchmem \
    -count=5

docker stop weknora-go-sqlite-bench
```

MySQL 9.4：

```bash
docker run --name weknora-mysql94-bench \
  -e MYSQL_ROOT_PASSWORD=root \
  -e MYSQL_DATABASE=weknora_bench \
  -p 127.0.0.1:3309:3306 \
  -d mysql:9.4

docker run --rm --network host \
  -v "D:\项目\WeKnora:/src" \
  -w /src \
  -e GOMODCACHE=/src/.codex_tmp/gomodcache \
  -e GOCACHE=/src/.codex_tmp/gocache \
  -e WEKNORA_MYSQL_BENCH_DSN='root:root@tcp(127.0.0.1:3309)/weknora_bench?charset=utf8mb4&parseTime=true&loc=UTC' \
  golang:1.26.0 \
  go test ./internal/application/repository/retriever/mysql \
    -run '^$' \
    -bench 'BenchmarkMySQLRetriever' \
    -benchmem \
    -count=5

docker stop weknora-mysql94-bench
```

MySQL 8.0.37：

```bash
docker run --name weknora-mysql-bench \
  -e MYSQL_ROOT_PASSWORD=root \
  -e MYSQL_DATABASE=weknora_bench \
  -p 127.0.0.1:3307:3306 \
  -d mysql:8.0.37

docker run --rm --network host \
  -v "D:\项目\WeKnora:/src" \
  -w /src \
  -e GOMODCACHE=/src/.codex_tmp/gomodcache \
  -e GOCACHE=/src/.codex_tmp/gocache \
  -e WEKNORA_MYSQL_BENCH_DSN='root:root@tcp(127.0.0.1:3307)/weknora_bench?charset=utf8mb4&parseTime=true&loc=UTC' \
  golang:1.26.0 \
  go test ./internal/application/repository/retriever/mysql \
    -run '^$' \
    -bench 'BenchmarkMySQLRetriever' \
    -benchmem \
    -count=5

docker stop weknora-mysql-bench
```
