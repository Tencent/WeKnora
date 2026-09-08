# 输出样例

样例只说明结构。实际运行必须替换为当前视频的真实源文档、对象、证据、分块、时间和工具返回的页面 ID。示例值不得复制到真实页面，局部片段不得单独作为页面写入。

## 1. 完整对象页

本例是提交给写入工具的候选提案，同时验证“实体一个有效属性即可发布”、证据贡献和四类来源字段不能混用。`V002-C001` 是候选 ID，不是最终规范对象 ID：

```yaml
---
page_type: index
id: V002-C001
knowledge_object_id: V002-C001
type: entity
primary_type: entity
entity_sub_type: organization
source_video_id: V002
source_document_id: doc-V002
transcript_generation: generation-20260907
title: 示例研究院
canonical_name: 示例研究院
information_nature: 机构
audit_status: passed
classification_confidence: 0.96
core_content: 示例研究院是视频中负责相关研究的机构。
evidence_ids: [ev-004]
source_refs: [doc-V002]
chunk_refs: [chunk-004]
time_range: 00:01:02-00:01:10
structure_fields:
  org_type: 研究机构
field_evidence:
  org_type: [ev-004]
evidence_contribution:
  source_video_id: V002
  source_document_id: doc-V002
  transcript_generation: generation-20260907
  evidence_ids: [ev-004]
  chunk_refs: [chunk-004]
  time_range: 00:01:02-00:01:10
  field_evidence:
    org_type: [ev-004]
  quality_status: passed
related_content: []
relations: []
---
# 示例研究院

## 一句话概述

示例研究院是视频中负责相关研究的机构。
```

## 2. 四类知识原子最小结构

以下片段只展示四类差异字段，不能单独写入。每页仍须带第 1 节的全部共同字段。

```yaml
- title: 网络效应
  primary_type: concept
  core_content: 网络效应描述参与者和连接增长如何提高产品价值。
  structure_fields:
    definition: 产品价值随参与者数量和连接增加而提升的现象
    mechanism: 新参与者增加可连接节点，从而提高网络整体效用
  field_evidence:
    definition: [ev-006]
    mechanism: [ev-006]

- title: 留存异常归因法
  primary_type: methodology
  core_content: 该方法通过定位时间拐点并逐层对比来寻找留存异常原因。
  structure_fields:
    input: 按时间、渠道和用户分群整理的留存数据
    steps: 先定位拐点，再分层对比，最后核对同期变更
  field_evidence:
    input: [ev-008]
    steps: [ev-008, ev-009]

- title: 社交产品投放调整案例
  primary_type: case
  core_content: 团队调整投放结构后短期止跌，但产生新的成本与品牌问题。
  structure_fields:
    context: 产品日活连续六周下降且预算缩减
    actions: 团队暂停品牌投放并把预算转向效果广告
    outcome: 日活止跌，但获客成本和品牌搜索表现恶化
  field_evidence:
    context: [ev-010]
    actions: [ev-010]
    outcome: [ev-011]

- title: 预算收缩时不能只看短期获客
  primary_type: insight
  core_content: 只追求短期转化可能以长期增长损失为代价。
  structure_fields:
    claim: 预算收缩时只保留效果渠道可能损害长期增长
    reasoning: 案例中日活虽止跌，但获客成本上升且品牌搜索量下降
  field_evidence:
    claim: [ev-012]
    reasoning: [ev-011]
```

## 3. 案例支持洞察

对象首次写入并按服务端返回的规范身份回读后，案例页可以补写以下关联。第二次调用仍须提交完整页面，只替换下面两个关联字段的值，不能用该片段覆盖页面：

```yaml
related_content:
  - target_object_id: <工具返回的规范对象 ID>
    target_wiki_page_id: <工具返回的真实页面 ID>
    target_type: insight
    title: 预算收缩时不能只看短期获客
    slug: <工具返回的真实 slug>
relations:
  - relation_id: V002-R001
    relation_type: supports
    target_object_id: <工具返回的规范对象 ID>
    target_wiki_page_id: <工具返回的真实页面 ID>
    evidence_ids: [ev-011]
    time_range: 00:04:00-00:04:10
    confidence: 0.88
```

方向为 `案例 supports 洞察`。

## 4. 候选去向

```yaml
decisions:
  - candidate_id: C001
    result: publish_candidate
    proposed_type: concept
  - candidate_id: C002
    result: merge
    target_candidate_id: C001
    reason: 只有概念示例，不能独立发布
    evidence_ids: [ev-020]
  - candidate_id: C003
    result: repair
    reason: 同时包含案例过程和独立判断，需要拆分
    repair_attempt: 1
  - candidate_id: C004
    result: reject
    reason: 普通词义，没有独立复用价值
    evidence_ids: [ev-021]
```

`publish_candidate` 通过最终审计后才能提交规范写入。写入门禁与第二阶段关系要求见 [wiki-schema.md](wiki-schema.md) 第 2、4、7 节。

## 5. 规范身份正反例

以下样例只约束处理过程。`same_object`、`different_object` 和 `review_required` 是服务端语义判定结果，Skill 只提供两侧身份特征和证据贡献。

### 5.1 类型装饰：同一对象

```yaml
candidate_title: Codex（实体）
canonical_title_suggestion: Codex
proposed_type: entity
identity_features:
  entity_sub_type: product
  referent: OpenAI Codex 编程产品
expected_server_decision: same_object
expected_existing_title: Codex
```

类型装饰必须在提交前清理。已有对象的规范标题和最终 ID 均由写入工具返回。

### 5.2 场景限定：单对象或拆分

正文只定义“第二大脑”时：

```yaml
candidate_title: AI Agent 第二大脑（概念）
canonical_title_suggestion: 第二大脑
identity_features:
  definition: 可调用知识并支持行动的个人知识系统
  mechanism: AI Agent 作为系统机制参与知识调用
expected_server_decision: same_object
expected_existing_title: 第二大脑
```

正文分别定义 AI Agent 和第二大脑时，输出两个候选并建立正式关系；不得再输出“AI Agent 第二大脑”第三个对象。证据不足以判断边界时输出 `review_required`。

### 5.3 字面不同：需比较实质

同义正例：

```yaml
left_title: 个人知识库五关标准
right_title: 个人知识库五大条件
comparison:
  type: methodology
  goal: 都用于判断个人知识库是否可用
  criteria_meaning: 五项条件含义一致，仅顺序和措辞不同
  applicability: 一致
expected_server_decision: same_object
```

近似但不同义的反例：

```yaml
left_title: 个人知识库五关标准
right_title: 个人知识库五大条件
comparison:
  type: methodology
  left_goal: 判断知识能否被检索、引用并转化为行动
  right_goal: 判断资料收集是否完整
  conflict_fields: [goal, criteria, output]
expected_server_decision: different_object
```

标题相似不能覆盖目标、判断条件或输出冲突。若现有页面同时命中两个不同规范身份，结果必须是 `review_required`。

## 6. 写入返回身份

```yaml
submission:
  candidate_id: V002-C001
  proposed_title: 示例研究院
result:
  status: reused
  knowledge_object_id: KO-018
  canonical_wiki_page_id: WP-018
  title: 示例研究院
  slug: entity/ko-018
```

关系、二次写入和视频索引只使用 `result` 中的身份。不得继续使用 `V002-C001` 或自行推导页面路径。
