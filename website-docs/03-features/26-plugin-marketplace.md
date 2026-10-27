# 插件市场

插件市场把一组技能和 MCP 服务打成一份目录，一次导入后按插件安装。空间管理员在侧边栏「工具箱 → 插件」里添加或删除市场。同一空间里的成员，包括查看者，都能看到这些市场，并自己选择要安装、改沙箱或卸载的插件。安装后的技能写入[技能目录](22-skills-sandbox.md)，MCP 服务写入[MCP 服务](08-mcp.md)表，供智能体按原有的技能范围和 MCP 勾选调用。这两类组件不出现在「技能管理」和「MCP 服务」列表里，也不能从那两页单独删除。卸载只在插件详情里进行。

## 添加市场并安装插件

1. 管理员打开「工具箱 → 插件」，点「添加市场」。
2. 选择一种来源：
   - **文件夹**：选市场根目录，浏览器在本地打包后上传。
   - **压缩包**：`.zip`、`.tar.gz`、`.tgz` 或 `.tar`。压缩包本身和解压后的内容上限都是 512 MiB，单个文件不超过 32 MiB。
   - **Git 地址**：`owner/repo`、GitHub / GitLab 仓库页面，或一个可直接下载的压缩包链接。下载是匿名的，只接受公开地址，并经过 SSRF 校验。
3. 导入成功后，列表里是这个市场里可以安装的插件。每个插件显示技能数量、MCP 数量，以及是否需要沙箱。
4. 空间成员点「安装」。带技能的插件要先有沙箱。Lite 桌面版直接装到本机，不用挑选沙箱；其他部署勾选要写入的沙箱。配置沙箱本身是管理员的事，成员只能在已有沙箱里选择。清单里声明的环境变量在这一步填写，值加密保存在工作区。安装技能用的模型由管理员在安装时选定，成员不改这个模型。还没有保存过、或保存的模型已不可用时，用工作区的默认对话模型。
5. MCP 会立刻创建。技能在后台装进所选沙箱，装完后状态变为「已安装」。探测到需要 OAuth、且该服务还没有静态凭据时，状态停在「待授权」。在插件详情里点「授权」，在 600×720 的小窗里完成登录；授权成功后详情页会更新状态。已经配了静态凭据的服务不会自动改成 OAuth。同一个插件名不能再从另一个市场安装。
6. 安装完成后打开插件详情，可以看到这个插件包含的技能和 MCP。任何成员都可以重新检测 OAuth、调整沙箱或卸载。沙箱不能全部取消，要拿掉全部沙箱请直接卸载。管理员还可以点技能名称进入该技能在沙箱上的安装详情（与技能管理里打开某一项相同），用文件夹按钮查看技能文件，点 MCP 名称进入该服务的配置页。

同名市场再次导入时，当前工作区改绑到新的压缩包。已经装上的插件仍留在原来的版本，卡片上会提示有新版本；要换上新内容，先卸载再安装。新压缩包如果少了本工作区已经安装的插件，这次导入会失败，需要先卸载那个插件。压缩包字节完全相同则不会改写。另一个工作区导入过同一份旧压缩包时，仍保留它自己的那一份。市场里还有已安装的插件时不能删除市场，先卸载插件。

卸载在插件详情里进行。MCP 服务马上移除，技能文件在后台从沙箱删掉。

## 一份 WeKnora 市场要包含什么

市场根目录放 `.weknora-plugin/marketplace.json`，每个插件放在它声明的相对路径下。插件至少要有一个可安装的技能，或一个 HTTP / SSE MCP 服务。只有 README、斜杠命令、hooks，或只声明了 stdio MCP 的条目会被跳过；全部被跳过时，这次导入失败。

```text
my-plugins/
  .weknora-plugin/
    marketplace.json
  plugins/
    docs-helper/
      .weknora-plugin/plugin.json
      .mcp.json
      skills/
        docs-helper/
          SKILL.md
```

`marketplace.json`：

```json
{
  "name": "my-plugins",
  "owner": { "name": "Acme", "email": "plugins@acme.example" },
  "metadata": {
    "description": "文档助手插件",
    "version": "1.0.0"
  },
  "plugins": [
    {
      "name": "docs-helper",
      "source": "./plugins/docs-helper",
      "description": "查阅内部文档并回答问题",
      "version": "1.0.0",
      "category": "productivity",
      "keywords": ["docs"]
    }
  ]
}
```

- `name` 是市场名称，也是安装后 MCP 服务名的前缀，写成 `<市场名>__<服务名>`。用简短、稳定的名字。
- `plugins[].source` 是压缩包内的相对路径，例如 `./plugins/docs-helper`。也可以写成 `{"source":"local","path":"./plugins/docs-helper"}`。路径不能含 `..`。
- `metadata.pluginRoot` 存在时，不带斜杠的 source 会接到这个目录后面。
- `description`、`version`、`category`、`keywords` 可选。说明和关键词用于插件列表里的搜索。

