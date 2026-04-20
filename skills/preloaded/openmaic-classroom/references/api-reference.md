# OpenMAIC API Reference

OpenMAIC 服务 API 接口规范。所有 API 基础路径为 `{base_url}/api`，默认 `http://localhost:3000/api`。

## 认证机制

- 如果 OpenMAIC 未设置 `ACCESS_CODE` 环境变量：所有接口开放
- 如果设置了 `ACCESS_CODE`：需先通过 `/api/access-code/verify` 获取 cookie
- `/api/health` 始终无需认证

### 获取认证 Cookie

```
POST {base_url}/api/access-code/verify
Content-Type: application/json

{ "code": "<ACCESS_CODE>" }
```

成功返回 `{ "success": true, "valid": true }` 并设置 `openmaic_access` cookie（7 天有效）。

## 标准响应格式

**成功**:
```json
{ "success": true, ...additionalFields }
```

**错误**:
```json
{ "success": false, "errorCode": "<Code>", "error": "<message>", "details?": "<detail>" }
```

---

## 核心端点

### 1. 健康检查

```
GET {base_url}/api/health
```

响应：
```json
{
  "success": true,
  "status": "ok",
  "version": "0.1.0",
  "capabilities": {
    "webSearch": true,
    "imageGeneration": false,
    "videoGeneration": false,
    "tts": true
  }
}
```

> `capabilities` 用于功能检测，只在返回此字段时才启用对应的可选功能。

---

### 2. 生成课程（异步任务）

#### 2a. 创建生成任务

```
POST {base_url}/api/generate-classroom
Content-Type: application/json
```

请求体：
```json
{
  "requirement": "教学主题描述",
  "pdfContent": { "text": "PDF文本内容", "images": [] },
  "enableWebSearch": false,
  "enableImageGeneration": false,
  "enableVideoGeneration": false,
  "enableTTS": false,
  "agentMode": "default"
}
```

成功响应 (202):
```json
{
  "success": true,
  "jobId": "abc123xyz",
  "status": "queued",
  "step": "queued",
  "message": "Classroom generation job queued",
  "pollUrl": "{base_url}/api/generate-classroom/abc123xyz",
  "pollIntervalMs": 5000
}
```

错误：
- `400`: `requirement` 字段缺失
- `500`: 内部错误

#### 2b. 轮询任务状态

```
GET {pollUrl}
```

响应：
```json
{
  "success": true,
  "jobId": "abc123xyz",
  "status": "running",
  "step": "generating_outlines",
  "progress": 35,
  "message": "Generating scene outlines...",
  "pollUrl": "...",
  "pollIntervalMs": 5000,
  "scenesGenerated": 2,
  "totalScenes": 6,
  "result": null,
  "error": null,
  "done": false
}
```

最终成功响应：
```json
{
  "success": true,
  "status": "succeeded",
  "result": {
    "classroomId": "Uyh82Y32ZK",
    "url": "{base_url}/classroom/Uyh82Y32ZK",
    "scenesCount": 6
  },
  "done": true
}
```

最终失败响应：
```json
{
  "success": true,
  "status": "failed",
  "error": "具体错误信息",
  "done": true
}
```

---

### 3. 解析 PDF

```
POST {base_url}/api/parse-pdf
Content-Type: multipart/form-data
```

表单字段：
- `pdf`（文件，必填）: PDF 文件
- `providerId`（可选）: PDF 提供商，默认 `"unpdf"`
- `apiKey`（可选）: 提供商 API Key
- `baseUrl`（可选）: 提供商基础 URL

响应：
```json
{
  "success": true,
  "data": {
    "text": "提取的文本内容",
    "images": [],
    "metadata": {
      "pageCount": 10,
      "fileName": "document.pdf",
      "fileSize": 1024000
    }
  }
}
```

---

### 4. 课程存储

#### 4a. 获取课程

```
GET {base_url}/api/classroom?id=<classroomId>
```

#### 4b. 持久化课程

```
POST {base_url}/api/classroom
Content-Type: application/json

{ "stage": {...}, "scenes": [...] }
```

---

### 5. Web 搜索

```
POST {base_url}/api/web-search
Content-Type: application/json

{ "query": "搜索关键词", "pdfText": "PDF上下文", "apiKey": "Tavily Key" }
```

---

### 6. 验证 LLM 连接

```
POST {base_url}/api/verify-model
Content-Type: application/json

{ "model": "openai:gpt-4o", "apiKey": "...", "baseUrl": "..." }
```

---

## 生成管线（逐步调用）

如果需要更细粒度的控制，可以使用以下逐步生成端点代替 `/api/generate-classroom`：

### A. 生成场景大纲（SSE 流）

```
POST {base_url}/api/generate/scene-outlines-stream
```

请求体：
```json
{
  "requirements": { "requirement": "主题", "userNickname": "用户昵称" },
  "pdfText": "PDF文本",
  "pdfImages": [],
  "researchContext": "网络搜索结果",
  "agents": []
}
```

Headers:
- `x-image-generation-enabled`: `"true"` 或 `"false"`
- `x-video-generation-enabled`: `"true"` 或 `"false"`

SSE 事件类型：`languageDirective`、`outline`、`done`、`error`、`retry`

### B. 生成场景内容

```
POST {base_url}/api/generate/scene-content
Content-Type: application/json

{
  "outline": {...},
  "allOutlines": [...],
  "stageId": "stage-1",
  "pdfImages": [],
  "agents": [],
  "languageDirective": "使用简体中文"
}
```

### C. 生成场景动作

```
POST {base_url}/api/generate/scene-actions
Content-Type: application/json

{
  "outline": {...},
  "allOutlines": [...],
  "content": {...},
  "stageId": "stage-1",
  "agents": [],
  "languageDirective": "使用简体中文"
}
```

### D. 生成 Agent 画像

```
POST {base_url}/api/generate/agent-profiles
Content-Type: application/json

{
  "stageInfo": { "name": "入门阶段", "description": "..." },
  "sceneOutlines": [{ "title": "...", "description": "..." }],
  "languageDirective": "使用简体中文",
  "availableAvatars": [],
  "avatarDescriptions": []
}
```
