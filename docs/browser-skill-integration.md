# BrowserSkill 本机浏览器接入

WeKnora 使用 Tencent/BrowserSkill 的官方 daemon 和扩展执行浏览器操作，通过 `local_browser` 薄工具适配接入，无需沙箱、浏览器技能包或用户运行本机命令。

## 使用流程

1. 管理员运行 `./scripts/build_browserskill.sh`，为后端配置 `BROWSERSKILL_BINARY`、`BROWSERSKILL_PUBLIC_URL` 和 `BROWSERSKILL_EXTENSION_PATH`，重启服务。
2. 用户在个人设置 → 浏览器连接下载配套扩展，解压后在 Chrome 扩展程序页面加载。当前没有商店发布版本。
3. 在设置页复制一次性配对链接，粘贴到扩展的「远程连接」，核对服务器并连接。同一空间内多个对话共用设备授权。
4. 回到对话描述浏览器任务。首次工具调用会在现有 Chrome 窗口创建后台任务标签，以绿色「WeKnora」标签组标识。模型切换目标标签不会切走用户正在看的页面。
5. 主对话小预览可定位任务标签、暂停、继续或结束。请求人工帮助时先显示提示；不会自动激活标签或窗口。借用已有标签必须经过原生确认，归还时解除控制，保留原位置。

授权只覆盖任务创建的标签和用户明确批准借用的标签；标签组仅提供视觉标识，不作为权限依据。即使用户把其他标签拖入组内，模型也不会获得其控制权。结束任务只清理任务创建的标签，不关闭共用窗口及其他对话的标签。上游的本机 daemon 连接模式仍使用原有独立窗口。

## 预览与命令

自动化命令按对话串行执行。UI 预览通过扩展现有 WSS 的独立请求获取，不进入 daemon 自动化队列，因此长导航或人工等待不会占住截图通道。

预览获取当前任务标签的 CDP 画面，不调用激活窗口/标签 API。JPEG 宽度最多 640 像素。前端每轮完成后间隔 1 秒刷新；服务端短缓存和扩展在途合并限制重复截图。页面隐藏时停止刷新，回到页面后立即更新。它是约 1 fps 的低帧率截图同步，不是视频流；失败超过 5 秒会提示画面暂未更新。

`wait_ms` 参数为 `duration_ms`，范围 0–10000 毫秒；工具描述、参数 schema 和执行前校验保持一致。命令超时或中断后暂停任务，不自动重放点击/提交。前端显示具体浏览器动作与可读错误，原始协议信息放在折叠的技术详情中。

## 构建与部署

固定源码提交 `5aaa36bf79a201ec40b277ce6c24f2ce23ce37ca`、官方 daemon v0.2.1、协议 v1.1。构建脚本应用 `patches/browserskill/remote-extension-connection.patch`，以冻结依赖构建扩展，并验证 daemon 下载包 SHA-256。产物位于 `artifacts/browserskill/`，不提交二进制。

```dotenv
BROWSERSKILL_BINARY=/opt/weknora/browserskill/bsk
BROWSERSKILL_PUBLIC_URL=wss://weknora.example.com/api/v1/local-browser/extension
BROWSERSKILL_EXTENSION_PATH=/opt/weknora/browserskill/browser-skill-weknora-0.2.1.zip
```

本机调试可用 `ws://localhost:8080/api/v1/local-browser/extension`；远程部署要求 WSS，内网可使用受浏览器信任的企业 CA。详细授权、重连、多副本路由、迁移及容量边界见 [生产部署链路](browser-skill-production.md)。

扩展使用官方 MIT 许可，分发时保留 `BrowserSkill-LICENSE`。配对、UI 定位和预览属于本次提出的网关协议约定，并非已经发布的 BrowserSkill 稳定接口。

## 验证

```bash
go test -race ./internal/browserskill ./internal/agent/... ./internal/handler/session \
  ./internal/router ./internal/application/service ./internal/container ./internal/middleware
```

原生/真实扩展测试另设：

```dotenv
BROWSERSKILL_TEST_BINARY=/absolute/path/to/bsk
BROWSERSKILL_TEST_EXTENSION=/absolute/path/to/unpacked-extension
BROWSERSKILL_TEST_CHROMIUM=/absolute/path/to/Chromium
BROWSERSKILL_TEST_PLAYWRIGHT=/absolute/path/to/playwright-core/index.mjs
BROWSERSKILL_TEST_HEADED=1
```

测试使用独立浏览器资料目录，覆盖配对、后台标签组、导航/输入/点击、目标标签截图、人工等待期间预览、显式定位、暂停/继续/结束、任务隔离、授权持久化和双节点路由。目标部署网络、真实模型任务与规模容量仍须现场验收；本机浏览器不保证免除网站验证或 403。
