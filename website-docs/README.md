# WeKnora 官网与文档

`website-docs/` 包含官网、文档、共享样式和全部构建部署脚本，可独立复制和构建，不依赖仓库外层文件。官网和文档共用一个域名、一次构建和一份部署产物。

产品、部署、API 和开发文档统一在这里维护。旧 `docs/` 的有效内容已按主题合并，过时和重复正文已删除；迁移取舍及仍保留的工程资源见[迁移记录](MIGRATION.md)；该记录仅供维护者使用，不发布到站点。

- `/`：产品首页。
- `/docs/`：直接进入“快速上手”。
- `/docs/…`：完整文档，保留分类导航、搜索和侧栏。

站点只占用这两个路径：根目录下只有 `index.html`、`404.html` 和 `docs/`。官网的脚本、样式和图片都放在 `/docs/_home/` 下，所以上游网关只需转发 `/`（精确匹配）和 `/docs/` 前缀。`build` 会拒绝引用这两个路径以外资源的页面。

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
sudo tar -xzf weknora-site-v0.8.3.tar.gz -C /srv/www/weknora/releases/20260915-1
```

解压后该目录下应直接有 `index.html`、`404.html`、`docs/`，无需再套一层 `static-site/`。

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
docker build -t weknora-site:0.8.3 website-docs
docker run -d --name weknora-site --restart unless-stopped -p 8080:80 weknora-site:0.8.3
```

访问 `http://服务器地址:8080/`。如使用域名和 HTTPS，让现有反向代理转发到该端口即可。镜像内已经包含官网、文档和 Nginx 路由配置。

容器内的 Nginx 默认监听 80 端口，可以用环境变量 `WEBSITE_NGINX_PORT` 修改，无需重新构建。使用 `--network host`，或者部署平台要求容器监听指定端口时，这样设置：

```bash
docker run -d --name weknora-site --restart unless-stopped -e WEBSITE_NGINX_PORT=8080 -p 8080:8080 weknora-site:0.8.3
```

只是想换一个对外端口时，改 `-p` 左侧的宿主机端口就够了，例如 `-p 9000:80`。

也可以只复制 `website-docs/` 目录，在该目录执行 `docker build -t weknora-site:0.8.3 .`。构建上下文必须是 `website-docs/`，宿主机的依赖、旧构建产物和部署包由 `.dockerignore` 排除。

从旧文档镜像迁移时，将容器端口映射或反向代理目标端口从 `8081` 改为 `80`（宿主机端口可自行选择，例如 `-p 8081:80`）。域名根路径 `/` 现在提供官网，`/docs/` 提供文档，反向代理需覆盖整个站点并保留请求路径。容器使用 Nginx 官方镜像的默认入口，无需额外的 `docker-entrypoint.sh`。

构建后可在安装了 Node.js 24 和 Docker 的机器上执行以下检查，无需安装 npm 依赖。检查会临时启动容器并自动清理，验证两站路由、静态资源、404、压缩和响应头：

