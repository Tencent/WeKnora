# WeKnora 官网与文档

`website-docs/` 包含官网、文档、共享样式和全部构建部署脚本，可独立复制和构建，不依赖仓库外层文件。官网和文档共用一个域名、一次构建和一份部署产物。

产品、部署、API 和开发文档统一在这里维护。旧 `docs/` 的有效内容已按主题合并，过时和重复正文已删除；迁移取舍及仍保留的工程资源见[迁移记录](MIGRATION.md)；该记录仅供维护者使用，不发布到站点。

- `/`：产品首页。
- `/docs/`：直接进入“快速上手”。
- `/docs/…`：完整文档，保留分类导航、搜索和侧栏。

顶栏高 64px、横向铺满。两边使用 README 原版商标，共用颜色、字体和深浅色偏好；Logo 在当前标签返回官网。官网和文档各自使用适合当前页面的导航。

## 发布方式：静态文件 + Nginx

### 1. 准备部署包

使用 Node.js 24，在仓库的 `website-docs/` 目录执行。首次构建或依赖锁文件变更后，先安装依赖：

```bash
cd website-docs
npm run setup
npm run build
npm run package:site
```

`build` 会编译官网、检查文档链接和 Mermaid、编译文档，并校验合并后的资源路径。全部通过后更新 `static-site/`；`package:site` 将其打包到 `releases/`。

### 2. 上传并解压

只需把压缩包上传到服务器。服务器不需要 Node.js，也不需要 WeKnora 后端或独立文档服务。

每次发布使用一个新的空目录，避免混入旧站点文件。下面的 `20260915-1` 是示例发布编号，后续发布换一个编号：

```bash
sudo mkdir -p /srv/www/weknora/releases/20260915-1
sudo tar -xzf weknora-site-v0.8.0.tar.gz -C /srv/www/weknora/releases/20260915-1
```

解压后该目录下应直接有 `index.html`、`docs/`、`_next/`，无需再套一层 `static-site/`。

### 3. 配置域名根目录

以 [deploy/nginx.conf](deploy/nginx.conf) 为模板添加站点配置，修改：

```nginx
server_name 你的域名;
root /srv/www/weknora/releases/20260915-1;
```

如果域名已有 HTTPS 配置，保留原来的证书及监听设置，把模板中的 `root`、`index` 和 `location` 规则合入现有站点配置。域名应指向这台服务器。

必须保留文档的 `.html` 路由解析规则；不要把所有未知路径回退到官网 `index.html`。当前构建部署在域名根路径，不支持直接放进 `/weknora/` 等子目录。

检查配置后重载：

```bash
sudo nginx -t
sudo nginx -s reload
```

### 4. 确认上线

访问以下路径：

- `/`：官网。
- `/docs/`：快速上手。
- `/docs/03-features/23-memory`：长期记忆文档，直接打开和刷新均正常。
- `/docs/not-found`：返回 404。

再确认搜索、深浅色切换和 Logo 返回官网正常。

后续更新重复构建、打包、上传到新目录，再修改 Nginx `root` 并重载。需要回滚时，将 `root` 改回上一发布目录。确认新版本稳定后再清理旧发布目录。

## 可选：Docker 部署

从干净源码即可构建，无需在宿主机安装 Node.js 或提前生成静态文件。Dockerfile 使用两个阶段：Node.js 24 按两份锁文件安装依赖并构建官网与文档，最终 Nginx 镜像只包含静态产物和服务配置。

在仓库根目录执行：

```bash
docker build -t weknora-site:0.8.0 website-docs
docker run -d --name weknora-site --restart unless-stopped -p 8080:80 weknora-site:0.8.0
```

访问 `http://服务器地址:8080/`。如使用域名和 HTTPS，让现有反向代理转发到该端口即可。镜像内已经包含官网、文档和 Nginx 路由配置。

也可以只复制 `website-docs/` 目录，在该目录执行 `docker build -t weknora-site:0.8.0 .`。构建上下文必须是 `website-docs/`，宿主机的依赖、旧构建产物和部署包由 `.dockerignore` 排除。

从旧文档镜像迁移时，将容器端口映射或反向代理目标端口从 `8081` 改为 `80`（宿主机端口可自行选择，例如 `-p 8081:80`）。域名根路径 `/` 现在提供官网，`/docs/` 提供文档，反向代理需覆盖整个站点并保留请求路径。容器使用 Nginx 官方镜像的默认入口，无需额外的 `docker-entrypoint.sh`。

构建后可在安装了 Node.js 24 和 Docker 的机器上执行以下检查，无需安装 npm 依赖。检查会临时启动容器并自动清理，验证两站路由、静态资源、404、压缩和响应头：

```bash
cd website-docs
npm run test:docker -- weknora-site:0.8.0
```

## 本地开发

使用 Node.js 24 LTS（目录内提供 `.nvmrc`，使用 nvm 时可执行 `nvm use`）。首次获取源码后安装依赖（分别按文档和官网的锁文件安装）：

```bash
cd website-docs
npm run setup
npm run build
npm run preview
```

预览地址：`http://127.0.0.1:3000/`。修改源码后重新构建并刷新；可用 `npm run preview -- --port 8080` 指定其他端口。

```bash
npm run check
npm run test:integration
```

集成检查需要安装 Google Chrome，验证主题同步、两边导航、搜索、手机端控件及直达文档路由。可用 `SITE_TEST_URL` 指定待验证的站点。

开发时可分别使用 `npm run dev:homepage`（官网）和 `npm run dev:docs`（文档）获得热更新；完整站内跳转请使用统一构建后的 `npm run preview`。

`npm run build:docs` 只编译文档，`npm run build:homepage` 只编译官网；正式发布使用 `npm run build`，输出会同时包含两者。`VERSION` 是这份站点构建使用的发布版本。

## 工程目录（相对于 website-docs）

- `homepage/`：Next.js 官网源码、品牌素材及独立依赖锁文件。
- `01-getting-started/` 至 `06-development/`：文档正文，原有路径不变。
- `.vitepress/`：文档站配置与主题。
- `public/`、`sample-data/`：文档截图和示例。
- `shared/`：品牌变量、顶栏样式及共用图标。
- `scripts/`：统一构建、检查、预览和打包。
- `static-site/`：唯一的部署目录，由构建生成。
- `deploy/`、`Dockerfile`：Nginx 和 Docker 部署配置。
- `releases/`：生成的部署压缩包。

品牌素材来源见 [homepage/BRAND-ASSETS.md](homepage/BRAND-ASSETS.md)。产品视频使用 README 中的 GitHub 原始附件，播放需要访问 GitHub；页面、文档和截图随包部署。
