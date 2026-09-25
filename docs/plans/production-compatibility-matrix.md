# Evaluation Dataset Studio 生产兼容矩阵

## 1. 文档状态

| 项目 | 状态 |
| --- | --- |
| 审计阶段 | 已暂停；等待生产版本、接口文档和服务端错误修复 |
| 本地当前版本 | 已完成代码、路由与 API 文档核对 |
| 生产版本 | 未确认；当前不继续推测 |
| 是否修改 WeKnora 后端 | 否 |
| 生产自动评测状态 | 暂停；已观察到认证失败和任务状态查询 HTTP 500 |
| 是否记录 API Key | 否 |

本文中的“已确认”仅代表当前仓库版本。生产列没有证据时一律标记为“待确认”，不能依据本地实现推断。当前开发改为 [`离线优先计划`](./2026-09-20-evaluation-dataset-studio-offline-first-plan.md)，本矩阵仅供未来恢复生产 Adapter 时使用。

## 2. Adapter 总览

| 能力 | `local-current` | `production-<version>` | `manual-export` |
| --- | --- | --- | --- |
| 读取知识库 | 已确认 | 待确认 | 不支持 |
| 读取对话模型 | 已确认 | 待确认 | 不支持 |
| 读取重排模型 | 已确认 | 待确认 | 不支持 |
| 启动评测 | 已确认 | 待确认 | 不支持 |
| 查询评测状态 | 已确认 | 待确认 | 不支持 |
| 取消评测 | 不支持 | 待确认 | 不支持 |
| 逐题结果 | 当前响应未提供 | 待确认 | 可后续导入外部结果 |
| 上传或注册数据集 | 不支持；需人工部署 | 待确认 | 人工部署 |
| 可配置 dataset ID | 当前服务仅支持 `default` | 待确认 | 记录人工指定值 |
| 自动版本识别 | 可读取系统信息接口 | 待确认 | 不适用 |

## 3. HTTP 与认证契约

所有路径均相对于 API 根地址。当前仓库默认 API 根地址是 `<base>/api/v1`。

| 用途 | `local-current` 证据 | 生产 | 风险与处理 |
| --- | --- | --- | --- |
| 健康检查 | `GET /health`，无需认证 | 待确认 | 只能用于连通性，不能证明评测可用 |
| 部署能力 | `GET /system/capabilities` | 待确认 | 响应 envelope 可能随版本变化 |
| 系统版本 | `GET /system/info` | 待确认 | scoped key 可能没有权限；可由运维提供版本信息 |
| 知识库列表 | `GET /knowledge-bases` | 待确认 | 当前 scoped key 需要 `retrieve` 或 full access |
| 模型列表 | `GET /models` | 待确认 | 当前 scoped key 需要 `manage_models` 或 full access |
| 启动评测 | `POST /evaluation` | 待确认 | 有副作用并产生模型费用，R0 禁止调用 |
| 查询评测 | `GET /evaluation?task_id=...` | 待确认 | 当前需要 `run_evaluations` 或 full access |
| 取消评测 | 没有路由 | 待确认 | 未确认前不显示可用按钮 |
| API 认证 | `X-API-Key: <key>` | 待确认 | 生产也可能使用 Bearer、Cookie 或额外空间 Header |
| 空间选择 | API Key 绑定租户/空间 | 待确认 | 不猜测 `X-Tenant-ID` 等额外 Header |
| HTTP 重定向 | Studio 当前拒绝 | 待确认 | 继续拒绝，避免凭据被转发到其他主机 |

## 4. 资源响应契约

| 资源 | `local-current` 最小依赖字段 | 生产 | Studio 内部映射 |
| --- | --- | --- | --- |
| 知识库 | envelope：`success`、`data[]`；元素：`id`、`name` | 待确认 | `id`、`name` |
| 对话模型 | `id`、`name`/`display_name`、`type=KnowledgeQA`、`status` | 待确认 | 可用对话模型选项 |
| 重排模型 | `id`、`name`/`display_name`、`type=Rerank`、`status` | 待确认 | 可用重排模型选项 |
| 可用状态 | 空值或 `active`；其他状态被过滤 | 待确认 | `available` 布尔值与原始状态 |

