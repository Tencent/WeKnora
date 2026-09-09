## 交付契约

所有 HTTP 路径均以 `/api/v1/learning` 开头，成功响应格式为 `{success:true,data:...}`。
仅支持 Web JWT 和当前工作区拥有的知识库。个人数据范围始终由已认证的 Caller 和 Principal 确定。

### HTTP 接口

- GET/PUT `/settings`；PUT 请求体 `{enabled:bool}`，返回 LearningSettings
- GET `/overview?knowledge_base_id=`，返回 LearningOverview
- GET `/nodes/:id`（页面 UUID），返回 LearningNodeView
- GET `/recommendations?knowledge_base_id=&limit=5`（最大 20），返回 LearningRecommendation[]
- POST `/nodes/:id/view`，返回 null
- POST `/overlay`；请求体 `{knowledge_base_id,slugs:[]}`（最大 2000），返回 LearningNodeView[]
- POST `/question-sets`；请求体 `{page_id}`，返回 LearningQuizView
- GET `/question-sets/:id`，返回 LearningQuizView
- POST `/attempts`；请求体 `{question_id,option_id,attempt_id}`，返回 LearningAnswerResult
- GET `/export?knowledge_base_id=`（KB 可选），返回 LearningExport
- DELETE `/profile?knowledge_base_id=`（KB 可选），返回 LearningClearResult

### DTO

```typescript
type LearningSettings = { enabled:boolean; algorithm_version:string }
type LearningMasteryView = {
  state:'unseen'|'learning'|'mastered'|'review_due'; p_mastery:number
  attempts:number; correct:number; consecutive_correct:number
  last_assessed_at?:string; next_review_at?:string; source_stale:boolean
}
type LearningNodeView = {
  page_id:string; knowledge_base_id:string; slug:string; title:string
  page_type:string; summary:string; familiar:boolean; mastery:LearningMasteryView
}
type LearningOverview = {
  enabled:boolean; algorithm_version:string; knowledge_base_id:string; total_nodes:number
  counts:{unseen:number; learning:number; mastered:number; review_due:number}
}
type LearningRecommendation = LearningNodeView & {
  score:number
  components:{review_need:number; graph_frontier:number; interest_match:number; content_quality:number}
  reason_codes:string[]
}
type LearningQuestionView = {
  id:string; prompt:string; options:{id:string;text:string}[]
  answered:boolean; result?:LearningAnswerResult
}
type LearningQuizView = {
  id:string; page_id:string; knowledge_base_id:string; slug:string; title:string
  status:'pending'|'running'|'ready'|'failed'|'stale'
  error_code?:string; questions:LearningQuestionView[]; algorithm_version:string
}
type LearningAnswerResult = {
  attempt_id:string; question_id:string; selected_option:string; correct:boolean
  correct_option:string; explanation:string
  evidence:{chunk_id:string;knowledge_id:string;quote:string}[]
  mastery:LearningMasteryView
}
type LearningClearResult = {deleted_attempts:number; deleted_mastery:number; deleted_quizzes:number}
```

### Go 服务

`learning.NewService` 返回 `interfaces.LearningService`。

```go
GetSettings(context.Context) (*types.LearningSettings, error)
SetEnabled(context.Context, bool) (*types.LearningSettings, error)
Overview(context.Context, string) (*types.LearningOverview, error)
Node(context.Context, string) (*types.LearningNodeView, error)
Recommend(context.Context, string, int) ([]*types.LearningRecommendation, error)
RecordView(context.Context, string) error
Overlay(context.Context, string, []string) ([]*types.LearningNodeView, error)
PrepareQuiz(context.Context, string) (*types.LearningQuizView, error)
GetQuiz(context.Context, string) (*types.LearningQuizView, error)
SubmitAnswer(context.Context, types.LearningAnswer) (*types.LearningAnswerResult, error)
Export(context.Context, string) (*types.LearningExport, error)
Clear(context.Context, string) (*types.LearningClearResult, error)
Handle(context.Context, *asynq.Task) error
Recover(context.Context) error
```

types 中的稳定错误：ErrLearningForbidden、ErrLearningDisabled、ErrLearningNotFound、ErrLearningStale、ErrLearningNotReady、ErrLearningConflict、ErrLearningInvalid、ErrLearningBusy、ErrLearningEvidence。

任务 `types.TypeLearningGenerate = "learning:generate"` 在同步调度器和 Asynq 调度器中均使用现有 QueueQuestion。生成和盲测验证调用使用 `types.WithLLMContentRedacted(ctx)`。新增迁移：PG 000092、SQLite 000014。

### 进度

- [x] 基线与 worktree。
- [x] 设计与跨模块契约。
- [x] 后端、迁移、算法和测试。
- [x] Wiki 重命名并保留身份标识。
- [x] HTTP、worker、恢复流程和 Agent 工具。
- [x] Wiki 面板、图谱叠加层、测验和隐私 UI。
- [x] 隔离运行时和真实模型验证。
- [x] PostgreSQL/race、前端和浏览器测试。
- [x] 可复现评估、runbook 和最终集成检查。

结果与复现命令见[验收报告](guided-learning-acceptance.md)。
