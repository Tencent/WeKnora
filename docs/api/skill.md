# Skills API

[返回目录](./README.md)

| 方法 | 路径      | 描述               |
| ---- | --------- | ------------------ |
| GET  | `/skills` | 获取预装 Skills 列表 |
| POST | `/sandbox-configs/{id}/skills` | 安装技能（zip 上传或托管平台 source） |

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
