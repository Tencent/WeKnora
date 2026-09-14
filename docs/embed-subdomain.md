# Embed 独立子域部署（可选）

> **一句话**：把聊天页面单独放到 `embed.example.com`，与主站 `app.example.com` 分开。**大多数部署不需要这一步**——主站和 embed 同域就能用。只有对安全隔离有明确要求时再考虑。

## 先搞清楚三个「网站」

嵌入聊天时，实际涉及三类地址（可以相同，也可以不同）：

| 角色 | 举例 | 干什么 |
|------|------|--------|
| **A. 业务站点（宿主）** | `https://shop.example.com` | 你的商城 / 文档站；在这里粘贴 Widget 脚本或 iframe |
| **B. Embed 页面源站** | `https://app.example.com` 或 `https://embed.example.com` | 提供 `embed.html`、`weknora-widget.js`；聊天 iframe 加载自这里 |
| **C. WeKnora API** | 通常与 B 同域，如 `https://app.example.com/api` | 后端接口 |

**默认（推荐入门）**：B 和主站管理后台都在 `https://app.example.com`，A 可以是任意第三方域名。

```
shop.example.com          app.example.com
┌─────────────────┐       ┌──────────────────────────┐
│ <script src=    │       │ /weknora-widget.js       │
│  app.../widget> │──────►│ /embed/:channelId        │
│                 │       │ /api/v1/embed/...        │
│  (浮窗 iframe)   │◄──────│                          │
└─────────────────┘       └──────────────────────────┘
```

**独立子域（进阶）**：把 B 拆到 `https://embed.example.com`，主站 `https://app.example.com` 只留管理后台。

```
shop.example.com          embed.example.com        app.example.com
┌─────────────────┐       ┌──────────────────┐     ┌─────────────┐
│ Widget 脚本      │──────►│ embed 静态页+API  │     │ 管理后台     │
│ iframe 指向 embed│◄──────│ （无 index SPA）  │     │ （可选分离） │
└─────────────────┘       └──────────────────┘     └─────────────┘
```

## 什么时候需要独立子域？

| 场景 | 是否需要 |
|------|----------|
| 内测、PoC、单域 Docker 部署 | **不需要** |
| 主站 `X-Frame-Options: SAMEORIGIN`，但要把聊天嵌到第三方页面 | **不需要**（被嵌的是 `/embed/*` 页面，不是管理后台 SPA） |
| 希望 embed 站点不携带主站登录 Cookie、缩小暴露面 | **可以考虑** |
| 希望 CDN / WAF 对 embed 流量单独限速、缓存 | **可以考虑** |
| 合规要求「对外嵌入」与「内部管理」必须不同源 | **需要** |

本地开发：`http://localhost:5173` 同域即可，Vite 已把 `/embed/*` 指到 `embed.html`，**不必**配子域。

## 怎么配置

### 1. 告诉管理端「embed 页面公网地址」

管理端生成 iframe / Widget 代码时，需要知道 B 的地址。在 `frontend/public/config.js`（或 Docker 启动脚本生成的配置）里设置：

```javascript
window.__RUNTIME_CONFIG__ = {
  // embed 页面与 widget.js 的源站；留空则使用当前浏览器所在域名
  EMBED_BASE_URL: 'https://embed.example.com',
  MAX_FILE_SIZE_MB: 50,
};
```

构建期也可通过环境变量注入：`VITE_EMBED_BASE_URL=https://embed.example.com`（可选）。

### 2. Nginx：单独 server 块只服务 embed

`frontend/nginx.conf` 顶部有注释掉的示例。要点：

- `server_name embed.example.com`
- 只暴露：`/embed/*` → `embed.html`，`/weknora-widget.js`，`/assets/*`
- `/api/` 反代到 WeKnora 后端（与主站相同后端即可）
- **不要**在这个 server 上挂完整 `index.html` 管理 SPA（减少攻击面）

主站 `server` 块可继续 `X-Frame-Options: SAMEORIGIN`，不影响第三方嵌 embed 子域的 iframe。

### 3. 域名白名单填什么？

填写**允许嵌入的宿主网站 Origin**：A 网站嵌入 B 上的 WeKnora，就填 A，例如 `https://shop.example.com`，不需要为了聊天 API 再添加 B。

- 每行一个完整 Origin（协议、域名、可选端口），不能带业务路径、查询参数或用户名。允许末尾 `/`，匹配时会规范化。
- `*.example.com` 允许 HTTP(S) 子域名及其端口，不包含根域 `example.com`；`*.example.com:8443` 可限定端口。
- 嵌入 HTML 的 `Content-Security-Policy: frame-ancestors` 由渠道白名单生成，由浏览器检查所有祖先页面；同源管理端预览也允许。
- iframe 内 API 请求来自 B，正常同源请求不再要求 B 出现在宿主白名单。安全模式下业务后端换取令牌仍需手动发送 `Origin: https://shop.example.com`，与白名单一致。
- 空白名单、无效或停用渠道拒绝加载；不要用 `*` 作为生产配置。

**部署要求**：标准前端 Nginx 的 `/embed/` 必须保留 `auth_request`、内部 `/_embed-frame-policy` 和 CSP 响应头配置。它通过后端 `/api/v1/embed-frame-policy` 获取策略，再直接返回 `embed.html`。后端不可用时拒绝返回嵌入页面。Lite 在 Go 静态页面响应上设置同一策略。自定义网关/CDN 不得移除或缓存该响应头；HTTPS 代理应保留原始 Host（含端口）、协议和 `Sec-Fetch-Site`。

**升级已有渠道**：过去仅填写 WeKnora 的 B 地址的渠道，需改为实际宿主 A；同时更新前端 Nginx 和后端。不会自动猜测或放行未知宿主。安全模式的业务来源配置保持一致。

白名单限制浏览器嵌入，不替代用户认证。直接打开链接或非浏览器客户端仍由 token、会话签名和限流保护；需控制访客身份时使用安全模式。

## Widget 跨域与 sandbox

当宿主站 A 与 embed 源站 B **不同域**时，`weknora-widget.js` 会自动给内部 iframe 加上 `sandbox`（也可手动 `data-sandbox="true"`）。

A 与 B 同域时保持默认即可，无需 `data-sandbox`。

## 检查清单

- [ ] `EMBED_BASE_URL` 与真实访问地址一致（含 `https`）
- [ ] embed 子域能打开 `/embed/<渠道ID>` 和 `/weknora-widget.js`
- [ ] embed 子域 `/api/` 能连到后端
- [ ] 渠道白名单包含实际宿主网站 A；安全模式 exchange 声明同一业务 Origin
- [ ] 管理端复制的 snippet 里 URL 已变为 embed 子域

## 相关文档

- 发布 Token 不落浏览器：[embed-secure-mode.md](./embed-secure-mode.md)
