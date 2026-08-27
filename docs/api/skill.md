# Skills API

[返回目录](./README.md)

| 方法 | 路径      | 描述               |
| ---- | --------- | ------------------ |
| GET  | `/skills` | 获取预装 Skills 列表 |
| POST | `/sandbox-configs/{id}/skills` | 安装技能（zip 上传或托管平台 source） |
| POST | `/sandbox-configs/{id}/skills/{skillId}/reinstall` | 用已保存的安装包重试安装 |
| GET  | `/sandbox-configs/{id}/skills/{skillId}/files` | 列出已安装技能的文件 |
| GET  | `/sandbox-configs/{id}/skills/{skillId}/files/content` | 读取已安装技能中的单个文件 |
| PATCH | `/sandbox-configs/{id}/skills/{skillId}` | 启用/停用技能，或设置空间级环境变量 |
| GET  | `/me/env-vars` | 列出自己的环境变量与各技能的声明 |
| PUT  | `/me/env-vars/skill` | 设置自己在某个技能上的变量值 |
| DELETE | `/me/env-vars/skill` | 删除自己在某个技能上的变量值 |
| PUT  | `/me/env-vars/sandbox` | 设置自己在某个沙箱配置上的变量值 |
| DELETE | `/me/env-vars/sandbox` | 删除自己在某个沙箱配置上的变量值 |

## GET `/skills` - 获取预装 Skills 列表

获取系统中所有预装的智能体技能列表。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/skills' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "name": "web_search",
            "description": "搜索互联网获取最新信息"
        },
        {
            "name": "code_interpreter",
            "description": "执行代码并返回结果"
        },
        {
            "name": "image_generation",
            "description": "根据文本描述生成图片"
        }
    ],
    "skills_available": true,
    "success": true
}
```

当系统未配置 Skills 时，`skills_available` 返回 `false`，`data` 为空数组：

```json
{
    "data": [],
    "skills_available": false,
    "success": true
}
```

## POST `/sandbox-configs/{id}/skills` - 安装技能

把技能安装到指定沙箱配置的镜像上。安装会启动沙箱并运行数分钟，本接口只负责受理，随后通过
`GET /sandbox-configs/{id}/skills/{skillId}/install-events` 跟随进度。

两种请求体二选一：

### 1. 上传 zip（multipart）

```curl
curl --location 'http://localhost:8080/api/v1/sandbox-configs/{id}/skills' \
--header 'X-API-Key: sk-xxxxx' \
--form 'file=@"skill.zip"'
```

### 2. 从托管平台安装（JSON）

`source` 只接受一种明确写法，不会根据下载结果猜测：

| 输入 | 含义 |
| --- | --- |
| `@owner/slug`、`@owner/slug@1.2.0` | ClawHub（默认 registry） |
| `my-skill`、`my-skill@1.2.0`（不含 `/`） | ClawHub slug |
| `https://clawhub.ai/...`、`https://skillhub.cn/...`、自托管 SkillHub 页面 | 对应 registry |
| `https://github.com/...`、`https://gitlab.com/...`、`https://skills.sh/...` | Git 托管 |
| `https://…/foo.zip` 或 `…/SKILL.md` | 直接下载 |

`owner/slug`（无 `@`、无 URL）会 400：它既是 ClawHub id 也是 GitHub 仓库，请改成 `@owner/slug` 或粘贴完整链接。

来源必须可匿名读取：服务端不会为这次下载附带任何凭据，因此私有仓库/私有 registry 需要先自行导出 zip 再上传。

```curl
curl --location 'http://localhost:8080/api/v1/sandbox-configs/{id}/skills' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{"source":"@owner/slug"}'
```

**响应**（202）:

```json
{
    "success": true,
    "data": {
        "skill_id": "..."
    }
}
```

## POST `/sandbox-configs/{id}/skills/{skillId}/reinstall` - 重试安装

用服务端已保存的安装包重新跑一遍安装，无需重新上传 zip 或重新提供 source。适用于安装失败的原因与安装包本身无关的情况：沙箱不可达、依赖源超时、安装过程被中断等。

与安装接口一样只负责受理，进度同样通过
`GET /sandbox-configs/{id}/skills/{skillId}/install-events` 跟随。技能会复用同一个 `skill_id`，不会产生新记录。

已经在当前镜像中正常服务、且安装包未变的技能会被跳过，不会重复构建快照。若该技能的安装包已不在存储中，返回 400，此时只能重新上传。

