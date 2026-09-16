# 存储后端（Storage Backends）

存储后端保存原始文件、解析图片和导出产物。一个空间可以注册多个实例，指定默认实例，并为知识库单独选择存储位置。

多实例存储适用于以下场景：

- 将不同团队或项目的资料保存到独立存储桶，分别管理用量与权限；
- 按数据存放要求选择存储桶所在地域；
- 迁移到云对象存储时，让新知识库使用新后端，已有知识库继续访问原文件。

<Screenshot
  src="/screenshots/settings-storage-backends.png"
  caption="存储后端设置：多实例列表、默认实例与连通性测试"
  hint="展示已注册的存储后端卡片（provider、状态、默认标记）与新建/编辑表单，含「测试连接」结果。" />

## 注册和选择存储后端 {#怎么配}

空间 Admin 可在「设置 → 存储」注册和管理实例：

1. 新建后端，选 provider（`local` / `minio` / `cos` / `oss` / `s3` / `tos` / `obs` 等，与[文档入库流程](../02-architecture/03-document-pipeline.md)里的存储 provider 一致），填连接参数；
2. **测试连接**：测试会实际读写存储，验证端点、存储桶和凭据；
3. 将实例设为空间默认。新建知识库未指定实例时使用该默认值；
4. 需要单独指定时，在知识库编辑弹窗的「存储」页签选择实例。

知识库为空时可修改存储后端；已有文件时，界面会禁用选择并提示迁移。文件路径依赖入库时的后端，直接更换会影响原文件访问。需要更换时，应新建知识库并迁移内容。

## 与向量存储的区别

文件存储和向量存储分别管理原文件与检索索引：

| | 存储后端（Storage Backend） | 向量存储（Vector Store） |
| --- | --- | --- |
| 存什么 | 原始文件、图片、导出产物 | 向量与检索索引 |
| 配在哪 | 「设置 → 存储」 | 「设置 → 向量库」 |
| 知识库字段 | `storage_backend_id` | `vector_store_id` |
| 相关章节 | 本篇 | [检索引擎与向量存储](05-retrieval-engines.md) |

## 接口参考 {#接口}

| 方法 | 路径 | 权限 |
| --- | --- | --- |
| GET | `/storage-backends/types` | Viewer+，返回支持的 provider 及其字段定义 |
| GET | `/storage-backends`、`/storage-backends/:id` | Viewer+ |
| POST | `/storage-backends` | Admin+ |
| PUT / DELETE | `/storage-backends/:id` | Admin+ |
| POST | `/storage-backends/test` | Admin+，用未保存的参数试连 |
| POST | `/storage-backends/:id/test` | Admin+，测已保存的实例 |
| PUT | `/storage-backends/:id/default` | Admin+，设为空间默认 |

API Key 需要 `manage_storage_backends` 能力或 full-access。

## 数据模型与兼容规则 {#数据模型与几个约束}

`storage_backends` 表（`tenant_id` 隔离，软删除）关键字段：

| 字段 | 说明 |
| --- | --- |
| `name` | 空间内唯一（软删除下的部分唯一索引） |
| `provider` | 存储类型 |
| `config` | JSONB，含加密后的密钥 |
| `source` | `user`（界面注册）/ 其它（系统生成） |
| `status` | `active` / 停用 |
| `legacy_alias` | 见下 |

`legacy_alias` 用于兼容环境变量配置的历史存储。升级时创建别名记录，使已有文件路径继续可解析，无需搬迁数据。同一空间、同一 provider 只允许一条别名记录，手动注册的实例独立保存。
