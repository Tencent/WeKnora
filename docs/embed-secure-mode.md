# Embed 安全模式

> **一句话**：长期密钥（发布 Token `em_…`）只放在**你自己的服务器**；访客浏览器里只有 30 分钟有效的短时令牌（`ems_…`）。

## 为什么要用？

| 方式 | 发布 Token 在哪 | 风险 |
|------|-----------------|------|
| iframe / 普通 Widget | 写在页面 HTML 或 URL hash 里 | 任何人「查看源代码」就能复制，等于公开密钥 |
| **安全模式 Widget** | 仅环境变量 / 密钥管理，在服务端 | 浏览器拿不到长期密钥；还可先校验访客是否登录 |

生产环境对外嵌入，**应优先用安全模式**。

## 两种 Token

| 名称 | 格式 | 谁持有 | 用途 |
|------|------|--------|------|
| 发布 Token | `em_…` | 仅你的服务端 | 向 WeKnora 换取短时令牌；在管理端「渠道密钥」查看 |
| 会话 Token | `ems_…` | 访客浏览器（iframe 内） | 调聊天、上传等 embed API；约 30 分钟过期，Widget 会自动刷新 |

## 工作流程

```
访客浏览器                 你的后端（shop 的服务器）              WeKnora
     │                              │                              │
     │ 1. 加载 Widget               │                              │
     │    data-token-endpoint       │                              │
     │    （不含 em_）               │                              │
     │─────────────────────────────►│                              │
     │                              │ 2. 校验访客已登录（可选）       │
     │                              │ 3. POST .../embed/:id/exchange │
     │                              │    Authorization: Embed em_…  │
     │                              │─────────────────────────────►│
     │                              │◄──── session_token (ems_…) ───│
     │◄── 4. { token, expiresIn } ──│                              │
     │ 5. iframe 用 ems_ 聊天        │                              │
```

对应管理端「嵌入渠道 → 安全模式」里的两段代码：

1. **页面脚本**：`data-token-endpoint="https://你的域名/weknora/embed-token"`（没有 `data-token`）
2. **服务端接口**：用发布 Token 调 exchange，把 `ems_…` 返回给前端

## 集成步骤

### 第 1 步：在 WeKnora 创建渠道

- 记下 **渠道 ID** 和 **发布 Token**（`em_…`）
- 配置域名白名单（见下）
- 配置分钟 / 日限流

### 第 2 步：部署取令牌接口

在你的业务后端新增一个 HTTP 接口（路径自定），要求：

**入参**：浏览器 GET 请求（Widget 会 `fetch` 这个地址）

**你必须做**：

- 校验调用方是合法访客（Session Cookie、JWT 等），未登录返回 `401`
- 用发布 Token 调 WeKnora exchange
- 成功时返回 JSON：`{ "token": "<ems_…>", "expiresIn": 1800 }`

**调 exchange 的约定**：

```http
POST https://<weknora-host>/api/v1/embed/<channel_id>/exchange
Authorization: Embed <发布 Token em_…>
Origin: https://<你的业务站点>    ← 须与渠道白名单一致，否则 403
```

> 服务端 `fetch` 默认不带 `Origin`，需要**手动设置**与白名单匹配的 `Origin` 头。

### 第 3 步：粘贴安全模式 Widget 代码

把 `data-token-endpoint` 改成上一步的真实 URL。完整示例在管理端「安全模式」Tab 可复制。

## 服务端示例

以下 `<WEKNORA_HOST>`、`<CHANNEL_ID>` 替换为实际值；发布 Token 放环境变量 `WEKNORA_PUBLISH_TOKEN`，**不要**写进前端。

### Node.js（Express）

```javascript
const WEKNORA_BASE = 'https://<WEKNORA_HOST>';
const CHANNEL_ID = '<CHANNEL_ID>';
const ALLOWED_ORIGIN = 'https://shop.example.com'; // 与渠道白名单一致

app.get('/weknora/embed-token', async (req, res) => {
  const hasSession = Boolean(req.cookies?.session_id);
  const auth = req.headers.authorization || '';
  if (!hasSession && !auth.startsWith('Bearer ')) {
    return res.status(401).json({ error: 'unauthorized' });
  }

  const r = await fetch(`${WEKNORA_BASE}/api/v1/embed/${CHANNEL_ID}/exchange`, {
    method: 'POST',
    headers: {
      Authorization: 'Embed ' + process.env.WEKNORA_PUBLISH_TOKEN,
      Origin: ALLOWED_ORIGIN,
    },
  });
  const body = await r.json();
  if (!body?.data?.session_token) {
    return res.status(502).json({ error: 'mint failed' });
  }
  res.json({ token: body.data.session_token, expiresIn: body.data.expires_in });
});
```

### Go（net/http）

```go
func embedTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	req, _ := http.NewRequest(http.MethodPost,
		"https://<WEKNORA_HOST>/api/v1/embed/<CHANNEL_ID>/exchange", nil)
	req.Header.Set("Authorization", "Embed "+os.Getenv("WEKNORA_PUBLISH_TOKEN"))
	req.Header.Set("Origin", "https://shop.example.com") // 与渠道白名单一致
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode >= 300 {
		http.Error(w, `{"error":"mint failed"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	var body struct {
		Data struct {
			SessionToken string `json:"session_token"`
			ExpiresIn    int    `json:"expires_in"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil || body.Data.SessionToken == "" {
		http.Error(w, `{"error":"mint failed"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token": body.Data.SessionToken, "expiresIn": body.Data.ExpiresIn,
	})
}
```

管理端「安全模式 → 服务端示例」Tab 会按当前渠道 ID 生成带真实 URL 的片段。

## 域名白名单怎么填？

| 要放行的请求来源 | 白名单示例 |
|------------------|------------|
| 聊天 iframe 所在源站（embed 页面） | `https://app.example.com` 或 `https://embed.example.com` |
| 你的取令牌后端（exchange 时带的 Origin） | `https://shop.example.com` |

若 embed 使用[独立子域](./embed-subdomain.md)，**两条都要加**（embed 源站 + 业务后端源站）。

开发环境可临时使用 `*`；**生产环境禁止 `*`**。

## 上线检查

- [ ] 发布 Token 仅通过环境变量 / 密钥服务注入，未提交到 Git、未打进前端静态包
- [ ] 取令牌接口校验访客身份
- [ ] 全链路 HTTPS
- [ ] 白名单已包含 embed 源站与 exchange 使用的 Origin
- [ ] 已配置限流；敏感智能体不要用普通模式把 Token 暴露在网页里
- [ ] 轮换发布 Token 后，同步更新服务端环境变量

## 常见问题

| 现象 | 原因与处理 |
|------|------------|
| exchange 返回 **401** / `publish token required` | 发布 Token 错误、已轮换，或误用了 `ems_` 会话 Token |
| exchange 或聊天 API 返回 **403** `origin not allowed` | 白名单未包含当前请求的 `Origin`；服务端 exchange 记得手动加 `Origin` 头 |
| iframe 一直「等待 Token」 | `token-endpoint` 未返回 `{ token, expiresIn }`，或 CORS 未允许 Widget 所在源站访问你的接口 |
| 取令牌接口 **502** `mint failed` | WeKnora 不可达、渠道已停用，或 exchange 响应格式不对 |
| 访客随便就能聊 | 取令牌接口未做登录校验——在 exchange 前加 Session / JWT 检查 |

## 相关

- 可选：embed 独立子域 → [embed-subdomain.md](./embed-subdomain.md)
- Widget SDK 注释：`frontend/public/weknora-widget.js`
- 代码生成：`frontend/src/api/embed/index.ts`