`plugin.json` 可以省略。没有它时，插件名用来源目录名，技能仍从 `skills/` 读取。有它时，其中的 `name` 会覆盖市场清单里的插件名。

```json
{
  "name": "docs-helper",
  "version": "1.0.0",
  "description": "查阅内部文档并回答问题",
  "author": { "name": "Acme", "url": "https://acme.example" },
  "license": "Apache-2.0",
  "keywords": ["docs"],
  "skills": "./skills/",
  "mcpServers": "./.mcp.json",
  "env": [
    { "name": "DOCS_API_TOKEN", "description": "文档服务的访问令牌", "required": true }
  ]
}
```

插件名以字母或数字开头，之后可以是字母、数字、连字符、下划线和点，最长 48 字节。`..` 和 `__` 不能出现，因为 `__` 用来分隔插件名和技能名。

### 技能

默认读取插件目录下的 `skills/<技能名>/SKILL.md`。`plugin.json` 的 `skills` 可以写成一个路径或路径数组，指向插件目录内的其他技能目录。每个技能目录里要有 `SKILL.md`，开头是 YAML frontmatter：

```markdown
---
name: docs-helper
description: 按用户的问题查阅内部文档并给出带来源的回答。
---

# 文档助手

先确认要查的文档范围，再调用文档 MCP 检索。
```

- `name` 和 `description` 都必填。名称由字母、数字、连字符、下划线和点组成，不能含 `..`，最长 64 个字符；说明最长 1024 个字符。
- 安装后的技能名是 `<插件名>__<技能目录名>`，并写回 `SKILL.md` 开头的 `name`。两段加起来不超过 64 字节。
- 技能目录里的脚本、模板和资料会一起打包。带技能的插件必须装进沙箱（或 Lite 的本机沙箱）才能给智能体用。

### MCP

默认读取插件目录下的 `.mcp.json`，其次是 `mcp.json`。`plugin.json` 的 `mcpServers` 可以是这两个文件之外的相对路径、一份内联的服务表，或把路径和内联服务混在一个数组里。后出现的同名服务覆盖先出现的。

```json
{
  "mcpServers": {
    "docs": {
      "type": "http",
      "url": "https://docs.example.com/mcp",
      "headers": { "Authorization": "Bearer ${DOCS_API_TOKEN}" }
    }
  }
}
```

- 传输用 `http`、`streamable-http` 或 `sse`。`streamable_http`、`http-streamable` 也按 HTTP 处理。没写 `type` 但写了 `url` 时按 HTTP 处理。
- `command` / `args` 或 `type: stdio` 的服务不会安装。插件里还有技能或其他 HTTP / SSE 服务时，插件仍会导入，stdio 那一项记在说明里。
- URL 里不能写 `${变量}`。请求头里最多一个变量，值只能是 `${NAME}`，或 `Authorization` 头上的 `Bearer ${NAME}`。
- 变量名是大写蛇形，例如 `DOCS_API_TOKEN`，最长 128 个字符。沙箱保留的名字不能用。`plugin.json` 的 `env` 里没声明、但请求头用到的变量，会在安装时作为必填项询问。

## 其他市场清单

同一份压缩包里也可以是 Claude、Cursor、Copilot 或 Codex 的市场清单。WeKnora 把它转成上面的内部结构，只保留压缩包里已经带有文件、并且含技能或 HTTP / SSE MCP 的插件。

按目录从浅到深查找；同一层有多份清单时按这个顺序采用，前一份无法解析或解析后没有可安装插件时，再试下一份：

| 顺序 | 清单 |
| --- | --- |
| 1 | `.weknora-plugin/marketplace.json` |
| 2 | `.claude-plugin/marketplace.json` |
| 3 | `.cursor-plugin/marketplace.json` |
| 4 | `.github/plugin/marketplace.json` |
| 5 | `.agents/plugins/marketplace.json` |

单个插件的清单同样按渠道依次尝试：`.weknora-plugin/plugin.json`、`.claude-plugin/plugin.json`、`.cursor-plugin/plugin.json`、`.codex-plugin/plugin.json`、根目录 `plugin.json`，以及 Gemini 的 `gemini-extension.json`。符号链接不会被跟随，会直接跳过。

清单里 `source` 指向 GitHub、Git、npm、压缩包 URL 或命令的插件不在这份压缩包里，导入时跳过并提示数量。压缩包里没有任何可安装插件时，导入失败。

没有市场清单也可以导入。压缩包如果只有一层目录，会先剥掉这一层。根目录本身是一个插件（有 `plugin.json`、`gemini-extension.json`、`.mcp.json` / `mcp.json`，或 `skills/<名>/SKILL.md`）时，整包当作只含这一个插件的市场；否则只检查下一层子目录，把其中的插件目录收成一个市场。
