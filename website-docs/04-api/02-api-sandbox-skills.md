# API 参考：沙箱、技能与个人变量

管理沙箱配置、技能目录、安装任务和个人环境变量。路径均以 `/api/v1` 为前缀；示例中的 `$BASE` 为服务地址，`$TOKEN` 为当前用户的 Bearer token。界面操作步骤见[技能目录与沙箱](../03-features/22-skills-sandbox.md)。

## 权限

| 资源 | 读 | 写 / 检测 |
| --- | --- | --- |
| 沙箱配置列表/详情 | Viewer+；API Key 必须 full-access | Admin+；API Key 必须 full-access |
| 沙箱实例清单、已安装技能及文件 | Admin+ | Admin+；API Key 必须 full-access |
| 技能目录列表 | Viewer+，JWT | 目录增删、安装、包文件浏览需 Admin+；写组的 API Key 必须 full-access |
| `/skills` 可用技能 | Viewer+，JWT | 只读，查询参数 sandbox_config_id |
| `/me/env-vars*` | 已登录的当前调用者 | 仅修改自己，服务端推导身份；无额外管理员门槛，使用 Bearer JWT |

跨空间 ID 不授予访问权。个人变量接口不能替代空间技能管理接口。

## 沙箱配置

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| GET | `/sandbox-configs` | 200 `{success,data:[ConfigResponse],workspace_scripts_disabled}` |
| POST | `/sandbox-configs` | `{name,description?,config}`；201 `{success,data:ConfigResponse}` |
| GET | `/sandbox-configs/:id` | 200 `{success,data:ConfigResponse}` |
| PUT | `/sandbox-configs/:id` | `{name,description?,config}`，name 必填；200 同详情 |
| DELETE | `/sandbox-configs/:id` | 可选 `force=true`；200 `{success:true}` |
| GET | `/sandbox-configs/:id/sandboxes` | 200 `{success,data:SandboxInventory}`，包含占用和关联智能体 |
| PUT | `/sandbox-configs/workspace-policy` | `{"scripts_disabled":true}`；返回 success/workspace_scripts_disabled |
| POST | `/sandbox-configs/templates/query` | `{config,config_id?,ensure_standard?,replace_standard?}`；查询远端模板，可创建/替换标准模板 |

ConfigResponse 为 `{id,name,description,sandbox_type,config,created_at,updated_at}`，凭据脱敏。编辑时以保存的配置为基础更新；脱敏占位值保留旧凭据，`skill_image` 由安装服务维护，客户端不能替换。

### config 字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `sandbox_type` | string | docker/cube/e2b；local 已移除 |
| `default_timeout_sec` | int | 执行超时，0 使用内置默认 |
| `allow_private_endpoints` | bool | 允许私网集群地址，不放行 link-local/云元数据 |
| `env_vars` | map[string]string | 该沙箱配置的环境，值加密保存 |
| `skill_rollout` | string | next_turn（默认）/new_session |
| `network` | object | Cube/E2B 网络策略，见下 |
| `cube` / `e2b` / `docker` | object | 与 sandbox_type 对应的连接配置 |
| `volume_mount` | object | 可选卷配置；后端能力决定是否生效，不能仅凭字段存在认定支持 |
| `skill_image` | object | 安装服务维护的快照信息，只读 |

volume_mount 字段包括 enabled、mount_path、provider、volume_id、volume_name；volume_owner_fingerprint 由服务端管理。卷配置与后端支持能力共同决定是否挂载，编辑时保留服务端返回的归属信息。

后端配置：

| 后端 | 字段 |
| --- | --- |
| cube | api_url、proxy_url、sandbox_domain、template_id；api_key 按集群需要；http_timeout_sec、cube_sandbox_ttl_seconds、dns_servers |
| e2b | api_key、template_id 必填；api_url、sandbox_domain、proxy_url 用于自托管；http_timeout_sec、e2b_sandbox_ttl_seconds |
| docker | image 必填；host（空为本机 socket）、tls_cert_path（TCP 必需）、cpu_limit、memory_limit_mb、pids_limit、network_mode（bridge/none）、runtime、idle_ttl_seconds、http_timeout_sec |