```bash
cd website-docs
npm run test:docker -- weknora-site:0.8.3
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
- `embed.go`：把 `01-getting-started/` 至 `07-releases/` 的正文编进 WeKnora 服务端，供内置的 WeKnora 助手检索和引用。新增分类目录时，同步更新 `embed.go` 和 `internal/productdocs/library.go` 中的分类列表。服务端测试会检查每篇正文能否解析、站内链接与锚点是否有效。

品牌素材来源见 [homepage/BRAND-ASSETS.md](homepage/BRAND-ASSETS.md)。产品视频使用 README 中的 GitHub 原始附件，播放需要访问 GitHub；页面、文档和截图随包部署。

## v0.8.3 截图清单

文档使用 `Screenshot` 组件展示真实产品截图，首页复用对应图片。维护截图时使用演示数据，隐藏 Token、真实邮箱和内部地址。

当前 11 张截图已全部接入，画廊、多模态设置、Wiki 分组图谱、原文引用定位均已同步到首页。多模态截图展示图片向量开关，Seafile 截图展示凭据配置，模型目录截图展示列表，创建用户截图展示成功结果。下表记录截图内容与使用位置，未展示的操作由正文说明。

所有文档图片放在 `website-docs/public/screenshots/`：

| 文件名 | 拍摄内容 | 使用位置 |
| --- | --- | --- |
| `kb-image-gallery-detail.png` | 图片详情，展示原图、图片描述、识别文字与来源文档 | 知识库功能、版本说明、首页 |
| `kb-image-gallery.png` | 画廊瀑布流，展开来源文档多选筛选，包含图表与照片 | 知识库功能、版本说明、首页 |
| `kb-image-vector-settings.png` | 支持图片输入的向量模型、多模态设置和图片向量检索开关（可用腾讯 WeMM-Embedding 示例） | 知识库功能、版本说明、首页 |
| `chat-source-location.png` | 带引用的回答，以及原始 PDF/Office 文件中的对应高亮 | 会话体验、版本说明、首页 |
| `chat-mention-selector.png` | 输入框的 @ 资源选择菜单，展示分类、资源数量和搜索入口 | 版本说明 |
| `chat-attachment-read.png` | 含图片文档的附件卡片、正文按需阅读和图片观察结果 | 会话体验、版本说明 |
| `system-user-management.png` | 用户列表、启停状态、系统管理员标记和操作菜单 | 平台管理、版本说明 |
| `system-model-catalog.png` | 模型目录、来源标记、覆盖编辑或版本历史 | 版本说明 |
| `wiki-graph-groups.png` | 新图谱布局、主题说明与分组聚焦，保留切换分组和返回全图入口 | Wiki 功能、版本说明、首页 |
| `datasource-seafile.png` | Seafile 数据源与资料库/目录/文件选择 | Seafile 功能 |
| `system-admin-create-user.png` | 创建用户成功结果，使用演示账号并隐藏密码 | 平台管理 |

首页复用其中的 `kb-image-gallery.png`、`kb-image-gallery-detail.png`、`kb-image-vector-settings.png`、`wiki-graph-groups.png`、`chat-source-location.png`，更新时只需替换 `website-docs/public/screenshots/` 中的源图；首页的 `prebuild` / `predev` 会自动同步已有同名副本（终端截图的旧文件名也会对应同步）。添加全新首页截图时，先在 `website-docs/homepage/public/docs/_home/product/` 建立副本。完成后运行 `npm run build`，检查图片引用与本地资源校验结果；页面外观由维护者人工确认。

本次发布整理的提交清单、文档核对依据和验证记录保存在主仓库 `docs/release-v0.8.3-audit.md`，不随独立站点发布。v0.8.3 发布日期为 2026-10-14，版本页、CHANGELOG 与四种语言 README 保持一致。

首页内容按两层维护：当期版本区以左侧功能导航、右侧截图展示画廊、多模态 Embedding（含腾讯 WeMM 支持）、Wiki 图谱、原文引用定位四项重点；左侧四项说明均保持展开，画廊通过「浏览图片 / 查看详情」切换两张截图；本机浏览器左侧分别展开「执行任务」「连接扩展」两种状态的说明，右侧切换对应截图，MCP Server 归入生态集成，对话追加、分叉与回滚归入 Agent 介绍。技能与沙箱、Wiki、模型生态与企业部署保留独立位置。语言新增等较小更新归入版本说明，后续换版时不要随版本区一并移除这些长期能力。

首页每项重点只用一句话说明用途，配置条件放到对应功能文档；引用定位与图谱更新同列版本重点；附件阅读、用户管理、Seafile 与 Docker 终端保留次级链接。本机浏览器复用「执行任务／连接扩展」两张大图与 BrowserSkill 图标、GitHub 入口。版本说明面向使用者，提交计数、代码快照和撤回记录留在发布核对报告。

首页章节底色按顺序交替：01／03／05／07 使用基础底色，02／04／06 与使用入口使用浅色背景；深色模式使用对应主题变量。所有章节背景铺满页面，正文使用同一宽度容器，浅色背景区负责相邻章节的分隔线，统一章节间距。