生产若返回分页对象而不是数组、改变模型类型枚举或使用其他 envelope，必须由生产 Adapter 显式转换。

## 5. 评测契约

### 5.1 当前请求

```json
{
  "dataset_id": "default",
  "knowledge_base_id": "<knowledge-base-id>",
  "chat_id": "<chat-model-id>",
  "rerank_id": "<rerank-model-id>"
}
```

| 项目 | `local-current` | 生产 |
| --- | --- | --- |
| 创建响应任务路径 | `data.task` | 待确认 |
| 任务 ID | `data.task.id` | 待确认 |
| 状态类型 | 整数 | 待确认 |
| 状态值 | `0` 待处理、`1` 运行中、`2` 成功、`3` 失败 | 待确认 |
| 进度 | `total`、`finished` | 待确认 |
| 错误 | `err_msg` 或 HTTP/envelope message | 待确认 |
| 检索指标 | `data.metric.retrieval_metrics` | 待确认 |
| 生成指标 | `data.metric.generation_metrics` | 待确认 |
| 逐题明细 | 当前响应未提供 | 待确认 |

任何生产状态值、字段名和指标名都必须来自文档或脱敏响应；不能复用本地整数枚举作为默认值。

## 6. 数据包契约

当前仓库已确认的导出 profile 暂命名为 `weknora-five-parquet-v1`：

| 根目录文件 | Arrow/Parquet 字段 |
| --- | --- |
| `queries.parquet` | `id: int64`、`text: string` |
| `corpus.parquet` | `id: int64`、`text: string` |
| `answers.parquet` | `id: int64`、`text: string` |
| `qrels.parquet` | `qid: int64`、`pid: int64` |
| `qas.parquet` | `qid: int64`、`aid: int64` |

| 项目 | `local-current` | 生产 |
| --- | --- | --- |
| ZIP 根目录文件 | 上述五个 Parquet 文件 | 待确认 |
| ZIP 内附加 manifest | 不允许，保持严格五文件 | 待确认 |
| 部署位置 | 人工解压到服务端 `dataset/samples/` | 待确认 |
| dataset ID | `default` | 待确认 |
| 上传/注册 API | 未发现 | 待确认 |
| 可追溯信息 | Studio 本地记录，不写入 ZIP | 待实现（R3） |

## 7. 生产审计证据

可以接受的证据，按优先级排列：

1. 与生产部署完全对应的 OpenAPI、API 文档或版本化源码标签。
2. 生产测试空间中只读接口的脱敏响应。
3. 生产运维提供的版本、构建号、认证与数据集部署说明。
4. 已存在任务的脱敏查询响应；R0 不创建新任务。

不接受以下内容作为生产契约：

- 当前本地仓库的行为。
- 仅凭 UI 字段名或错误提示做的推测。
- 对多个写接口逐一试探得到的偶然成功结果。
- 包含 API Key、Cookie、Token、内部域名或个人数据的原始日志。

## 8. 待确认项与阻塞关系

| 待确认项 | 是否阻塞 R1/R2 | 是否阻塞生产 Adapter | 是否阻塞生产验收 |
| --- | --- | --- | --- |
| 生产版本/构建标识 | 否 | 是 | 是 |
| API 根路径与认证方式 | 否 | 是 | 是 |
| 知识库/模型只读响应 | 否 | 是 | 是 |
| 评测创建与查询契约 | 否 | 是 | 是 |
| 数据包格式与部署方式 | 否 | 是 | 是 |
| 取消/逐题结果能力 | 否 | 否；可声明不支持 | 否 |

结论：目前可以继续执行 R1 的内部重构，也可以实现不依赖生产写操作的 R2 框架；在生产证据到位前不能实现或宣称 `production-<version>` Adapter。
