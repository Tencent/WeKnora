# 生图提示词模板

每张图单独生成。根据正文内容替换变量，不要把多张图拼在一起。

## Seedream API 适配

原版小黑 skill 使用内置 `image_gen` 工具，本 skill 替换为 Seedream 5.0 Pro API。API 的 `prompt` 字段接受一段完整的文本提示词，因此将原版模板的各段拼接为一个连续字符串传入。

## 提示词模板

```
Generate one standalone 16:9 horizontal Chinese article illustration.

Visual DNA:
Pure white background. Minimalist black hand-drawn line art. Slightly wobbly pen lines. Lots of empty white space. Sparse red/orange/blue handwritten Chinese annotations. Clean absurd product-sketch feeling. No gradients, no shadows, no paper texture, no complex background, no commercial vector style, no PPT infographic look, no cute mascot poster, no children's illustration, no realistic UI.

Recurring IP character required:
小黑, a small solid-black absurd creature with white dot eyes, tiny thin legs, blank serious expression, slightly uneven hand-drawn body shape. 小黑 must perform the core conceptual action, not decorate the scene. Make 小黑 serious, deadpan, and slightly bizarre, not cute.

Theme:
{正文配图主题}

Structure type:
{结构类型：Workflow / 系统局部 / 前后对比 / 角色状态 / 概念隐喻 / 方法分层 / 地图路线 / 小漫画分镜}

Core idea:
{这张图要表达的核心意思}

Composition:
{具体画面：小黑在哪里、正在做什么、主要物件是什么、信息如何流动}

Suggested elements:
{元素1} / {元素2} / {元素3} / {元素4}

Chinese handwritten labels:
{标注词1} / {标注词2} / {标注词3} / {标注词4} / {可选标注词5}

Color use:
Black for main line art and 小黑. Orange for main flow/path/arrows. Red only for key warnings/problems/results. Blue only for secondary notes or feedback/system state.

Constraints:
One image explains only one core structure. Keep the main subject around 40%-60% of the canvas. Preserve at least 35% blank white space. Use at most 5-8 short handwritten Chinese labels. Do not write a title in the top-left corner. Do not write the structure type on the image. Do not make it a formal diagram, course slide, or dense explainer. Do not copy prior examples or reuse known case compositions unless explicitly requested; invent a fresh visual metaphor for this specific article. It should be clear but not instructional, interesting but not childish, strange but clean.
```

## 从插画计划映射变量

如输入为插画计划 JSON，按以下方式映射到模板变量：

| 模板变量 | 插画计划字段 | 映射方式 |
|---|---|---|
| `{正文配图主题}` | `visual_purpose` | 直接使用，描述画什么 |
| `{结构类型}` | `visual_structure` | 查 `composition-patterns.md` 映射表 |
| `{这张图要表达的核心意思}` | `visual_purpose` | 补充一句话说明意图 |
| `{具体画面}` | `visual_purpose` + `visual_structure` | 转译为画面描述：小黑在哪、做什么、物件是什么 |
| `{元素N}` | `must_include` | 每个必须包含的项转为一个元素 |
| `{标注词N}` | `must_include` | 关键元素同时作为中文标注词 |
| 负向约束 | `must_avoid` | 在 Constraints 段追加："Do not include: {must_avoid 内容}" |

### visual_structure 映射表

| visual_structure 值 | 模板中的结构类型 |
|---|---|
| `path` | 地图路线 |
| `flow` | Workflow 流程 |
| `comparison` | 前后对比 |
| `map` | 概念隐喻 |
| `timeline` | 地图路线 |
| `hierarchy` | 方法分层 |
| `causal` | Workflow 流程 |
| `state` | 角色状态 |
| `comic` | 小漫画分镜 |

## 调用方式

将模板填好后，作为 `prompt` 参数传入 Seedream API：

```bash
python scripts/generate_image.py \
  --prompt "填好的完整提示词" \
  --size "2K" \
  --output "01-topic-name.png"
```

或用 curl（见 `api-contract.md`）：

```bash
curl -X POST https://ark.cn-beijing.volces.com/api/v3/images/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ARK_API_KEY" \
  -d '{
    "model": "doubao-seedream-5-0-pro-260628",
    "prompt": "填好的完整提示词",
    "size": "2K",
    "response_format": "url",
    "watermark": false,
    "sequential_image_generation": "disabled",
    "stream": false
  }'
```

## 长度控制

Seedream API 建议提示词不超过 300 个汉字或 600 个英文单词。模板本身的英文骨架约 200 词，填充后总量在合理范围内。如中文标注词较多，优先精简元素描述。

## 图像编辑提示

Seedream API 支持图生图编辑（传入 `image` 参数）。以下为局部编辑场景的提示词：

去掉左上角标题：

```
Edit the provided image. Remove only the handwritten title "{要删除的文字}" and its underline from the top-left corner. Fill that area with the same clean white background, matching the surrounding blank paper. Preserve everything else exactly: characters, labels, paths, line style, composition, aspect ratio, and image quality. Do not add any new text or objects.
```

增强怪诞感：

```
Regenerate this illustration with the same core meaning and simple layout, but make 小黑 more central to the conceptual action. 小黑 should be doing the strange work that explains the idea, not standing beside the diagram. Keep it clean, sparse, hand-drawn, and not cute.
```
