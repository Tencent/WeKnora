# Agent Skills 文档

## 概述

Agent Skills 是一种让 Agent 通过阅读"使用说明书"来学习新能力的扩展机制。与传统的硬编码工具不同，Skills 通过注入到 System Prompt 来扩展 Agent 的能力，遵循 **Progressive Disclosure（渐进式披露）** 的设计理念。
目前仅支持带**智能推理**能力的智能体使用。前端可在智能体的编辑页面找到相关配置

### 核心特性

- **非侵入式扩展**：不影响原有 Agent ReAct 流程
- **按需加载**：三级渐进式加载，优化 Token 使用
- **沙箱执行**：脚本在隔离环境中安全执行
- **灵活配置**：支持多目录、白名单过滤

## 设计理念

### Progressive Disclosure（渐进式披露）

Skills 采用三级加载机制，确保只在需要时才向 LLM 提供详细信息：

```
┌─────────────────────────────────────────────────────────────────┐
│ Level 1: 元数据 (Metadata)                                      │
│ • 始终加载到 System Prompt                                       │
│ • 约 100 tokens/skill                                           │
│ • 包含：技能名称 + 简短描述                                       │
└─────────────────────────────────────────────────────────────────┘
                              ↓ 用户请求匹配时
┌─────────────────────────────────────────────────────────────────┐
│ Level 2: 指令 (Instructions)                                    │
│ • 通过 read_skill 工具按需加载                                   │
│ • SKILL.md 的指令内容                                           │
│ • 包含：详细指令、代码示例、使用方法                               │
└─────────────────────────────────────────────────────────────────┘
                              ↓ 需要更多信息时
┌─────────────────────────────────────────────────────────────────┐
│ Level 3: 附加资源 (Resources)                                   │
│ • 通过 read_skill 工具加载特定文件                               │
│ • 补充文档、配置模板、脚本文件                                    │
│ • 通过 execute_skill_script 执行脚本                            │
└─────────────────────────────────────────────────────────────────┘
```

## Skill 目录结构

每个 Skill 是一个目录，包含 `SKILL.md` 主文件和可选的附加资源：

```
my-skill/
├── SKILL.md           # 必需：主文件（含 YAML frontmatter）
├── REFERENCE.md       # 可选：补充文档
├── templates/         # 可选：模板文件
│   └── config.yaml
└── scripts/           # 可选：可执行脚本
    ├── analyze.py
    └── generate.sh
```

## SKILL.md 格式

### YAML Frontmatter

每个 `SKILL.md` 必须以 YAML frontmatter 开头，定义元数据：

```markdown
---
name: pdf-processing
description: Extract text and tables from PDF files, fill forms, merge documents. Use when working with PDF files or when the user mentions PDFs, forms, or document extraction.
---

# PDF Processing

This skill provides utilities for working with PDF documents.

## Quick Start

Use pdfplumber to extract text from PDFs:

```python
import pdfplumber

with pdfplumber.open("document.pdf") as pdf:
    text = pdf.pages[0].extract_text()
    print(text)
