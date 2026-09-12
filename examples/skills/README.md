# Skills 示例

本目录提供可安装到 WeKnora 沙箱的技能样例，供技能维护者参考和测试。
使用前需在空间安装技能，并为启用 Skills 的智能体选择对应沙箱配置；工作台预览另需开启 `WEKNORA_SANDBOX_WORKBENCH_ENABLED`。
技能包含可执行代码，安装前检查依赖和来源，不要用生产凭据运行样例测试。

| 技能 | 用途 | 入口 |
| --- | --- | --- |
| `pdf-processing` | 提取 PDF 文本、分析表单字段 | [SKILL.md](pdf-processing/SKILL.md)、[表单说明](pdf-processing/FORMS.md) |
| `presentation-builder` | 从 JSON 生成含封面、要点、表格或本地图片的 PPTX | [SKILL.md](presentation-builder/SKILL.md)、[样例 JSON](presentation-builder/assets/sample.json) |

配置与执行约定见 [Agent Skills](../../docs/agent-skills.md)，终端和文件安全边界见 [Sandbox Workbench](../../docs/sandbox-workbench.md)。

## 安装与调用

从仓库根目录将完整技能目录打包，上传到空间技能设置的目标沙箱配置：

```bash
bundle_zip="$(mktemp -d)/presentation-builder.zip"
(
  cd examples/skills
  zip -r "$bundle_zip" presentation-builder -x '*/__pycache__/*' '*.pyc'
)
```

保留 `SKILL.md`、脚本、依赖清单、assets 和 tests。安装成功后，Agent 先读取 `skill://presentation-builder/SKILL.md`，再调用：

```json
{
  "skill_name": "presentation-builder",
  "command": "python \"$WEKNORA_SKILL_DIR/scripts/build_presentation.py\" --input \"$WEKNORA_SKILL_DIR/assets/sample.json\" --output presentation.pptx"
}
```

将上述参数传给 `shell_exec`。成功时脚本输出文件路径、页数和字节数，文件位于 `/workspace/output`；同名文件不会覆盖，重复执行需改输出名。图片仅接受用户提供或自制的本地 PNG/JPEG，样例不需要图片和网络。

## 本地测试与预览

以下命令从仓库根目录运行，需要 Linux、Python 3.11 和 Node.js 24。Python 单测使用临时工作目录，不需要沙箱凭据或 `/workspace` 写权限：

```bash
repo_root="$(pwd -P)"
test_venv="$(mktemp -d)/venv"
python3 -m venv "$test_venv"
"$test_venv/bin/python" -m pip install \
  -r "$repo_root/examples/skills/presentation-builder/requirements.txt"
"$test_venv/bin/python" -B -m unittest discover \
  -s "$repo_root/examples/skills/presentation-builder/tests" -v
python3 -B -I "$repo_root/internal/sandbox/workbench_files_test.py"
```

builder 的 27 项测试覆盖 JSON 和路径校验、不覆盖写入、图片限额及 PPTX 结构。命令行输入必须是允许目录下的绝对路径或 `-`（stdin），输出仅接受文件名，不支持覆盖工作区根目录。

浏览器测试还需要 PPTX、HTML、CSV 三份合成 fixture。可在有权限的测试 Docker daemon 上构建标准镜像，再用一次性容器生成 PPTX；只有指定的 fixture 目录会被写入：

```bash
export PREVIEW_ARTIFACT_DIR="$(mktemp -d)"
export WORKBENCH_TEST_DOCKER_HOST=unix:///var/run/docker.sock
export WORKBENCH_TEST_DOCKER_IMAGE=weknora-sandbox:workbench-test
docker --host "$WORKBENCH_TEST_DOCKER_HOST" build \
  -f docker/Dockerfile.sandbox --target sandbox \
  -t "$WORKBENCH_TEST_DOCKER_IMAGE" .
docker --host "$WORKBENCH_TEST_DOCKER_HOST" run --rm \
  --user "$(id -u):$(id -g)" \
  --mount "type=bind,src=$repo_root/examples/skills/presentation-builder,dst=/skill,readonly" \
  --mount "type=bind,src=$PREVIEW_ARTIFACT_DIR,dst=/workspace/output" \
  "$WORKBENCH_TEST_DOCKER_IMAGE" sh -ec '
    python3 -m venv /tmp/builder
    /tmp/builder/bin/python -m pip install --no-cache-dir -r /skill/requirements.txt
    /tmp/builder/bin/python /skill/scripts/build_presentation.py \
      --input /skill/assets/sample.json --output agent-workbench-demo.pptx
  '
printf '%s\n' '<h1>Workbench report</h1><p>Generated preview fixture.</p>' \
  > "$PREVIEW_ARTIFACT_DIR/workbench-report.html"
printf '%s\n' 'Backend,Status' 'Docker,Ready' 'E2B,Ready' \
  > "$PREVIEW_ARTIFACT_DIR/backend-summary.csv"
```

这里的 bind mount 由 daemon 所在主机解释，命令假定 daemon 与仓库同机；远程 daemon 可按 [Docker 测试准备](../../docs/sandbox-workbench.md#docker-准备)构建镜像，并在远端生成后取回 fixture。CSV 的状态为合成测试数据，不代表后端运行结果。

安装前端依赖和 Chromium，启动 Vite 后运行浏览器检查：

```bash
cd "$repo_root/frontend"
npm ci --no-audit --no-fund
npx playwright install --with-deps chromium
npm run dev -- --host 127.0.0.1
```

在另一个终端进入 `frontend`，设置前面生成目录的绝对路径后运行：

```bash
PREVIEW_ARTIFACT_DIR=/absolute/path/to/generated-fixtures npm run test:workbench-browser
npm test
npm run type-check
npm run build
```

浏览器测试拦截 fixture API，不登录真实账号，覆盖实时文件和消息产物预览、OOXML 外链拒绝、图片限额及 HTML 凭据和网络隔离。自动化流程见 [Frontend CI](../../.github/workflows/frontend.yml)。
