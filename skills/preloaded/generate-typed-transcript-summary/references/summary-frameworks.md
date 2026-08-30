# 类型化总结框架

## `interview` 人物访谈

```markdown
## 一、人物背景
## 二、经历与决策
## 三、核心观点
## 四、原则与思维模型
## 五、案例与证据
## 六、反思与边界
```

## `training` 培训课程

```markdown
## 一、目标与受众
## 二、知识地图
## 三、核心概念
## 四、方法与步骤
## 五、示例与异常
## 六、练习与应用
```

## `salon` 沙龙分享

```markdown
## 一、活动与参与者
## 二、议题与观点
## 三、观点交锋
## 四、案例与问答
## 五、共识与分歧
## 六、探索方向
```

## `general` 通用内容

```markdown
## 一、定位与问题
## 二、主张与论证
## 三、证据与案例
## 四、限定与反方
## 五、影响与建议
```

## 通用结构规则

- 上述二级标题的文案和顺序必须原样保留。
- 可在标准章节下增加三级标题，但不得用自拟二级标题取代模板章节。
- 次类型框架只用于主类型章节内的局部表达。

## JSON 渲染契约

模板只定义总结结构，不写入 WeKnora。生成结果使用以下字段：

```json
{
  "schemaVersion": 1,
  "videoType": "training",
  "sections": [
    {
      "id": "goals-audience",
      "title": "一、目标与受众",
      "blocks": [
        {
          "id": "block-1",
          "kind": "paragraph",
          "text": "可直接展示的纯文本",
          "evidenceChunkIds": ["转写分块 ID"]
        }
      ]
    }
  ]
}
```

- `block.kind` 只能是 `paragraph` 或 `bullet`。
- `block.text` 不得包含 Markdown 标题、列表符号、代码围栏或 HTML。
- 后端根据 `evidenceChunkIds` 回填 `evidence`，包括原文、起止时间和时间戳；LLM 不得自行填写出处正文。
- 每个展示 block 至少关联一个当前转写代次的证据分块。