```

## 元数据验证规则

| 字段 | 要求 |
|------|------|
| `name` | 1-50 字符，仅允许汉字、英文字母、数字，不能是保留词 |
| `description` | 1-500 字符，描述技能用途和触发条件 |

**保留词**：`system`, `default`, `internal`, `core`, `base`, `root`, `admin`


## 配置

### AgentConfig 配置项

```go
type AgentConfig struct {
    // ... 其他配置 ...

    // Skills 相关配置
    SkillsEnabled  bool     `json:"skills_enabled"`   // 是否启用 Skills
    SkillDirs      []string `json:"skill_dirs"`       // Skill 目录列表
    AllowedSkills  []string `json:"allowed_skills"`   // 白名单（空=全部允许）
}
```

### 配置示例

```json
{
  "skills_enabled": true,
  "skill_dirs": [
    "/path/to/project/skills",
    "/home/user/.agent-skills"
  ],
  "allowed_skills": ["pdf-processing", "code-review"]
}
```

### Sandbox 配置入口

Sandbox 不再把凭据和模板放进 `WEKNORA_SANDBOX_*`。后端、凭据、模板、执行超时、TTL 和私网访问策略均在「设置 → 沙箱后端」按空间保存；智能体没有选择空间配置时，脚本执行保持禁用。Docker 后端默认关闭，由系统管理员在「设置 → 系统设置」打开，或设置 `WEKNORA_SANDBOX_DOCKER_ENABLED=true`。

### Sandbox 模式

Docker、CubeSandbox、E2B 均通过同一套空间配置 CRUD、连接检查和智能体选择接口管理。CubeSandbox / E2B 的集群搭建和设置页接入流程见 [WeKnora 沙箱集群与标准模板](sandbox-cluster.md)。设置页会通过当前连接拉取模板目录；若没有 WeKnora 标准模板，后端会从标准镜像发起创建，用户无需复制模板 ID。

| 模式 | 状态 | 说明 |
|------|------|------|
| `docker` | 稳定 | 单机 Docker daemon；会话级持久（一个会话一个长驻容器），支持多机 WeKnora 副本（需 Redis），但沙箱都落在同一台 daemon 上。见 [Docker 沙箱后端](sandbox-docker-backend.md) |
| `cube` | 稳定 | Tencent CubeSandbox MicroVM；会话级持久，支持多机（需 Redis） |
| `e2b` | 稳定 | E2B 云端 MicroVM；会话级持久，支持多机（需 Redis）；依赖第三方 SDK go-e2b |

### 工作区沙箱后端配置

一个工作区可以维护**多份具名**沙箱后端配置（「设置 → 沙箱后端」），智能体在编辑弹窗的「能力扩展 → 沙箱后端」里各自选一份。不选表示禁用脚本执行。

同一后端类型可以有多份配置：例如两份 E2B 分别指向不同账号或区域，让不同智能体的技能脚本落在不同配额上。

**空间配置是唯一运行时来源。** 端点、凭据和运行参数不会从 `.env` 回退：

| 后端 | 必填 | 可留空 |
|---|---|---|
| Cube | API 端点、Proxy 端点、沙箱域名、从集群列表选择的模板 | API Key（自建部署通常无鉴权） |
| E2B | API Key、从账号列表选择的模板 | API 端点、沙箱域名（go-e2b 自行解析默认值） |

留空必填项在保存时就会被拒绝（HTTP 400）。HTTP 超时、沙箱 TTL 和执行超时留空均使用程序内置默认值。

这条规则换来的是：库里那一行就是沙箱位置的完整描述。因此身份比较不必再去解析 `.env`，改 `.env` 也不会在无人察觉的情况下把某份配置重新指向别的账号。

**会话与配置的绑定是「随沙箱同生共死」的钉子。** 会话首次创建沙箱时，把当时用的配置 ID 记在 `sessions.sandbox_config_id` 上；此后该会话的附件上传、产物收集、沙箱销毁都锁定在这份配置上。改智能体的选择**只影响之后新建的沙箱**——否则管理员改一次配置，正在进行的会话就会去错误的账号里找产物，销毁也会打空，留下一个没人知道 ID 的 paused 沙箱持续计费。

### 安装租户技能

空间「技能沙箱」设置里可以把技能装进当前配置的镜像。除上传 zip 外，也支持从托管平台粘贴来源（每种写法只对应一种来源，不会猜测）：

- ClawHub：`@owner/slug`，或不含 `/` 的 slug（如 `my-team--skill`）
- ClawHub skills.sh 联邦页：`https://clawhub.ai/skills-sh/owner/repo/slug` 或 `skills-sh:owner/repo/slug`（服务端向 ClawHub 解析钉死的 GitHub commit，不会把 URL 最后一段当成仓库子目录）
- 页面链接：ClawHub / [skillhub.cn](https://skillhub.cn) / 自托管 SkillHub、skills.sh、GitHub、GitLab
- 直接的 zip / `SKILL.md` URL

不要粘贴裸的 `owner/slug`：请改成 `@owner/slug` 或完整 `https://github.com/...` 链接。来源必须可匿名读取，服务端下载时不携带任何凭据；私有仓库请先导出 zip 再上传。安装仍走原有镜像快照流程。

**有沙箱在跑时改不了身份字段。** 身份字段分两组，成因不同但后果都足够严重：

| 组 | 字段 | 一改会怎样 |
|---|---|---|
| 控制面 | 后端类型、API 端点、API Key | 旧沙箱**再也无法列举/删除/恢复**——新凭据没有权限动它们，而 `onTimeout=pause` 意味着 TTL 也不会回收|
| 数据面 | E2B 沙箱域名；Cube 代理地址、沙箱域名 | 旧沙箱仍可删，但 envd 请求会打到错的主机 ⇒ 该配置下**所有活会话立刻失效**，而沙箱还活着继续计费 |

因此这类修改会被拒绝（HTTP 409），界面会给出沙箱数量、受影响会话数，以及两条出路：**结束或删除那些会话**（删会话会销毁其沙箱），或者**新建一份配置**把智能体指过去（旧凭据原样留着，清理能力不丢）。**没有「释放沙箱」按钮**——那等于在管理员背后销毁正在进行的对话。

**删除配置**只拦远端沙箱，不拦智能体引用：确认弹窗会列出仍指向它的智能体名单，但不阻止删除；删除后那些智能体执行技能时会明确报错，而不是静默换到别的后端。若后端已连不上、无法核实是否仍有沙箱，可以强制删除（这是唯一能强制的情形——能数出来的活沙箱永远不让强删）。

**`sessions.sandbox_config_id` 取值语义：**

| 值 | 含义 |
|---|---|
| `NULL` | 当前无活沙箱（删会话 / 销毁后会 Clear） |
| `"-"` | 旧版本部署默认沙箱的历史兼容标记；新会话不再写入 |
| UUID | 活沙箱建在工作区某份**具名配置**上 |


### 会话级 sandbox 部署要点

- **binding store 自动选择**：进程根据通用 `REDIS_ADDR` 是否配置自动决定绑定存储；Redis key 命名空间复用 `WEKNORA_REDIS_NAMESPACE`，未设置时为 `weknora`。
- **多机部署（生产推荐）**：配置 `REDIS_ADDR`。多副本共享同一 session 的沙箱绑定，通过 Redis SET NX + 可续租分布式锁串行化 create / recover / delete。
- **单机部署**：不配置 `REDIS_ADDR`（或 Lite 模式）时使用进程内内存 binding，仅限单实例。进程重启会丢失 session→sandbox 映射，remote 侧沙箱成为孤儿（注意：**TTL 到期只会暂停、不会销毁**，见下）。
- **切换 provider**：不同 provider 的 sandbox ID 不通用。智能体改选配置只影响之后新建的沙箱，已有沙箱继续按 session pin 回收。
- **⚠️ 孤儿沙箱不会被 TTL 自动回收**：会话沙箱创建时使用 `onTimeout=pause` + `autoResume=true`（见 `buildSessionCreateRequest`），因此 **TTL 到期是"暂停"而非"销毁"**——保留状态本就是 pause 的目的。加上 CAS 换绑会把旧 sandboxID 从 binding store 覆盖掉，被替换的沙箱会变成**无人知晓 ID 的 paused 孤儿**，持续占用快照存储与费用。删除会话（`session.go` 的 destroyer）与 lifecycle 的惰性 orphan cleanup 都覆盖不到这种情况。生产环境需依赖按 metadata 列举并与 binding 对账的清理任务来回收（`internal/sandbox/orphan_reaper.go`），且**必须显式包含 `paused` 状态**。对账维度是 `(tenant_id, config_id)` 而非仅 `tenant_id`：同一工作区的两份配置可能指向**同一个 provider 账号**（例如同一个 E2B Key 只差模板），只按 `tenant_id` 过滤会把另一份配置的沙箱一并误删。
- **网络策略**：`cube` 与 `e2b` 默认开启公网出口和 public traffic，可在 create 时通过 provider-neutral `RemoteNetworkPolicy`（`AllowInternetAccess` / `AllowPublicTraffic` / `AllowOut` / `DenyOut`）精细化配置；两个 adapter 都实现了同一契约。

## Agent 工具

Skills 功能通过两个工具与 Agent 交互：

### read_skill

读取技能内容或特定文件。

**参数**：
```json
{
  "skill_name": "pdf-processing",      // 必需：技能名称
  "file_path": "FORMS.md"              // 可选：相对路径
}
```

**使用场景**：
1. 加载 Level 2 内容：仅传 `skill_name`
2. 加载 Level 3 资源：同时传 `skill_name` 和 `file_path`

**示例调用**：
```json
// 加载技能主内容
{"skill_name": "pdf-processing"}

// 加载补充文档
{"skill_name": "pdf-processing", "file_path": "FORMS.md"}

// 查看脚本内容
{"skill_name": "pdf-processing", "file_path": "scripts/analyze.py"}
```

### execute_skill_script

在沙箱中执行技能脚本。

**参数**：
```json
{
  "skill_name": "pdf-processing",           // 必需：技能名称
  "script_path": "scripts/analyze.py",      // 必需：脚本相对路径
  "args": ["input.pdf", "--format", "json"] // 可选：命令行参数
}
```

**支持的脚本类型**：
- Python (`.py`)
- Shell (`.sh`)
- JavaScript/Node.js (`.js`)
- Ruby (`.rb`)
- Go (`.go`)

## 预加载技能（Preloaded Skills）

系统内置了以下 5 个预加载技能，用于增强知识库问答和文档处理能力：

### 1. citation-generator - 引用生成器

**用途**：自动生成规范引用格式

**触发场景**：
- 需要生成参考文献
- 标注知识库内容出处
- 要求提供引用信息

**核心能力**：
| 功能 | 说明 |
|------|------|
| 来源标注 | 为回答中使用的每个知识点标注来源 |
| 格式化引用 | 支持 APA、MLA、Chicago、简化格式 |
| 参考文献列表 | 在回答末尾生成完整的参考文献列表 |

**简化引用格式示例**：
```
根据公司政策[员工手册2024.pdf, 第15页]，年假申请需提前...
```

---

### 2. data-processor - 数据处理器

**用途**：数据处理与分析

**触发场景**：
- "分析这些数据"、"统计一下"、"计算总数/平均值"
- "转换为 JSON/CSV 格式"
- "提取关键信息"、"整理成表格"
- "生成报告"、"数据汇总"

**核心能力**：
| 功能 | 说明 |
|------|------|
| 数据分析 | 对检索到的文档数据进行统计分析 |
| 格式转换 | JSON/CSV/Markdown 等格式相互转换 |
| 数据提取 | 从非结构化文本中提取结构化信息 |
| 报告生成 | 生成数据分析报告和摘要 |

**可用脚本**：
- `scripts/analyze.py` - 数据分析脚本
- `scripts/format_converter.py` - 格式转换脚本
- `scripts/extract_info.py` - 信息提取脚本

**脚本使用示例**：
```bash
# 数据分析
echo '{"items": [1, 2, 3, 4, 5]}' | python scripts/analyze.py

# 格式转换（JSON 转 CSV）
echo '[{"name": "A", "value": 1}]' | python scripts/format_converter.py --to csv

# 信息提取
echo "2024年销售额为100万元" | python scripts/extract_info.py
```

---

### 3. doc-coauthoring - 文档协作 （源于Claude官方Skill）

**用途**：引导用户完成结构化文档创作

**触发场景**：
- 编写文档："write a doc"、"draft a proposal"、"create a spec"
- 文档类型：PRD、设计文档、决策文档、RFC

**工作流程**：

```
Stage 1: 上下文收集 (Context Gathering)
        ↓
Stage 2: 细化与结构 (Refinement & Structure)
        ↓
Stage 3: 读者测试 (Reader Testing)
```

**三阶段说明**：
| 阶段 | 目标 | 关键活动 |
|------|------|----------|
| Stage 1 | 缩小用户与 Claude 之间的信息差 | 元信息提问、上下文收集、澄清问题 |
| Stage 2 | 逐节构建文档 | 头脑风暴、筛选整理、迭代修改 |
| Stage 3 | 测试文档对读者的效果 | 预测读者问题、子代理测试、修复盲点 |

---

### 4. document-analyzer - 文档分析器

**用途**：深度分析文档结构和内容

**触发场景**：
- 分析文档结构
- 提取关键信息
- 识别文档类型
- 进行内容质量评估

**核心能力**：
| 功能 | 说明 |
|------|------|
| 结构分析 | 识别文档的章节层级、组织架构 |
| 关键信息提取 | 提取核心论点、关键数据、重要结论 |
| 文档类型识别 | 判断文档类型（报告、手册、论文、合同等） |
| 内容质量评估 | 评估文档的完整性、一致性、可读性 |

**分析流程**：
1. **文档概览** - 获取文档基本信息
2. **结构分析** - 识别标题层级、章节组织
3. **内容提取** - 提取核心主题、关键论点、支撑数据
4. **质量评估** - 评估完整性、一致性、清晰度

---

### 技能目录结构

预加载技能位于 `skills/preloaded/` 目录下：

```
skills/preloaded/
├── citation-generator/
│   └── SKILL.md
├── data-processor/
│   ├── SKILL.md
│   └── scripts/
│       ├── analyze.py
│       ├── format_converter.py
│       └── extract_info.py
├── doc-coauthoring/
│   └── SKILL.md
├── document-analyzer/
│   └── SKILL.md
└── summary-generator/
    └── SKILL.md
```

## 创建自定义 Skill

暂时不支持用户自主创建自定义 Skill


## 沙箱安全机制

### 脚本安全校验（Script Validator）

在脚本执行前，系统会进行多层安全校验，拦截潜在的恶意操作：

#### 校验类型

| 类型 | 说明 | 示例 |
|------|------|------|
| **危险命令检测** | 检测可能破坏系统的命令 | `rm -rf /`, `mkfs`, `shutdown`, fork bombs |
| **危险模式匹配** | 正则匹配高危操作模式 | `curl \| bash`, `base64 -d`, `eval()` |
| **网络访问检测** | 检测网络请求尝试 | `curl`, `wget`, `socket.connect`, `requests.get` |
| **反向 Shell 检测** | 检测远程控制后门 | `/dev/tcp/`, `bash -i`, `nc -e` |
| **参数注入检测** | 检测命令行参数中的注入 | `&&`, `\|`, `$()`, 反引号 |
| **Stdin 注入检测** | 检测标准输入中的嵌入命令 | 嵌入的命令替换语法 |

#### 拦截的危险命令

**系统破坏类**：
- `rm -rf /`, `rm -rf /*` - 递归删除根目录
- `mkfs`, `dd if=/dev/zero` - 文件系统/磁盘操作
- Fork bombs: `:(){ :|:& };:`

**系统控制类**：
- `shutdown`, `reboot`, `halt`, `poweroff`
- `killall`, `pkill`
- `systemctl`, `service`

**权限提升类**：
- `chmod 777 /`, `chown root`
- `setuid`, `setgid`, `passwd`
- 访问 `/etc/passwd`, `/etc/shadow`, `/etc/sudoers`

**凭证窃取类**：
- 访问 `.ssh/`, `id_rsa`, `id_ed25519`
- 读取敏感配置文件

**容器逃逸类**：
- `docker`, `kubectl`, `nsenter`
- `unshare`, `capsh`

#### 拦截的危险模式

**代码注入**：
```
# 以下模式会被拦截
curl ... | bash           # 下载并执行
wget ... | sh             # 下载并执行
eval()                    # 动态代码执行
exec()                    # 命令执行
os.system()               # 系统命令执行
subprocess.Popen(shell=True)  # Shell 命令执行
```

**编码绕过尝试**：
```
# 以下模式会被拦截
base64 -d                 # Base64 解码执行
echo ... | base64 -d      # 管道解码
xxd -r                    # Hex 解码
```

**Python 特有风险**：
```python
# 以下模式会被拦截
__import__()              # 动态导入
pickle.load()             # 反序列化（可执行任意代码）
yaml.load()               # 不安全的 YAML 加载
yaml.unsafe_load()        # 显式不安全加载
```

#### Shell 操作符拦截

参数中包含以下操作符时会被拦截：

| 操作符 | 说明 |
|--------|------|
| `&&`, `\|\|` | 命令链接 |
| `;` | 命令分隔 |
| `\|` | 管道 |
| `$()`, `` ` `` | 命令替换 |
| `>`, `>>`, `<` | 重定向 |
| `2>`, `&>` | 错误/组合重定向 |
| `\n`, `\r` | 换行注入 |

#### 校验结果

校验失败时返回详细的错误信息：

```go
type ValidationError struct {
    Type    string // 错误类型：dangerous_command, dangerous_pattern, arg_injection 等
    Pattern string // 匹配到的模式
    Context string // 上下文信息
    Message string // 人类可读的描述
}
```

**示例错误**：
```
security validation failed [dangerous_command]: Script contains dangerous command: rm -rf / (pattern: rm -rf /, context: ...cleanup && rm -rf / && echo done...)
```

#### 使用示例

```go
// 创建校验器
validator := sandbox.NewScriptValidator()

// 校验脚本内容
result := validator.ValidateScript(scriptContent)
if !result.Valid {
    for _, err := range result.Errors {
        log.Printf("Security error: %s", err.Error())
    }
    return errors.New("script validation failed")
}

// 校验命令行参数
argsResult := validator.ValidateArgs(args)

// 校验标准输入
stdinResult := validator.ValidateStdin(stdin)

// 或一次性校验全部
fullResult := validator.ValidateAll(scriptContent, args, stdin)
```

---

### Docker 沙箱

Docker 模式提供最强的隔离：

- **非 root 用户**：容器内以普通用户运行
- **Capability 限制**：移除所有 Linux capabilities
- **只读文件系统**：根文件系统只读
- **资源限制**：内存 256MB，CPU 限制
- **网络隔离**：默认无网络访问
- **临时挂载**：Skill 目录只读挂载
- **脚本预校验**：执行前进行安全校验

#### 沙箱镜像

系统使用专用的沙箱镜像 `wechatopenai/weknora-sandbox`，预装了 Python 3.11、Node.js 20、常用 CLI 工具和 Python 库，无需在执行时临时安装依赖。

**预拉取镜像**（推荐在首次部署时执行，避免首次执行脚本时等待下载）：

```bash
# 方式一：直接拉取
docker pull wechatopenai/weknora-sandbox:main

# 方式二：本地构建
sh scripts/build_images.sh -s
```

> 如果未预拉取，创建第一个沙箱时会先拉取镜像，首次执行需要等待下载完成；也可以在设置页的模板步骤提前触发拉取。

> 用 `main` 而非 `latest`：`latest` 只在发版时移动，目前仍停在 `/workspace` 及其 `input`/`output` 目录交给沙箱账号之前的版本，用它建出来的沙箱写不了自己的产物目录。发版带上该修复后即可换回 `latest`。

**镜像内置环境**：
- Python 3.11 + pip（requests、pyyaml、pandas、beautifulsoup4）
- Node.js 20 + npm
- CLI 工具：jq、curl、bash、grep、sed、awk 等

```bash
# Docker 执行示例
docker run --rm \
  --user 1000:1000 \
  --cap-drop ALL \
  --read-only \
  --memory=256m \
  --network=none \
  -v /path/to/skill:/skill:ro \
  -w /skill \
  wechatopenai/weknora-sandbox:main \
  python scripts/analyze.py input.pdf
```

## API 参考

### SkillManager

```go
type Manager interface {
    // 初始化，发现所有 Skills
    Initialize(ctx context.Context) error
    
    // 获取所有 Skill 元数据（Level 1）
    GetAllMetadata() []*SkillMetadata
    
    // 加载 Skill 指令（Level 2）
    LoadSkill(ctx context.Context, skillName string) (*Skill, error)
    
    // 读取 Skill 文件内容（Level 3）
    ReadSkillFile(ctx context.Context, skillName, filePath string) (string, error)
    
    // 列出 Skill 中的所有文件
    ListSkillFiles(ctx context.Context, skillName string) ([]string, error)
    
    // 执行 Skill 脚本
    ExecuteScript(ctx context.Context, skillName, scriptPath string, args []string) (*sandbox.ExecuteResult, error)
    
    // 检查是否启用
    IsEnabled() bool
}
```

### Skill 结构

```go
type Skill struct {
    Name         string // 技能名称
    Description  string // 技能描述
    BasePath     string // 目录绝对路径
    FilePath     string // SKILL.md 绝对路径
    Instructions string // SKILL.md 主体指令内容
    Loaded       bool   // 是否已加载 Level 2
}

type SkillMetadata struct {
    Name        string // 技能名称
    Description string // 技能描述
    BasePath    string // 目录路径
}
```

### ExecuteResult 结构

```go
type ExecuteResult struct {
    ExitCode int           // 退出码
    Stdout   string        // 标准输出
    Stderr   string        // 标准错误
    Duration time.Duration // 执行时长
    Error    error         // 执行错误
}
```

## 示例：完整工作流

以下是 Agent 处理用户请求的完整流程：

```
用户: "帮我从 report.pdf 提取表格数据"

Agent 思考:
  → 查看 System Prompt 中的 Skills 列表
  → 发现 "pdf-processing" 技能匹配

Agent 行动 1: 调用 read_skill
  → {"skill_name": "pdf-processing"}
  → 获取 SKILL.md 指令内容
  → 学习如何使用 pdfplumber

Agent 行动 2: 调用 execute_skill_script
  → {"skill_name": "pdf-processing", 
     "script_path": "scripts/extract_text.py",
     "args": ["report.pdf"]}
  → 脚本在沙箱中执行，返回提取的表格数据

Agent 回复:
  → 向用户展示提取的表格数据
  → 提供数据使用建议
```

## 故障排查

### Skill 未被发现

1. 检查 `skill_dirs` 配置是否正确
2. 确认目录中存在 `SKILL.md` 文件
3. 验证 YAML frontmatter 格式

```bash
# 运行 demo 验证
go run ./cmd/skills-demo/main.go
```

### 脚本执行失败

1. 检查沙箱后端配置
2. Docker 模式：确认 Docker 服务运行中
3. 检查脚本权限和语法

### 元数据验证错误

常见错误：
- `skill name too long`: 名称超过 50 字符
- `skill name contains invalid characters`: 包含非法字符
- `skill name is reserved`: 使用了保留词
- `skill description too long`: 描述超过 500 字符
