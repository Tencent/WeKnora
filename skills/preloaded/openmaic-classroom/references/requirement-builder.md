# 需求构建指南

将 WeKnora RAG 检索结果转换为 OpenMAIC 课程生成所需的 `requirement` 格式。

## 核心原则

OpenMAIC 的 `requirement` 字段需要是**结构化的教学需求描述**，而不是原始文档片段。构建时需考虑：

1. **教学主题**: 明确要教什么
2. **目标受众**: 面向谁教学（如：初学者、专业人士、学生等）
3. **教学深度**: 入门级、中级、高级
4. **内容范围**: 基于哪些知识源构建

## 转换模板

### 模板 1: 纯需求（无检索结果）

用户直接描述需求时，直接使用用户描述作为 requirement：

```
用户: "帮我创建一个关于量子力学的入门课程"
→ requirement: "Create an introductory classroom on quantum mechanics for beginners"
```

### 模板 2: 基于 RAG 检索结果

```
步骤：
1. 使用 knowledge_search 检索相关知识
2. 从检索结果中提取：
   - 核心主题/概念
   - 关键知识点
   - 文档来源信息
3. 构建结构化 requirement
```

构建格式：
```
基于以下知识内容，创建一个面向[目标受众]的[深度级别]课程：

核心主题：[从检索结果提取的主要概念]
关键知识点：
- [知识点1]
- [知识点2]
- ...
内容来源：[文档名称列表]
```

### 模板 3: 基于单个文档

```
基于文档《[文档名称]》的内容，创建一个面向[目标受众]的课程，
重点讲解以下方面：
- [用户指定的重点1]
- [用户指定的重点2]
```

### 模板 4: 基于多个文档/知识块

```
综合以下文档内容，创建一个系统的课程：

文档1《[名称1]》: [简要内容摘要]
文档2《[名称2]》: [简要内容摘要]
文档3《[名称3]》: [简要内容摘要]

要求：
- 教学深度：[级别]
- 目标受众：[描述]
- 重点覆盖：[关键主题列表]
```

### 模板 5: 基于概念图遍历（Concept Graph）

当从知识图谱 concept 页面及其关联 entity 生成微课堂时使用此模板。由 `scripts/concept-to-requirement.py` 自动生成。

**输入结构**：
```json
{
  "concept": { "slug": "concept/rag", "title": "RAG 检索增强生成", "summary": "...", "content": "..." },
  "entities": [
    { "slug": "entity/vector-db", "title": "向量数据库", "summary": "...", "link_type": "outlink" },
    { "slug": "entity/embedding", "title": "Embedding 模型", "summary": "...", "link_type": "bidirectional" }
  ],
  "language": "zh-CN",
  "depth": "intermediate",
  "audience": "相关领域的学习者"
}
```

**输出 requirement 结构**：
```
基于知识图谱概念「[concept.title]」，为[audience]创建一个[depth]微课堂（micro-classroom）。

教学锚点：[concept.summary]

学习目标：
  - 理解[concept.summary 中的关键句]

核心知识点：
  - [从 concept.content 解析的定义/机制]

关联实体（实践环节）：
  - 案例：[entity.title]：[entity.summary]
  - 工具：[entity.title]：[entity.summary]
  - 应用场景：[entity.title]：[entity.summary]
  - 前置知识：[entity.title]

实践任务：
  - 通过 [entity.title] 实践 [concept.title] 的应用

常见误区检查：
  - [从 concept.content 解析的误区]

评估提示：
  - 请解释 [concept.title] 的核心定义

请使用中文生成课程内容。
```

**entity 分类排序规则**（纯文本操作，无 LLM/embedding）：
- link_type 权重：bidirectional (+3) > outlink (+2) > inlink (+1)
- title token overlap：concept title 分词后与 entity title 的交集数 (+1 per hit, cap +2)
- summary keyword hit：concept summary 关键词在 entity summary 中出现 (+1 per hit, cap +2)
- slug token hit：concept slug token 在 entity slug 中出现 (+1)
- summary 为空扣分 (-2)
- 取 top 3-5 entities，分为 Examples / Tools / Application Scenarios / Prerequisites

**概念内容解析逻辑**：
- 优先解析 markdown 结构：标题列表（定义段、机制段、案例段、误区段）
- fallback 到前 N 字

## 示例

### 示例 1: 技术文档 → 课程

```
检索结果：
- 文档: "Kubernetes 部署指南.pdf"
- 关键内容: Pod 管理、Service 配置、Ingress 路由、存储卷

构建的 requirement：
"基于 Kubernetes 部署指南，创建一个面向 DevOps 工程师的中级课程。
重点涵盖：Pod 生命周期管理、Service 和 Ingress 网络配置、持久化存储卷管理。
课程应包含实践操作环节。"
```

### 示例 2: 产品手册 → 入门课程

```
检索结果：
- 文档: "产品使用手册 v2.0.pdf"
- 关键内容: 产品概述、快速开始、核心功能、常见问题

构建的 requirement：
"基于产品使用手册 v2.0，为新用户创建一个入门课程。
帮助用户快速了解产品核心功能，掌握基本操作方法，
并能够独立完成常见任务。课程语言为中文。"
```

### 示例 3: 研究论文 → 高级课程

```
检索结果：
- 文档: "Transformer 架构研究综述.pdf"
- 关键内容: Attention 机制、位置编码、多头注意力、训练技巧

构建的 requirement：
"基于 Transformer 架构研究综述，为具有深度学习基础的研究人员
创建高级课程。深入讲解 Attention 机制的数学原理、位置编码的
各种变体、多头注意力的设计动机，以及训练大模型时的实践技巧。"
```

## 可选功能配置建议

在构建 request 时，根据用户需求推荐可选功能：

| 场景 | 推荐功能 |
|------|----------|
| 技术培训 | `enableWebSearch: true`（补充最新技术动态） |
| 产品介绍 | `enableImageGeneration: true`（生成产品截图/界面图） |
| 市场营销 | `enableImageGeneration: true, enableVideoGeneration: true` |
| 语言教学 | `enableTTS: true`（语音朗读） |
| 学术研究 | 默认配置即可（不需要多媒体） |

## 多文档处理

当需要将多个独立文档分别生成课程时：

1. 为每个文档单独构建 requirement
2. 依次调用生成 API（不可并行）
3. 每完成一个即返回 URL，继续下一个
4. 最终汇总所有 Classroom URL

> 注意：OpenMAIC 托管模式每天最多 10 次生成配额，本地模式取决于 LLM Provider 配额。
