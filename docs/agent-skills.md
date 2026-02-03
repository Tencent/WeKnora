# Agent Skills 文档

## 概述

Agent Skills 是一种让 Agent 通过阅读"使用说明书"来学习新能力的扩展机制。与传统的硬编码工具不同，Skills 通过注入到 System Prompt 来扩展 Agent 的能力，遵循 **Progressive Disclosure（渐进式披露）** 的设计理念。

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

## Available Operations

1. **Text Extraction**: Extract text content from PDF pages
2. **Table Extraction**: Extract tabular data from PDFs
...
```

### 元数据验证规则

| 字段 | 要求 |
|------|------|
| `name` | 1-50 字符，仅允许 `a-z`, `0-9`, `-`, `_`，不能是保留词 |
| `description` | 1-500 字符，描述技能用途和触发条件 |

**保留词**：`system`, `default`, `internal`, `core`, `base`, `root`, `admin`

### 最佳实践

**name 命名**：
- ✅ `pdf-processing`, `code_review`, `api-client`
- ❌ `PDF Processing`, `my skill`, `system`

**description 编写**：
- 清晰描述技能的功能
- 包含触发条件（如 "when working with PDF files"）
- 避免过于模糊的描述

## 配置

### AgentConfig 配置项

```go
type AgentConfig struct {
    // ... 其他配置 ...
    
    // Skills 相关配置
    SkillsEnabled  bool     `json:"skills_enabled"`   // 是否启用 Skills
    SkillDirs      []string `json:"skill_dirs"`       // Skill 目录列表
    AllowedSkills  []string `json:"allowed_skills"`   // 白名单（空=全部允许）
    SandboxMode    string   `json:"sandbox_mode"`     // sandbox 模式
    SandboxTimeout int      `json:"sandbox_timeout"`  // 脚本执行超时（秒）
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
  "allowed_skills": ["pdf-processing", "code-review"],
  "sandbox_mode": "docker",
  "sandbox_timeout": 30
}
```

### Sandbox 模式

| 模式 | 说明 |
|------|------|
| `docker` | 使用 Docker 容器隔离（推荐） |
| `local` | 本地进程执行（基础安全限制） |
| `disabled` | 禁用脚本执行 |

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

## 创建自定义 Skill

### 第一步：创建目录结构

```bash
mkdir -p my-skills/code-review
cd my-skills/code-review
```

### 第二步：编写 SKILL.md

```markdown
---
name: code-review
description: Review code for best practices, security issues, and performance. Use when the user asks to review, analyze, or improve code quality.
---

# Code Review Skill

This skill helps analyze code for quality and security issues.

## How to Use

When reviewing code:

1. Check for common security vulnerabilities
2. Identify performance bottlenecks
3. Suggest best practice improvements

## Security Checklist

- [ ] SQL Injection prevention
- [ ] XSS protection
- [ ] Input validation
- [ ] Authentication checks

## Performance Tips

- Avoid N+1 queries
- Use appropriate data structures
- Consider caching strategies
```

### 第三步：添加辅助脚本（可选）

创建 `scripts/lint.py`：

```python
#!/usr/bin/env python3
"""Simple code linter for demonstration."""
import sys
import json

def lint_code(filepath):
    issues = []
    with open(filepath) as f:
        for i, line in enumerate(f, 1):
            if len(line) > 120:
                issues.append({
                    "line": i,
                    "issue": "Line too long",
                    "severity": "warning"
                })
            if "eval(" in line:
                issues.append({
                    "line": i,
                    "issue": "Avoid using eval()",
                    "severity": "error"
                })
    return issues

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: lint.py <filepath>")
        sys.exit(1)
    
    result = lint_code(sys.argv[1])
    print(json.dumps(result, indent=2))
```

### 第四步：配置 Agent

将 Skill 目录添加到 Agent 配置：

```json
{
  "skills_enabled": true,
  "skill_dirs": ["/path/to/my-skills"]
}
```

## 沙箱安全机制

### Docker 沙箱

Docker 模式提供最强的隔离：

- **非 root 用户**：容器内以普通用户运行
- **Capability 限制**：移除所有 Linux capabilities
- **只读文件系统**：根文件系统只读
- **资源限制**：内存 256MB，CPU 限制
- **网络隔离**：默认无网络访问
- **临时挂载**：Skill 目录只读挂载

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
  python:3.11-slim \
  python scripts/analyze.py input.pdf
```

### Local 沙箱

Local 模式提供基础保护：

- **命令白名单**：仅允许特定解释器
- **工作目录限制**：限定在 Skill 目录
- **环境变量过滤**：仅传递安全变量
- **超时控制**：默认 30 秒超时
- **路径遍历防护**：防止访问 Skill 目录外文件

**允许的命令**：
- `python`, `python3`
- `node`, `nodejs`
- `bash`, `sh`
- `ruby`
- `go run`

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

1. 检查 `sandbox_mode` 配置
2. Docker 模式：确认 Docker 服务运行中
3. Local 模式：确认解释器已安装
4. 检查脚本权限和语法

### 元数据验证错误

常见错误：
- `skill name too long`: 名称超过 50 字符
- `skill name contains invalid characters`: 包含非法字符
- `skill name is reserved`: 使用了保留词
- `skill description too long`: 描述超过 500 字符

## 运行 Demo

```bash
cd /path/to/WeKnora
go run ./cmd/skills-demo/main.go
```

输出示例：

```
=======================================================================
  Agent Skills Demo - Progressive Disclosure in Action
=======================================================================

📁 Skills directory: /path/to/WeKnora/examples/skills

Step 1: Initialize Sandbox Manager
---------------------------------------------------
✅ Sandbox initialized (type: local)

Step 2: Initialize Skills Manager
---------------------------------------------------
✅ Discovered 1 skills

...

🎉 Demo completed successfully!
```
