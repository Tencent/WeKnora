# 知识搜索 API

[返回目录](./README.md)

| 方法 | 路径               | 描述     |
| ---- | ------------------ | -------- |
| POST | `/knowledge-search` | 知识搜索 |

## POST `/knowledge-search` - 知识搜索

在知识库中搜索相关内容（不使用 LLM 总结），直接返回检索结果。

**参数说明（请求体）**:

| 字段                | 类型     | 必填 | 说明                                                       |
| ------------------- | -------- | ---- | ---------------------------------------------------------- |
| query               | string   | 是   | 搜索查询文本                                              |
| knowledge_base_id   | string   | 否   | 单个知识库 ID（向后兼容）；与 `knowledge_base_ids` 互斥    |
| knowledge_base_ids  | string[] | 否   | 多个知识库 ID 列表，跨知识库搜索                          |
| knowledge_ids       | string[] | 否   | 进一步限定到指定知识（文件）；不传则在整库范围内搜索       |

> 必须指定 `knowledge_base_id` 或 `knowledge_base_ids` 中的至少一个。

**请求**:

```curl
# 搜索单个知识库
curl --location 'http://localhost:8080/api/v1/knowledge-search' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "query": "如何使用知识库",
    "knowledge_base_id": "kb-00000001"
}'

# 搜索多个知识库
curl --location 'http://localhost:8080/api/v1/knowledge-search' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "query": "如何使用知识库",
    "knowledge_base_ids": ["kb-00000001", "kb-00000002"]
}'

# 搜索指定文件
curl --location 'http://localhost:8080/api/v1/knowledge-search' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "query": "如何使用知识库",
    "knowledge_ids": ["4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5"]
}'
```

**响应**:

```json
{
    "data": [
        {
            "id": "chunk-00000001",
            "content": "知识库是用于存储和检索知识的系统...",
            "knowledge_id": "knowledge-00000001",
            "chunk_index": 0,
            "knowledge_title": "知识库使用指南",
            "start_at": 0,
            "end_at": 500,
            "seq": 1,
            "score": 0.95,
            "chunk_type": "text",
            "image_info": "",
            "metadata": {},
            "knowledge_filename": "guide.pdf",
            "knowledge_source": "file"
        }
    ],
    "success": true
}
```

**响应字段说明（data[]）**:

| 字段               | 类型    | 说明                                  |
| ------------------ | ------- | ------------------------------------- |
| id                 | string  | 分块 ID                              |
| content            | string  | 命中的分块文本                       |
| knowledge_id       | string  | 该分块所属的知识 ID                  |
| chunk_index        | int     | 分块在知识中的序号                   |
| knowledge_title    | string  | 来源知识标题                         |
| start_at / end_at  | int     | 分块在源文档中的字符偏移             |
| seq                | int     | 命中排序号                           |
| score              | number  | 相似度（rerank 后归一化后的最终得分） |
| chunk_type         | string  | 分块类型（`text` / `image` / ...）   |
| image_info         | string  | 图像分块的额外信息（JSON 字符串）    |
| metadata           | object  | 自定义元数据                         |
| knowledge_filename | string  | 来源文件名                           |
| knowledge_source   | string  | 来源类型（`file` / `url` / `manual`） |
