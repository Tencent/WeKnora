# WeKnora 子路径部署指南 (Sub-path Deployment Guide)

在很多内网、政企或特定离线网关部署环境中，由于没有域名分发能力，多个内部子系统往往需要共享同一个外网 IP 和同一个入口端口（例如 `6000` 端口），并采用不同的子路径进行转发分发：
* `http://IP:6000/weknora/` -> WeKnora 系统
* `http://IP:6000/system1/` -> 外部子系统 1

本指南为您说明如何利用 `WEKNORA_BASE_PATH` 环境变量一键完成 WeKnora 的子路径化部署。

---

## 1. 部署配置流程

### 第一步：修改配置文件 (`.env`)
在 WeKnora 项目根目录的 `.env` 中加入子路径与暴露端口配置：

```env
# 统一网关分配给 WeKnora 的前缀子路径
WEKNORA_BASE_PATH=/weknora
# 前端服务暴露端口，可按需修改避免冲突
FRONTEND_PORT=8091
```

### 第二步：编译与重建容器
由于前端代码的基地址（Vite base）必须在编译时注入（构建期依赖），修改 `WEKNORA_BASE_PATH` 后**必须重新构建前端镜像**：

```bash
# 1. 重新编译前端静态资源并构建前端 UI 镜像
./scripts/build_images.sh -f

# 2. 重建并重启前端容器，使之加载应用最新生成的静态文件
docker compose up -d --no-deps --force-recreate frontend
```

---

## 2. 入口 Nginx 反向代理配置

在外层只暴露 `6000` 端口的统一入口 Nginx 上，增加如下配置对 WeKnora 流量进行分发：

> **⚠️ 重要事项**：外部 Nginx 的 `proxy_pass` 目标代理地址末尾**必须带上 `/weknora/` 后缀**。转发请求时**千万不要剥离** `/weknora/` 路径前缀，必须将其完整透传发送给 WeKnora 前端容器。

```nginx
server {
    listen 6000;
    client_max_body_size 100M;

    # 对输入地址不带斜杠的 /weknora 进行 301 重定向，保证浏览器能正确加载到子路径下的静态文件
    location = /weknora {
        return 301 /weknora/;
    }

    # 完整透传反向代理，不要剥离 /weknora/ 前缀
    location /weknora/ {
        proxy_pass http://127.0.0.1:8091/weknora/;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSockets 和 SSE 大模型流式输出（打字机效果）的必须配置
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        chunked_transfer_encoding off;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```