```bash
curl -X POST "$BASE/api/v1/sandbox-configs" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"E2B 工作环境","config":{"sandbox_type":"e2b","e2b":{"api_key":"<e2b-key>","template_id":"<template-id>"}}}'

curl "$BASE/api/v1/sandbox-configs/cfg-1/sandboxes" \
  -H "Authorization: Bearer $TOKEN"
```

修改后端身份和删除配置会检查远端实例。409 的 error.code 可为 `sandboxes_still_live`、`sandbox_inventory_unverifiable`、`skill_snapshot_release_failed` 等；423 表示配置正在被另一个请求修改。`force=true` 仅允许在库存无法核实时删除，不会跳过已确认的活跃/暂停实例。响应中的占用数据用于定位相关会话和智能体。

### 网络策略

`network`：

| 字段 | 说明 |
| --- | --- |
| `deny_egress_by_default` | false 默认允许出站；true 默认拒绝 |
| `allow_out` | IPv4/CIDR/域名或单标签通配域名，域名用于默认拒绝模式 |
| `deny_out` | IPv4/CIDR 拒绝清单 |
| `cube_rules` | Cube 的 name/scheme/sni/host/methods/path/deny/audit/inject 规则，顺序有意义 |
| `e2b_host_rules` | E2B 的 host/headers 规则；host 也须列入 allow_out |
| `allow_public_inbound` | 兼容旧输入，保存时清除，入站始终要求凭据 |

`cube_rules[].inject` 为 `[{header,secret,format}]`，`e2b_host_rules[].headers` 为 header→secret，秘密字段加密保存并脱敏。Docker 用 `docker.network_mode`，不支持这些 L7 规则。

```json
{
  "deny_egress_by_default": true,
  "allow_out": ["pypi.org", "files.pythonhosted.org"],
  "deny_out": []
}
```

### POST /system/sandbox-check

请求 `{config,config_id?,deep?}`；config_id 可用于恢复已保存的脱敏凭据。`deep=false` 做连接检查；true 还执行临时脚本，远端后端会实际创建并销毁沙箱，可能产生后端用量。

成功执行探测返回 `{success:true,data:{ok,provider,checks,capabilities}}`，探测失败也可为 HTTP 200，应检查 data.ok；参数错误返回 400 的 code/msg。每个 check 含 name/ok/message/reason/latency_ms，`ok=null` 表示跳过。例如默认拒绝出网时外网探针被策略跳过，不等于通过外网访问验证。

```bash
curl -X POST "$BASE/api/v1/system/sandbox-check" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"config_id":"cfg-1","deep":true}'
```

## 技能目录

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| GET | `/skills/catalog` | 200 `{success,data:[CatalogItem]}` |
| POST | `/skills/catalog` | multipart `file` 或 JSON `{"source":"@owner/slug"}`；201 `{success,data:{id,name,version,description}}` |
| POST | `/skills/catalog/:id/install` | `{"sandbox_config_ids":["cfg-1","cfg-2"]}`；202 `{success,data:{installs,errors?}}` |
| GET | `/skills/catalog/:id/files` | 200 `{success,data:[FileEntry]}` |
| GET | `/skills/catalog/:id/files/content?path=SKILL.md` | 200 `{success,data:FileContent}` |
| DELETE | `/skills/catalog/:id` | 无安装引用时删除；200 `{success:true}` |

安装到多沙箱可能部分受理：检查 `data.installs` 和 `data.errors`，HTTP 202 不表示全部安装完成。目录删除不会隐式卸载各沙箱。

```bash
curl -X POST "$BASE/api/v1/skills/catalog" \
  -H "Authorization: Bearer $TOKEN" -F 'file=@skill.zip'
curl -X POST "$BASE/api/v1/skills/catalog/catalog-1/install" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"sandbox_config_ids":["cfg-1"]}'
```

