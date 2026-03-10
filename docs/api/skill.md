# Skills API

[返回目录](./README.md)

| 方法 | 路径      | 描述               |
| ---- | --------- | ------------------ |
| GET  | `/skills` | 获取预装 Skills 列表 |

## GET `/skills` - 获取预装 Skills 列表

获取系统中所有预装的智能体技能列表。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/skills' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "name": "web_search",
            "description": "搜索互联网获取最新信息"
        },
        {
            "name": "code_interpreter",
            "description": "执行代码并返回结果"
        },
        {
            "name": "image_generation",
            "description": "根据文本描述生成图片"
        }
    ],
    "skills_available": true,
    "success": true
}
```

当系统未配置 Skills 时，`skills_available` 返回 `false`，`data` 为空数组：

```json
{
    "data": [],
    "skills_available": false,
    "success": true
}
```