```curl
curl --location --request POST \
'http://localhost:8080/api/v1/sandbox-configs/{id}/skills/{skillId}/reinstall' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（202）:

```json
{
    "success": true,
    "data": {
        "skill_id": "..."
    }
}
```

## GET `/sandbox-configs/{id}/skills/{skillId}/files` - 列出技能文件

返回该技能存档里的文件路径与大小。路径相对技能根目录（`SKILL.md` 所在目录），不启动沙箱。

```curl
curl --location 'http://localhost:8080/api/v1/sandbox-configs/{id}/skills/{skillId}/files' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": [
        { "path": "SKILL.md", "size": 412 },
        { "path": "scripts/extract.py", "size": 1280 }
    ]
}
```

## GET `/sandbox-configs/{id}/skills/{skillId}/files/content` - 读取技能文件

`path` 为技能根目录相对路径。文本以 UTF-8 返回；较小的图片为 base64；其它二进制文件不返回正文，并设置 `binary: true`。

```curl
curl --location 'http://localhost:8080/api/v1/sandbox-configs/{id}/skills/{skillId}/files/content?path=SKILL.md' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "path": "SKILL.md",
        "size": 412,
        "encoding": "utf-8",
        "media_type": "text/markdown",
        "content": "---\nname: pdf-tools\n..."
    }
}
```

## 技能的环境变量

技能安装时会声明自己需要哪些环境变量（名称、说明、是否必填）。变量的**值**分两层，执行时按「本人的值 → 空间级值」的顺序取，取不到必填项则拒绝执行：

| 层级 | 谁能写 | 作用范围 | 接口 |
| --- | --- | --- | --- |
| 空间级 | Admin+ | 该空间所有人 | `PATCH /sandbox-configs/{id}/skills/{skillId}` |
| 个人级 | 任何登录成员 | 仅本人 | `PUT /me/env-vars/skill` |

任何接口都**不会**回读已保存的值，只返回 `unset` / `workspace` / `user` 三种状态。清空一个值用 DELETE，而不是写入空字符串——「没填」和「不需要」是两种状态。

个人级的值按**调用身份**存放，而不是按用户 ID。用 API Key 驱动的调用与网页登录是不同身份：在网页 Settings 里填的值不会作用于 API Key 发起的执行，反之亦然。集成方若通过 API Key 运行需要凭据的技能，请让管理员配置空间级值。

### PATCH `/sandbox-configs/{id}/skills/{skillId}` - 更新技能

`enabled` 与 `envs` 都是可选的，可只送其一，但不能都不送（400）。`envs` 只写技能已声明的名称，未声明的名称会被忽略而不是报错；值为空字符串表示清空该值并保留声明。

```curl
curl --location --request PATCH \
'http://localhost:8080/api/v1/sandbox-configs/{id}/skills/{skillId}' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{"enabled":true,"envs":{"TAVILY_API_KEY":"tvly-xxxxx"}}'
```

### GET `/me/env-vars` - 列出自己的环境变量

按沙箱配置分组，返回本人的配置级变量，以及该配置下每个已启用技能声明的变量。`source` 表示这次执行实际会用哪一层的值。

```curl
curl --location 'http://localhost:8080/api/v1/me/env-vars' \
--header 'Authorization: Bearer <token>'
```

**响应**:

```json
{
    "success": true,
    "data": [
        {
            "sandbox_config_id": "cfg-1",
            "sandbox_config_name": "默认沙箱",
            "description": "日常对话用的沙箱",
            "vars": [
                { "name": "HTTP_PROXY", "source": "user", "updated_at": "2026-08-27T10:00:00Z" }
            ],
            "skills": [
                {
                    "skill_id": "sk-1",
                    "skill_name": "web-search",
                    "description": "通过 Tavily 检索网页",
                    "vars": [
                        { "name": "TAVILY_API_KEY", "description": "Tavily 搜索密钥", "required": true, "source": "workspace" },
                        { "name": "REGION", "source": "unset" }
                    ]
                }
            ]
        }
    ]
}
```

### PUT / DELETE `/me/env-vars/skill` - 设置或删除自己在技能上的值

`name` 必须是该技能声明过的名称，否则 400。删除后该技能重新回落到空间级值（若有）。

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/me/env-vars/skill' \
--header 'Authorization: Bearer <token>' \
--header 'Content-Type: application/json' \
--data '{"skill_id":"sk-1","name":"TAVILY_API_KEY","value":"tvly-xxxxx"}'
```

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/me/env-vars/skill' \
--header 'Authorization: Bearer <token>' \
--header 'Content-Type: application/json' \
--data '{"skill_id":"sk-1","name":"TAVILY_API_KEY"}'
```

删除一个本就没设过的值返回 404。

### PUT / DELETE `/me/env-vars/sandbox` - 设置或删除自己的配置级变量

配置级变量不依附于任何技能，名称自定，会注入本人在该沙箱配置上的每一次技能脚本与 shell 命令。`WEKNORA_` 前缀与 `PATH` 等保留名会被拒绝。

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/me/env-vars/sandbox' \
--header 'Authorization: Bearer <token>' \
--header 'Content-Type: application/json' \
--data '{"sandbox_config_id":"cfg-1","name":"HTTP_PROXY","value":"http://127.0.0.1:7890"}'
```
