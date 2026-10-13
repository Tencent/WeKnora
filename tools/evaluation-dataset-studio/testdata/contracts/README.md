# WeKnora 契约 Fixture

本目录保存版本适配器的脱敏请求/响应样例，用于契约测试，不保存真实生产流量。

目录命名规则：

- `local-current/`：当前仓库版本的已确认契约。
- `production-<version>/`：生产审计完成后按版本新增，不能覆盖旧目录。

fixture 必须满足：

1. 不包含 API Key、Authorization、Cookie、Token 或模型服务密钥。
2. UUID、名称、时间、租户和业务指标全部使用虚构值。
3. 保留字段层级、JSON 类型、状态枚举和 envelope 结构。
4. 有副作用的响应可由文档构造，但文件说明中必须标明，不得为采集样例而调用生产写接口。
5. 生产返回的新字段可以保留结构；敏感字段删除或替换。

`local-current/evaluation-start.json` 来自当前仓库 API 文档契约，并非本次审计新发起的评测。

`local-current/knowledge.json` 和 `chunks.json` 描述只读的文档、文本分块分页契约，用于验证“知识库分块导入语料”的 Adapter；内容和 UUID 均为虚构值。
