# 存储后端（Storage Backends）

原始文件、解析出的图片、导出产物都要落在某个对象存储上。早期这只能靠环境变量配一套全局存储；migration `000068` 起改成**可注册多个存储实例**，空间选一个作默认，单个知识库还能绑定到指定实例。

典型用途：

- 不同团队/项目的资料落在不同的桶，便于分账与权限隔离；
- 合规要求某类文档必须存在特定地域的桶里；
- 从自建 MinIO 迁到云对象存储时，新库先用新后端，老库保持不动。

<Screenshot
  src="/screenshots/settings-storage-backends.png"
  caption="存储后端设置：多实例列表、默认实例与连通性测试"
  hint="展示已注册的存储后端卡片（provider、状态、默认标记）与新建/编辑表单，含「测试连接」结果。" />

## 怎么配

入口在「设置 → 存储」（`storage` 分区，需 Admin）：

1. 新建后端，选 provider（`local` / `minio` / `cos` / `oss` / `s3` / `tos` / `obs` 等，与[文档入库流程](../02-architecture/03-document-pipeline.md)里的存储 provider 一致），填连接参数；
2. **保存前点「测试」**：连通性测试会真实读写一次，配错的桶或过期的密钥能立刻发现，而不是等到上传文档时才报错；
3. 需要的话把它设为空间默认（`PUT /storage-backends/:id/default`，同时写回 `tenants.default_storage_backend_id`）。新建知识库不指定实例时就用这个默认值；
4. 单个知识库想用别的实例，在知识库编辑弹窗的「存储」页签里选——对应 `knowledge_bases.storage_backend_id`。

## 接口

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

## 数据模型与几个约束

`storage_backends` 表（`tenant_id` 隔离，软删除）关键字段：

| 字段 | 说明 |
| --- | --- |
| `name` | 空间内唯一（软删除下的部分唯一索引） |
| `provider` | 存储类型 |
| `config` | JSONB，含加密后的密钥 |
| `source` | `user`（界面注册）/ 其它（系统生成） |
| `status` | `active` / 停用 |
| `legacy_alias` | 见下 |

**`legacy_alias` 是为平滑升级准备的**：升级前用环境变量配置的那套全局存储，会被折算成一条别名记录，让老知识库的文件路径继续可解析，而不必做数据搬迁。同一空间同一 provider 只允许一条别名（部分唯一索引保证），因此它不会和你手工注册的实例混淆。

**库里一有文件就不能再换**：知识库的存储选择在**空库时可改**，一旦有了文件，界面上的选择框就被禁用并提示需要迁移（`KBStorageSettings.vue` 按 `hasFiles` 判断）。原因是已入库文件的路径是按当时的后端生成的，直接改绑定会让旧文件失联。确实要换的话，新建知识库再迁移内容。

## 与向量存储的区别

两者容易混：

| | 存储后端（Storage Backend） | 向量存储（Vector Store） |
| --- | --- | --- |
| 存什么 | 原始文件、图片、导出产物 | 向量与检索索引 |
| 配在哪 | 「设置 → 存储」 | 「设置 → 向量库」 |
| 知识库字段 | `storage_backend_id` | `vector_store_id` |
| 相关章节 | 本篇 | [检索引擎与向量存储](05-retrieval-engines.md) |
