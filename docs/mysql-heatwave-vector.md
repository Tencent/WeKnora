# MySQL HeatWave 向量加速

WeKnora 通过现有 `mysql` 检索器驱动支持 MySQL HeatWave，不提供单独的
`mysql_heatwave` 驱动。按普通 MySQL 检索器方式配置即可：

```env
RETRIEVE_DRIVER=mysql
MYSQL_HOST=mysql
MYSQL_PORT=3306
MYSQL_USERNAME=root
MYSQL_PASSWORD=
MYSQL_DATABASE=weknora
MYSQL_TABLE_PREFIX=weknora_embeddings
```

MySQL 检索器开始写入向量嵌入时，会通过
`VECTOR_TO_STRING(STRING_TO_VECTOR(...))` 探测服务器是否支持原生向量类型。

- 探测成功时，新建的按维度分表会使用 `VECTOR(N)`。
- 探测失败时，表会使用 `JSON` 存储向量嵌入。
- 已存在的向量嵌入表不会在 `JSON` 和 `VECTOR(N)` 之间自动迁移。

向量检索时，WeKnora 会探测数据库侧余弦距离函数，例如 `DISTANCE(..., 'COSINE')`
和 `VECTOR_DISTANCE(..., 'COSINE')`。

- 如果有可用函数，检索会在 SQL 中计算 `score = 1 - distance`，并按 `score DESC`
  排序。
- 如果没有可用函数，或者检测到的函数后续运行失败，检索会回退到 Go 侧余弦排序。
- 该回退路径保证普通 MySQL Community 和 Commercial 部署仍可正常使用。

MySQL Community 9.x 支持原生 `VECTOR(N)` 类型，但 `DISTANCE()` /
`VECTOR_DISTANCE()` 的可用性取决于 OCI 上的 MySQL HeatWave 或 MySQL AI。参考
MySQL 向量文档：

- https://dev.mysql.com/doc/refman/9.4/en/vector.html
- https://dev.mysql.com/doc/refman/9.4/en/vector-functions.html

可选集成测试：

```bash
WEKNORA_MYSQL_TEST_DSN='user:pass@tcp(localhost:3306)/weknora' \
go test ./internal/application/repository/retriever/mysql

WEKNORA_MYSQL_HEATWAVE_TEST_DSN='user:pass@tcp(heatwave-host:3306)/weknora' \
go test ./internal/application/repository/retriever/mysql -run TestMySQLHeatWaveRetrieverIntegration
```
