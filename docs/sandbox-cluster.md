# WeKnora 沙箱集群与标准模板

本文面向部署和平台管理员，说明如何把 CubeSandbox 或 E2B 接入 WeKnora。Docker、Local、CubeSandbox、E2B 都在同一个空间设置页面通过同一套配置与检查接口管理；只有远端后端需要本文所述的集群和模板准备。普通智能体使用者不需要搭建模板，也不应该逐项猜测运行环境。

## 谁负责什么

| 角色 | 职责 |
| --- | --- |
| WeKnora 发布流程 | 维护 `docker/Dockerfile.sandbox`，发布与 WeKnora 版本匹配的 `wechatopenai/weknora-sandbox` 镜像 |
| 集群管理员 | 部署 CubeSandbox 或开通 E2B，并保证控制面模板 API 可用 |
| 空间管理员 | 在“设置 → 沙箱后端”中填写集群地址和凭据，先完成连接验证，再选择接口返回的模板 |
| 智能体管理员 | 在智能体的 Skills 配置中选择已经验证过的沙箱后端 |

“WeKnora 标准模板”指模板内容由 WeKnora 维护，并不代表所有集群共享同一个模板 ID。CubeSandbox 的模板 ID、E2B 的模板 ID/别名都属于具体集群或账号；跨集群硬编码一个 ID 会指向不存在或内容不一致的模板。

## 标准模板包含什么

标准镜像定义在 `docker/Dockerfile.sandbox`，当前包含：

- Python 3.11；
- Node.js 20、npm 与 npx；
- jq 及基础 Shell 工具；
- `/workspace` 工作目录；
- UID 1000 的非 root `sandbox` 用户。

生产环境应使用与 WeKnora 相同的版本标签，不建议长期指向 `latest`。Skills 新增系统依赖时，应先更新标准镜像并重新注册模板，再切换集群的默认模板 ID。

## CubeSandbox

1. 按 [CubeSandbox Quick Start](https://github.com/TencentCloud/CubeSandbox/blob/master/docs/guide/quickstart.md) 完成控制面、计算节点、CubeProxy 和域名解析配置。生产环境还需要按官方文档完成鉴权、TLS、网络策略和多节点部署。
2. 在 WeKnora 的空间设置中填写 CubeAPI、CubeProxy、sandbox domain 和可选 API Key。若这些端点位于 RFC1918/loopback 网络，显式打开“允许访问私网集群地址”。
3. 点击“连接并继续”。WeKnora 先验证控制面地址与凭据，通过后才进入模板步骤并调用集群模板列表；如果不存在名为 `weknora` 的标准模板，会从 `wechatopenai/weknora-sandbox:latest` 发起一次构建。
4. 模板构建状态会自动刷新。状态变为 `READY` 后才可选择并进入运行配置；界面显示模板名称、状态和版本，配置内部才保存该集群自己的 `template_id`。

多实例 WeKnora 必须配置 Redis，以共享 session 到 sandbox 的绑定。只有单实例开发环境才应使用内存绑定。

## E2B

E2B 官方托管服务和自建 E2B Infrastructure 都可接入。填写 API Key 后先执行“连接并继续”，流程与 Cube 相同：验证连接后列出账号可见模板，缺少 `weknora` 时通过 E2B Template API 从标准镜像启动后台构建。自建部署还需填写 API URL 和 sandbox domain；E2B 上游通过 Terraform 提供 AWS、GCP 等部署方式，具体以 [E2B self-hosting guide](https://github.com/e2b-dev/infra/blob/main/self-host.md) 和 [E2B Template 文档](https://e2b.dev/docs/template/quickstart) 为准。

## 在设置页面完成接入

1. 打开“设置 → 沙箱后端”，点击“添加沙箱后端”。
2. 填写该集群自己的 API、Proxy、sandbox domain 和凭据；这些值只保存在当前空间配置中，不读取 Sandbox 环境变量。
3. 点击“连接并继续”，验证控制面地址和凭据；连接通过后才加载模板列表。
4. 等待自动创建的标准模板就绪，或选择集群已有的兼容模板；构建中的模板不可选择，状态会自动刷新。
5. 配置运行参数；上线前可执行一次“完整验证”。完整验证会真实创建、执行并销毁一个沙箱。
6. 保存后，在智能体 Skills 配置中选择该后端。对配置的修改只影响之后新建的沙箱；已有会话仍固定使用创建时的配置。

## 上线检查

- 模板版本与 WeKnora 版本匹配，且不依赖浮动的 `latest`；
- WeKnora 到控制面、Proxy 和 sandbox domain 的 DNS、TLS 与防火墙均已打通；
- 多实例部署已配置 Redis；
- API Key 只通过密钥管理或加密配置保存；
- “连接验证”和“完整验证”均通过；
- 已配置运行中与 paused 沙箱的容量监控、费用监控和孤儿清理；
- 切换模板或集群前已确认旧会话和旧沙箱的回收策略。
