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

渠道里的「域名白名单」校验的是 **API 请求的 `Origin` 头**，不是「允许哪些网站粘贴脚本」的抽象概念。

实际要填：

1. **Embed 页面源站**（必填）——聊天 iframe 内发 API 时浏览器会带这个 Origin  
   - 同域部署：`https://app.example.com`  
   - 独立子域：`https://embed.example.com`
2. **你的业务后端源站**（安全模式时建议填）——你自己写的「取令牌接口」在服务端调用 `POST .../exchange` 时，需带与白名单一致的 `Origin` 头（见 [embed-secure-mode.md](./embed-secure-mode.md)）

第三方商城 `https://shop.example.com` **通常不用**进白名单（它只是加载脚本，不直接调 embed API）。  
若你把 embed 反代到商城同域路径下（少见），才需要填商城域名。

## Widget 跨域与 sandbox

当宿主站 A 与 embed 源站 B **不同域**时，`weknora-widget.js` 会自动给内部 iframe 加上 `sandbox`（也可手动 `data-sandbox="true"`）。

A 与 B 同域时保持默认即可，无需 `data-sandbox`。

## 检查清单

- [ ] `EMBED_BASE_URL` 与真实访问地址一致（含 `https`）
- [ ] embed 子域能打开 `/embed/<渠道ID>` 和 `/weknora-widget.js`
- [ ] embed 子域 `/api/` 能连到后端
- [ ] 渠道白名单包含 embed 源站（及安全模式下的业务后端源站）
- [ ] 管理端复制的 snippet 里 URL 已变为 embed 子域

## 相关文档

- 发布 Token 不落浏览器：[embed-secure-mode.md](./embed-secure-mode.md)
