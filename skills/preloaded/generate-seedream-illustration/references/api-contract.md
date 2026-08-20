# Seedream 5.0 Pro API 契约

## 端点

```
POST https://ark.cn-beijing.volces.com/api/v3/images/generations
```

## 鉴权

```
Authorization: Bearer $ARK_API_KEY
Content-Type: application/json
```

API Key 通过环境变量 `ARK_API_KEY` 传入，在火山方舟控制台获取。

## 请求参数

| 参数 | 类型 | 必选 | 默认值 | 说明 |
|---|---|---|---|---|
| `model` | string | 是 | - | 模型 ID，固定为 `doubao-seedream-5-0-pro-260628` |
| `prompt` | string | 是 | - | 图片生成提示词，支持中英文。建议不超过 300 汉字或 600 英文单词 |
| `size` | string | 否 | `2K` | 图片尺寸，见下方尺寸说明 |
| `response_format` | string | 否 | `url` | 返回格式：`url` 或 `b64_json` |
| `watermark` | boolean | 否 | `true` | 是否添加"AI生成"水印。本 Skill 默认 `false` |
| `sequential_image_generation` | string | 否 | `disabled` | 组图功能：`auto` 或 `disabled`。本 Skill 固定 `disabled` |
| `stream` | boolean | 否 | `false` | 是否流式输出。本 Skill 固定 `false` |

### size 参数

**方式 1：分辨率档位**

| 值 | 适用场景 |
|---|---|
| `1K` | 快速预览、缩略图 |
| `2K` | 默认，正文配图 |
| `4K` | 高精度需求 |

**方式 2：宽高像素值**

| 宽高比 | 推荐像素值 | 适用场景 |
|---|---|---|
| 1:1 | `2048x2048` | 正方形信息图 |
| 4:3 | `2304x1728` | 横版插画 |
| 3:4 | `1728x2304` | 竖版插画 |
| 16:9 | `2560x1440` | 宽幅流程图、架构图 |
| 9:16 | `1440x2560` | 竖版知识卡片 |
| 3:2 | `2496x1664` | 横版杂志风格 |
| 2:3 | `1664x2496` | 竖版杂志风格 |
| 21:9 | `3024x1296` | 宽幅时间轴 |

> 方式 1 和方式 2 不可混用。使用方式 1 时需在 prompt 中用自然语言描述宽高比或用途，由模型判断最终尺寸。

## curl 模板

```bash
curl -X POST https://ark.cn-beijing.volces.com/api/v3/images/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ARK_API_KEY" \
  -d '{
    "model": "doubao-seedream-5-0-pro-260628",
    "prompt": "提示词内容",
    "size": "2K",
    "response_format": "url",
    "watermark": false,
    "sequential_image_generation": "disabled",
    "stream": false
  }'
```

## 响应参数

### 成功响应

```json
{
  "model": "doubao-seedream-5-0-pro-260628",
  "created": 1757321139,
  "data": [
    {
      "url": "https://...",
      "size": "2560x1440"
    }
  ],
  "usage": {
    "generated_images": 1,
    "output_tokens": 14400,
    "total_tokens": 14400
  }
}
```

| 字段 | 说明 |
|---|---|
| `model` | 请求使用的模型 ID |
| `created` | 请求创建时间的 Unix 时间戳（秒） |
| `data[].url` | 图片下载链接，**有效期 24 小时**，需及时下载 |
| `data[].b64_json` | Base64 编码的图片数据（当 `response_format` 为 `b64_json` 时返回） |
| `data[].size` | 实际生成图片的宽高像素值 |
| `usage.generated_images` | 成功生成的图片张数 |
| `usage.output_tokens` | 消耗的 token 数（计算逻辑：sum(图片长×图片宽)/256，取整） |

### 错误响应

```json
{
  "error": {
    "code": "invalid_api_key",
    "message": "Invalid API key"
  }
}
```

| HTTP 状态码 | 含义 | 处理方式 |
|---|---|---|
| 401 | 鉴权失败 | 检查 `$ARK_API_KEY` |
| 400 | 参数错误 | 检查请求体格式 |
| 429 | 限流 | 等待 5 秒后重试，最多 2 次 |
| 500 | 内部错误 | 不重试，标记失败 |

## 计费

- 按成功生成的图片张数计费，生成失败不计费。
- token 用量 = sum(图片长 × 图片宽) / 256，取整。
- 具体单价参见火山方舟模型计费页面。
