---
name: generate-seedream-illustration
description: 调用豆包 Seedream 5.0 Pro API 生成 Ian 小黑风格的中文正文配图。用于为文字稿总结、知识卡片、方法论、流程、结构、状态、隐喻或观点生成配图；默认使用小黑 IP、纯白手绘、少量红橙蓝批注、简洁清爽但天马行空的视觉风格。需配合 ARK_API_KEY 环境变量。
---

# Ian 小黑怪诞正文配图（Seedream 版）

为中文文章生成 16:9 横版正文配图。把文章里的关键判断、流程、结构、状态或隐喻，变成一张清爽、怪诞、有创意的手绘解释图。

默认视觉 IP 是"小黑"：黑色实心、白点眼、细腿、空表情，认真做一件荒诞但成立的事。小黑必须参与画面的核心动作，不能只当装饰。

与原版 `$ian-xiaohei-illustrations` 的唯一区别：生图工具从内置 `image_gen` 替换为豆包 Seedream 5.0 Pro API。

## 参考文件

按任务需要读取，不要一次塞满上下文：

- `references/style-dna.md`：风格 DNA、颜色、文字、禁忌。
- `references/xiaohei-ip.md`：小黑 IP 的形象、性格、动作库和禁忌。
- `references/composition-patterns.md`：结构类型、原创隐喻方法和反复刻规则。
- `references/prompt-template.md`：单张生图提示词模板（适配 Seedream API）。
- `references/qa-checklist.md`：生成后检查和迭代规则。
- `references/api-contract.md`：Seedream 5.0 Pro API 参数和错误处理文档。
- `references/illustration-contract.md`：插画计划 JSON 契约。

## API 配置

| 配置项 | 值 |
|---|---|
| 端点 | `https://ark.cn-beijing.volces.com/api/v3/images/generations` |
| 模型 | `doubao-seedream-5-0-pro-260628` |
| 鉴权 | Bearer `$ARK_API_KEY`（环境变量，不得硬编码） |
| 返回格式 | `url`（默认）或 `b64_json` |
| 水印 | 默认关闭 |

详细参数、错误码和重试策略见 [references/api-contract.md](references/api-contract.md)。

## 工作流

### 1. 消化输入

如输入为插画计划 JSON（兼容 `illustration-contract.md`），提取 `visual_purpose`、`visual_structure`、`must_include`、`must_avoid` 四个字段，映射到构图类型和提示词变量。

如输入为正文或 Markdown，先提炼核心观点、认知转折点和适合用图解释的内容。不要平均配图，优先选择认知锚点。

### 2. 先出配图策略

如果用户只是说"分析怎么配图"，先给 shot list：每张图写清放置位置、主题、核心意思、结构类型、小黑动作、建议元素和标注词。默认 4-8 张，够用就好。

### 3. 单张生成

如果用户明确要求"生成 / 做图"，按 `references/prompt-template.md` 构造提示词，然后调用 API：

```bash
python scripts/generate_image.py \
  --prompt "构造好的提示词" \
  --size "2K" \
  --output "illustration_01.png"
```

每张图只讲一个核心结构，不要把多张图拼在一张里。不要复刻过往案例，每次从当前文章重新发明隐喻。

### 4. 检查与迭代

生成后按 [references/qa-checklist.md](references/qa-checklist.md) 检查。不合格的图重生成或局部编辑。

### 5. 保存交付

把最终图按顺序命名保存到指定路径（`01-topic-name.png`）。保留原始生成文件，不要覆盖已有资产，除非用户明确要求替换。返回的图片 URL 有效期 24 小时，必须及时下载。

## 强制规则

- 提示词只描述插画计划中已有的知识结构，不得新增人物、数据、因果或事实。
- 生成失败不得伪造图片文件或使用占位图替代。

## 输出口径

生成后的交付要包含：生成了几张、每张图的用途、保存路径、哪些图最稳哪些是可选。不要长篇解释风格理论；让图自己说话。
