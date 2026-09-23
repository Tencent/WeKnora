# 本机浏览器接入与部署

WeKnora 通过 BrowserSkill daemon 和 [BrowserSkill](https://github.com/Tencent/BrowserSkill) 浏览器扩展，将用户电脑上的浏览器接入智能推理对话。扩展支持 Chrome 和 Microsoft Edge（基于 Chromium 125 及以上），其他 Chromium 浏览器不保证兼容。浏览器在用户电脑上运行，daemon 在 WeKnora app 一侧运行，无需技能沙箱。网页剪藏和侧边栏知识库问答使用另一个[知识管理助手插件](06-chrome-extension.md)。

## 安装扩展

扩展需要 **0.3.1 及以上**版本，任选一种方式安装：

- Chrome：[Chrome 应用商店](https://chromewebstore.google.com/detail/hhcmgoofomhgciiibhipgmgkgnoenaoi)
- Edge：[Edge 加载项](https://microsoftedge.microsoft.com/addons/detail/browserskill/emacgiaaaiojkkpkddmmdfhmokgmnikg)
- 配套 ZIP：无法访问商店时，在「浏览器连接」页的「手动安装（备用）」下载。解压后在 `chrome://extensions`（Edge 为 `edge://extensions`）开启「开发者模式」，选择「加载已解压的扩展程序」。

已连接的扩展版本低于 0.3.1 时，「浏览器连接」页会提示升级。

## 配对与使用

1. 打开侧边栏「工具箱 → 浏览器连接」。旧的「设置 → 浏览器连接」链接会自动跳转到这里。
2. 复制页面生成的一次性配对链接，在扩展「连接设置 → 远程连接」粘贴并保存。
3. 在智能推理输入框开启「本机浏览器」，再提交任务。配对成功不代表每轮自动启用。
4. 首次调用创建独立任务窗口；对话预览可定位页面、暂停、继续或结束当前浏览器任务。

连接状态显示在「浏览器连接」标签上（已连接、离线或未配对）。已配对时，侧边栏「工具箱」右侧的浏览器小图标也会用圆点标出状态：绿色为已连接，橙色为离线。不需要时可以在「浏览器连接」页关闭「在侧边栏显示连接状态」，该设置只保存在当前浏览器。同一页还可以设置「浏览器搜索指令」，指定偏好的搜索引擎和搜索地址。

授权按空间和用户保存；同一空间内多个对话共用设备授权，但各有任务。当前每个空间＋用户保留一个设备授权，激活新设备会替换旧设备。

配对链接五分钟有效。设备令牌有效期 90 天，30 天后自动轮换；生成新链接不会立即断开旧设备，成功激活后才替换授权。撤销设备、停用用户或移除空间成员会阻止后续使用。

### 人工参与与任务恢复

任务可操作自己创建的标签；任务窗口内由点击或按键打开、且通过来源校验的新标签也可操作，结束任务时会保留。独立弹出窗口和用户原有标签通过 `tab_borrow` 借用，按浏览器设置确认授权后操作，完成后用 `tab_return` 归还。结束任务时关闭任务显式创建的页面、归还借用页面。

手动拖入任务窗口不等于授权；未授权页面已在任务窗口内时，需要用户先移到普通窗口再借用。借用确认提示需要普通窗口中的 HTTP(S) 页面承载，扩展设置页、新标签页和独立弹窗不能承载。若借用后原窗口消失，归还可能创建普通备用窗口；归还页面不会随任务结束被关闭。

登录、验证码或授权步骤由 `request_help` 发起。按预览提示进入浏览器，完成后在浏览器帮助提示中确认；Agent 再观察页面并继续。仅在聊天中说“请登录”不会创建人工接管提示。帮助等待最多五分钟，超时或取消会保留现场并暂停。

暂停取消当前浏览器调用但不撤销配对；点击继续后，如果 Agent 本轮已结束，还需发送继续指令。异常或服务重启后的任务不会自动重放点击、提交等动作，应恢复后先检查页面。

普通成功任务会自动结束；用户要求保留页面、人工帮助或异常中断时可保留现场。预览显示“已保留最后画面”表示当前不再轮询，不代表浏览器控制已释放。结束浏览器任务不等于停止整个对话。

## 单实例部署

配套 app 镜像包含 daemon、扩展 ZIP 和许可证，并预设文件路径；配套 frontend Nginx 处理 WebSocket。更新时同时更新 app、frontend 和用户的扩展。

原生部署在仓库根目录构建：

```bash
./scripts/build_browserskill.sh
```

需要 Git、Node.js、Python 3、Rust/Cargo 和 C 编译器，Linux 还需要 CMake。固定源码版本由 `scripts/browserskill-release.json` 管理，直接构建上游源码，不应用额外补丁，产物输出到 `artifacts/browserskill/`。使用对应操作系统与架构的 daemon，并按实际安装路径配置：

```dotenv
BROWSERSKILL_BINARY=/opt/weknora/browserskill/bsk
BROWSERSKILL_EXTENSION_PATH=/opt/weknora/browserskill/browser-skill-weknora-0.3.1.zip
BROWSERSKILL_MAX_CONNECTIONS=32
```

默认从用户当前页面 origin 生成配对 WSS 地址，保留端口。仅在独立网关域名或特殊路径部署时设置覆盖项：

```dotenv
BROWSERSKILL_PUBLIC_URL=wss://weknora.example.com/api/v1/local-browser/extension
```

本机可用 localhost WS，远端要求浏览器信任的 WSS 证书。内网可以使用受信任的企业 CA。显式设置 `BROWSERSKILL_BINARY=` 可关闭能力；修改 Docker 环境变量后使用 `docker compose up -d app frontend` 重建容器。

配套扩展和 daemon 必须一同升级，扩展需要 0.3.1 及以上（Chrome 应用商店、Edge 加载项或配套 ZIP 均可）。覆盖原解压目录并重新加载可保留扩展 ID，重新安装导致 ID 改变时需要重新配对。分发时保留 `BrowserSkill-LICENSE`。

## 多副本与入口代理

所有 app 共用数据库；设备授权、连接租约和中断标记持久化到数据库。每个 app 按需启动共享 daemon，其他副本通过签名内部 RPC 将任务交给持有扩展连接的节点。

```dotenv
# 每个副本不同，必须直达该节点，不能填负载均衡地址
BROWSERSKILL_INTERNAL_URL=http://10.0.0.12:8080
# 所有副本相同，从 Secret 注入至少 32 字符的随机值
BROWSERSKILL_CLUSTER_SECRET=<shared-random-secret>
```

Kubernetes 可使用 Pod IP 组成直连地址。不要用各副本独立的 SQLite 文件代替共享数据库。内部 HTTP 只适合可信隔离网络；需要传输保密时使用 HTTPS 和受信任证书。

入口代理必须：

- 转发 `/api/v1/local-browser/extension` 的 WebSocket Upgrade，以及 `/authorize` POST。
- 禁止从公共入口访问 `/api/v1/local-browser/internal`，仅允许 app 节点互访。配套 frontend 已拒绝该路径，直接暴露 app 或自定义 Ingress 时也要处理。
- 避免记录 Authorization、Sec-WebSocket-Protocol 和授权请求正文。

连接租约 45 秒，每十秒续租；节点宕机后的恢复受旧租约到期和扩展重连退避影响。服务恢复后检查任务状态，不自动重放未知结果的操作。

## 排障与验收

| 现象 | 检查方向 |
| --- | --- |
| 无配套扩展下载或能力不可用 | daemon 与扩展文件路径、可执行权限、目标架构和 app 镜像版本 |
| 提示扩展版本过低 | 从 Chrome 应用商店、Edge 加载项或配套 ZIP 升级到 0.3.1 及以上，覆盖原解压目录后重新加载 |
| 授权成功但 WSS 失败 | 公网地址/端口、受信任证书、外层代理 WebSocket Upgrade |
| 设备连接正常但任务不执行 | 本轮开关、任务是否暂停、是否等待标签借用或人工帮助 |
| 没有人工帮助提示 | 执行记录是否调用 `request_help`；扩展是否禁用了人工帮助 |
| 切换副本后失败 | 数据库是否共享、节点直连地址是否正确、共享密钥和网络策略 |
| 预览卡住 | 页面是否隐藏、任务是否保留最后画面、扩展中具体的 CDP 超时；预览为低帧率截图，不是视频 |

默认 32 是单节点活动设备保护上限，不是吞吐保证。上线时用目标集群验证配对、重连、撤销、人工帮助和跨副本路由，并观察 daemon CPU、RSS、截图流量、RPC 延迟与重连时间。真实扩展测试入口为 `internal/browserskill/extension_test.go`，使用独立测试浏览器资料目录。