来源写法、匿名下载要求和 ZIP 限制见[技能来源](../03-features/22-skills-sandbox.md#支持的来源)。

## 沙箱内技能

以下路径前缀是 `/sandbox-configs/:id/skills`，所有操作含读都需 Admin+。

| 方法 | 后缀 | 行为 |
| --- | --- | --- |
| GET | 空 | `{success,data:[SkillResponse]}` |
| POST | 空 | ZIP file 或 source JSON 安装；202 `{success,data:{skill_id}}` |
| GET | `/:skillId` | `{success,data:SkillResponse}` |
| POST | `/:skillId/reinstall` | 复用包重装；202 `{success,data:{skill_id}}` |
| POST | `/:skillId/stop` | 停止安装；200 返回技能状态，已 failed 可重复调用，其他非 installing 状态拒绝 |
| PATCH | `/:skillId` | `enabled?`、`envs?`；省略不修改 |
| DELETE | `/:skillId` | 从此沙箱卸载，保留目录包 |
| GET | `/:skillId/files` | 文件清单 |
| GET | `/:skillId/files/content?path=SKILL.md` | 读取相对路径文件，拒绝穿越 |
| GET | `/:skillId/install-events` | SSE 安装进度 |
| GET | `/:skillId/transcript` | SSE 安装过程事件 |

SkillResponse 包括 id/name/version/description/enabled/status/error/bundle_sha256/installed_snapshot_id/install_session_id/install_message_id/created_at/updated_at，以及 `envs:[{name,description,required,is_set}]`。文件响应可为 UTF-8 文本、小图片的 base64 或二进制元数据，不能假定所有文件都有文本内容。

PATCH 的 envs 只更新已声明变量；未声明名称忽略，空字符串清除值、保留声明。

```bash
curl -X PATCH "$BASE/api/v1/sandbox-configs/cfg-1/skills/skill-1" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"envs":{"API_TOKEN":"<workspace-token>"}}'

curl -N "$BASE/api/v1/sandbox-configs/cfg-1/skills/skill-1/install-events" \
  -H "Authorization: Bearer $TOKEN"
```

进度载荷为 `{percent,stage,log?,status?,done}`。断开连接不取消安装；done 可能只是无法继续跟随进度，最终结果以技能 status 为准。无 Redis 时流提示改为轮询状态。transcript 的 204 表示安装刚开始、事件定位尚未准备；404 可表示无日志或日志已过期，不能据此判定安装失败。

`GET /skills?sandbox_config_id=cfg-1` 返回可用于智能体的技能元数据 `{success,data:[{name,description}],skills_available}`，与管理员看到的全部安装记录不同。

## 个人环境变量

调用者身份由当前认证上下文确定，不接收 user_id。个人接口返回变量名、来源及更新时间，不返回明文。

| 方法 | 路径 | 请求 |
| --- | --- | --- |
| GET | `/me/env-vars` | 返回按 sandbox_config_id 分组的配置变量与技能变量 |
| PUT | `/me/env-vars/skill` | `{skill_id,name,value}`，名称须为技能已声明项 |
| DELETE | `/me/env-vars/skill` | `{skill_id,name}` |
| PUT | `/me/env-vars/sandbox` | `{sandbox_config_id,name,value}` |
| DELETE | `/me/env-vars/sandbox` | `{sandbox_config_id,name}` |

读取为 `{success,data:[{sandbox_config_id,sandbox_config_name,description,vars,skills}]}`，source 标明 user/workspace/unset 等状态。设置/删除成功返回 success；删未设置项返回 404。个人沙箱变量拒绝 WEKNORA_ 前缀和 PATH 等保留名；技能声明变量可使用所需的 WEKNORA_* 凭据名。

```bash
curl -X PUT "$BASE/api/v1/me/env-vars/skill" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"skill_id":"skill-1","name":"API_TOKEN","value":"<my-token>"}'

curl -X DELETE "$BASE/api/v1/me/env-vars/skill" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"skill_id":"skill-1","name":"API_TOKEN"}'
```

覆盖顺序为个人技能值 > 个人沙箱值 > 空间技能值。写入个人值不会更改管理员配置。实现参考：`internal/router/routes_infra.go`、`routes_agent.go`、`routes_auth_tenant.go`，以及 `internal/handler/sandbox_config.go`、`sandbox_skill.go`、`skill_catalog.go`、`me_env_var.go`。
