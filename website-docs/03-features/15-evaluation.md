# 评估能力（Evaluation）

换个向量模型、开不开重排、分块调大一点——这些改动到底有没有让效果变好？评估能力就是用来回答这个问题的：准备一份带标准答案的 QA 数据集，WeKnora 会自动建一个临时知识库灌进语料，逐题跑完整的检索 + 生成流程，最后给出一组可比较的分数（检索侧 Precision / Recall / NDCG / MRR / MAP，生成侧 BLEU / ROUGE）。

::: tip 目前只有 API
评估暂时没有独立的界面入口，通过 `POST /api/v1/evaluation` 发起、`GET /api/v1/evaluation?task_id=...` 轮询结果，需要 Admin 权限。数据集是 Parquet 格式，格式要求见下文。
:::

用法建议：固定数据集，每次只改一个变量（比如只换 embedding 模型），对比同一组指标，否则分数变化归因不清。

## API

`internal/router/router.go`：

```go
evaluationRoutes := g.apiKeyGroup(r.Group("/evaluation"), apiKeyRunEvaluations(apiKeyFullAccess()))
{
    evaluationRoutes.POST("", g.Admin(), handler.Evaluation)
    evaluationRoutes.GET("", g.Viewer(), handler.GetEvaluationResult)
}
```

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/v1/evaluation` | Admin（API Key 需 `RunEvaluations` 能力） | 创建评估任务，立即返回任务信息 |
| GET | `/api/v1/evaluation?task_id=...` | Viewer | 查询任务状态、进度与指标结果 |

### 创建评估任务

请求参数（`internal/handler/evaluation.go`）：

```go
type EvaluationRequest struct {
    DatasetID       string `json:"dataset_id"`        // 数据集 ID，默认 "default"
    KnowledgeBaseID string `json:"knowledge_base_id"` // 参考知识库（复用其配置）
    ChatModelID     string `json:"chat_id"`           // 聊天模型
    RerankModelID   string `json:"rerank_id"`         // 重排模型
}
```

| 参数 | 必填 | 默认行为 |
| --- | --- | --- |
| `dataset_id` | 否 | 缺省使用内置 `default` 数据集（`dataset/samples/`） |
| `knowledge_base_id` | 否 | 未提供则新建评估专用知识库；提供则复制其配置创建评估 KB |
| `chat_id` | 否 | 缺省自动选择默认 Chat 模型 |
| `rerank_id` | 否 | 缺省自动选择默认 Rerank 模型 |

任务 ID 由 `utils.GenerateTaskID("evaluation", tenantID, datasetID)` 生成，格式为
`evaluation_<tenantID>_<Unix 毫秒时间戳>_<8 位通用唯一标识符片段>_<datasetID>`。通用唯一标识符
（Universally Unique Identifier，UUID）片段与时间戳共同保证同一租户、同一数据集的多次任务具有不同 ID。
任务对象（`internal/types/evaluation.go`）：

```go
type EvaluationTask struct {
    ID        string           `json:"id"`
    TenantID  uint64           `json:"tenant_id"`
    DatasetID string           `json:"dataset_id"`
    StartTime time.Time        `json:"start_time"`
    Status    EvaluationStatue `json:"status"`
    ErrMsg    string           `json:"err_msg,omitempty"`
    Total     int              `json:"total,omitempty"`    // 样本总数
    Finished  int              `json:"finished,omitempty"` // 已完成数
}
```

任务状态枚举（注意源码中拼写为 `EvaluationStatue`）：

```go
const (
    EvaluationStatuePending EvaluationStatue = iota // 0 待启动
    EvaluationStatueRunning                          // 1 运行中
    EvaluationStatueSuccess                          // 2 成功
    EvaluationStatueFailed                           // 3 失败
)
```

## 评估流程

`internal/application/service/evaluation.go` 中，POST 接口**同步完成准备、异步执行评估**：

1. **知识库准备**：新建（或按参考 KB 配置克隆）评估专用知识库，取默认 Embedding 与 LLM 模型；
2. **参数装配**：从系统配置装配 `ChatManage` 评估参数——`VectorThreshold`、`KeywordThreshold`、`EmbeddingTopK`、`RerankTopK`、`RerankThreshold`、`MaxRounds`、`SummaryConfig`（MaxTokens / TopK / TopP / RepeatPenalty / Prompt / ContextTemplate 等）、`FallbackResponse`、改写提示词等；
3. **任务注册**：以任务 ID 注册到内存存储，状态 `Pending`，立即返回响应；
4. **后台执行**（goroutine）：将数据集 corpus 灌入评估 KB → 并行评估每个 QA 对 → 汇聚指标 → 清理资源。

并发度取 `max(GOMAXPROCS - 1, 1)`（errgroup 限流）：

```go
var g errgroup.Group
metricHook := NewHookMetric(len(dataset), knowledge.ID)
g.SetLimit(max(runtime.GOMAXPROCS(0)-1, 1))
for i, qaPair := range dataset {
    g.Go(func() error {
        // 1. 克隆 ChatManage 配置
        // 2. 走 KnowledgeQAByEvent 完整管道（检索 + 重排 + 生成）
        // 3. 记录 MetricInput（检索到的 passage ID、生成文本、GT）
        // 4. 加锁更新 finished 进度
    })
}
g.Wait()
```

每个样本产出一个 `MetricInput`（`internal/types/evaluation.go`）：

```go
type MetricInput struct {
    RetrievalGT    [][]int // 检索 ground truth（相关 passage ID 列表）
    RetrievalIDs   []int   // 实际检索返回的 passage ID
    GeneratedTexts string  // 模型生成文本
    GeneratedGT    string  // 参考答案
}
```

`metric_hook.go` 对每个样本遍历所有已注册指标计算器求分，最终 `Avg()` 对全部样本逐指标取均值，写入 `MetricResult`。

::: warning RetrievalIDs 的来源契约
评估流程先由 `getPassageList` 建立按数据集 passage ID（PID）索引的稀疏 passage 切片，再把切片索引依次写入 `ParsedChunk.Seq` 和 `Chunk.ChunkIndex`。因此，只有检索结果的 `KnowledgeID` 等于本次评估临时知识 ID 且 `ChunkIndex >= 0` 时，`recordFinish` 才将 `ChunkIndex` 作为 PID。该对应关系只适用于评估专用的受控 passage 入库流程。

`recordFinish` 优先使用非空重排结果，重排结果为空时使用原始检索结果。每个结果均保留原始排名位置；来源不属于本次临时知识、索引无效或 PID 重复的结果按未命中计分。该规则使 Precision、NDCG、MRR 和 MAP 的输入排名与检索管道输出一致。
:::

语料灌入也必须**同步等待索引完成**（`CreateKnowledgeFromPassageSync`）：异步入库时评估查询会跑在索引建好之前，同样表现为指标恒为 0。另外 passage 列表按 `maxPID + 1` 分配长度，pid 是 0-based 且包含末位。

### 评估流程图

```mermaid
flowchart TD
    A["POST /api/v1/evaluation<br/>(dataset_id, knowledge_base_id, chat_id, rerank_id)"] --> B["创建评估专用知识库<br/>(新建或克隆参考 KB 配置)"]
    B --> C["装配 ChatManage 评估参数<br/>(阈值 / TopK / Summary 配置)"]
    C --> D["注册任务到内存存储<br/>ID 含租户、时间戳、UUID 片段和数据集，状态 Pending"]
    D --> E["立即返回任务信息"]
    D --> F["goroutine 后台执行, 状态 Running"]
    F --> G["加载 Parquet 数据集<br/>queries / corpus / qrels / answers / qas"]
    G --> H["按 PID 建立稀疏 passage 切片<br/>同步灌入并完成索引"]
    H --> I["errgroup 并行处理 QA 对<br/>并发 = max(CPU-1, 1)"]
    I --> J["每个问题跑 KnowledgeQAByEvent<br/>检索 + 重排 + 生成"]
    J --> K["校验 KnowledgeID + ChunkIndex<br/>保留未知来源和重复 PID 的未命中排名"]
    K --> L["MetricList.Avg 汇聚 12 项指标均值"]
    L --> M["写回 EvaluationDetail, 状态 Success / Failed<br/>清理评估知识库"]
    M --> N["GET /api/v1/evaluation?task_id=...<br/>轮询进度与指标"]
