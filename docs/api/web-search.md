# Web Search API

[返回目录](./README.md)

| 方法 | 路径                     | 描述                   |
| ---- | ------------------------ | ---------------------- |
| GET  | `/web-search/providers`  | 获取网络搜索服务商列表 |

## GET `/web-search/providers` - 获取网络搜索服务商列表

获取系统中可用的网络搜索服务商列表。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/web-search/providers' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "name": "google",
            "label": "Google Search",
            "description": "通过 Google 自定义搜索 API 进行网络搜索",
            "enabled": true
        },
        {
            "name": "bing",
            "label": "Bing Search",
            "description": "通过 Bing Search API 进行网络搜索",
            "enabled": true
        },
        {
            "name": "serpapi",
            "label": "SerpAPI",
            "description": "通过 SerpAPI 进行搜索引擎结果抓取",
            "enabled": false
        }
    ],
    "success": true
}
```