```

## 指标清单

指标注册表见 `internal/application/service/metric_hook.go`，共 12 项，分两组。文本先经 `metric/common.go` 分词：中文用 Jieba 分词、英文按空白切分、按 `。` / `.` 切句。

### 检索指标（Retrieval Metrics）

| 指标 | 字段 | 实现文件 | 含义 |
| --- | --- | --- | --- |
| Precision | `precision` | `metric/precision.go` | 检索准确率：命中的相关文档数 / 检索返回总数，按 GT 集合求均值 |
| Recall | `recall` | `metric/recall.go` | 检索召回率：命中的相关文档数 / 相关文档总数 |
| NDCG@3 | `ndcg3` | `metric/ndcg.go` | 归一化折损累计增益（取前 3 位），奖励把相关文档排在前面 |
| NDCG@10 | `ndcg10` | `metric/ndcg.go` | 同上，取前 10 位 |
| MRR | `mrr` | `metric/mrr.go` | 首个相关文档倒数排名的平均：`sum(1/rank) / N` |
| MAP | `map` | `metric/map.go` | 平均精度均值：对每个命中位置累计 `Precision@k` 再归一化 |

NDCG 核心计算（`metric/ndcg.go`）：

```go
// DCG = sum((2^rel_i - 1) / log2(i+2))，rel 为 0/1
dcg += (math.Pow(2, float64(relevance)) - 1) / math.Log2(float64(i+2))
// NDCG = DCG / IDCG（理想排序的 DCG）
```

MRR 核心计算（`metric/mrr.go`）：

```go
for i, predID := range ids {
    if _, ok := gtSet[predID]; ok {
        sumRR += 1.0 / float64(i+1) // 第一个命中位置的倒数
        break
    }
}
```

### 生成指标（Generation Metrics）

| 指标 | 字段 | 实现文件 | 含义 |
| --- | --- | --- | --- |
| BLEU-1 | `bleu1` | `metric/bleu.go` | 1-gram 精度（权重 `[1.0, 0, 0, 0]`） |
| BLEU-2 | `bleu2` | `metric/bleu.go` | 1/2-gram 各 50%（权重 `[0.5, 0.5, 0, 0]`） |
| BLEU-4 | `bleu4` | `metric/bleu.go` | 1~4-gram 均权（`[0.25, 0.25, 0.25, 0.25]`），含 brevity penalty |
| ROUGE-1 | `rouge1` | `metric/rouge.go` | 一元词重叠 F1 |
| ROUGE-2 | `rouge2` | `metric/rouge.go` | 二元词组重叠 F1 |
| ROUGE-L | `rougel` | `metric/rouge.go` | 最长公共子序列（LCS）F1 |

BLEU 核心（`metric/bleu.go`）：修正 n-gram 精度的加权几何平均乘以简短惩罚 `bp * exp(sum(w_i * log(p_i)))`。ROUGE 取 F1：`F1 = 2PR / (P + R + 1e-8)`（`metric/rouge_score.go`）。

## 数据集格式

数据集服务（`internal/application/service/dataset.go`）从 `./dataset/samples/` 加载 5 个 **Parquet** 文件：

| 文件 | Schema | 含义 |
| --- | --- | --- |
| `queries.parquet` | `id: int64, text: string` | 问题集合 |
| `corpus.parquet` | `id: int64, text: string` | 语料段落（评估时灌入知识库） |
| `answers.parquet` | `id: int64, text: string` | 参考答案 |
| `qrels.parquet` | `qid: int64, pid: int64` | 问题 → 相关段落的 ground truth 关联（检索指标依据） |
| `qas.parquet` | `qid: int64, aid: int64` | 问题 → 答案映射（生成指标依据） |

对应的 Go 结构体：

```go
type TextInfo struct {
    ID   int64  `parquet:"id"`
    Text string `parquet:"text"`
}
type RelsInfo struct {
    QID int64 `parquet:"qid"`
    PID int64 `parquet:"pid"`
}
type QaInfo struct {
    QID int64 `parquet:"qid"`
    AID int64 `parquet:"aid"`
}
```

加载后拼装为逐样本的 `QAPair`（`internal/types/dataset.go`）：

```go
type QAPair struct {
    QID      int      // 问题 ID
    Question string   // 问题文本
    PIDs     []int    // 相关段落 ID（ground truth）
    Passages []string // 段落文本
    AID      int      // 答案 ID
    Answer   string   // 参考答案文本
}
```

`DatasetService.GetDatasetByID` 当前固定从 `dataset/samples/` 加载上述五个 Parquet 文件，`dataset_id` 用于任务标识，尚未参与数据目录选择。加载时服务会打印问题数、语料数、平均相关段落数和答案覆盖率等统计信息。

## 结果查询

`GET /api/v1/evaluation?task_id=evaluation_1_1787880000000_a1b2c3d4_default` 返回 `EvaluationDetail`：

```json
{
  "success": true,
  "data": {
    "task": {
      "id": "evaluation_1_1787880000000_a1b2c3d4_default",
      "dataset_id": "default",
      "status": 2,
      "total": 100,
      "finished": 100
    },
    "params": { "...": "ChatManage 评估参数快照" },
    "metric": {
      "retrieval_metrics": {
        "precision": 0.85, "recall": 0.92,
        "ndcg3": 0.88, "ndcg10": 0.86,
        "mrr": 0.95, "map": 0.87
      },
      "generation_metrics": {
        "bleu1": 0.72, "bleu2": 0.65, "bleu4": 0.58,
        "rouge1": 0.78, "rouge2": 0.71, "rougel": 0.75
      }
    }
  }
}
```

任务运行期间可轮询该接口获取 `finished / total` 进度；`status = 3` 时 `err_msg` 携带失败原因。

> **注意**：评估结果存储在**内存**（`evaluationMemoryStorage`：`map[string]*EvaluationDetail` + `sync.RWMutex`，见 `internal/application/service/evaluation.go`），服务重启后任务与结果会丢失，需重新发起评估。

## 实现参考

想读源码时按下表定位（路径相对仓库根目录）：

| 层 | 文件 |
| --- | --- |
| HTTP Handler | `internal/handler/evaluation.go` |
| 评估服务 | `internal/application/service/evaluation.go` |
| 指标注册与汇聚 | `internal/application/service/metric_hook.go` |
| 指标实现 | `internal/application/service/metric/`（`precision.go`、`recall.go`、`ndcg.go`、`mrr.go`、`map.go`、`bleu.go`、`rouge.go`、`rouge_score.go`、`common.go`） |
| 数据集加载 | `internal/application/service/dataset.go`、`internal/handler/dataset.go` |
| 类型定义 | `internal/types/evaluation.go`、`internal/types/dataset.go` |
| 内置样例数据集 | `dataset/samples/`（Parquet 文件） |
| 路由注册 | `internal/router/router.go` 的 `RegisterEvaluationRoutes` |
