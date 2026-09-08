## Delivery Contract

All HTTP paths begin `/api/v1/learning`. Success envelope: `{success:true,data:...}`.
Web JWT only, current workspace owned KBs only. Personal scope always comes from authenticated Caller and Principal.

### HTTP

- GET/PUT `/settings`, PUT body `{enabled:bool}` -> LearningSettings
- GET `/overview?knowledge_base_id=` -> LearningOverview
- GET `/nodes/:id` (page UUID) -> LearningNodeView
- GET `/recommendations?knowledge_base_id=&limit=5` (max20) -> LearningRecommendation[]
- POST `/nodes/:id/view` -> null
- POST `/overlay` body `{knowledge_base_id,slugs:[]}` (max2000) -> LearningNodeView[]
- POST `/question-sets` body `{page_id}` -> LearningQuizView
- GET `/question-sets/:id` -> LearningQuizView
- POST `/attempts` body `{question_id,option_id,attempt_id}` -> LearningAnswerResult
- GET `/export?knowledge_base_id=` (optional KB) -> LearningExport
- DELETE `/profile?knowledge_base_id=` (optional KB) -> LearningClearResult

### DTOs

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

### Go Service

`learning.NewService` returns `interfaces.LearningService`.

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

Stable errors in types: ErrLearningForbidden, ErrLearningDisabled, ErrLearningNotFound, ErrLearningStale, ErrLearningNotReady, ErrLearningConflict, ErrLearningInvalid, ErrLearningBusy, ErrLearningEvidence.
Task `types.TypeLearningGenerate = "learning:generate"` uses existing QueueQuestion in both synchronous and Asynq dispatchers.
Generation and blind-verification calls use `types.WithLLMContentRedacted(ctx)`.
New migrations: PG 000092, SQLite 000014.

### Progress

- [x] Baseline and worktree.
- [x] Design and cross-module contracts.
- [x] Backend, migrations, algorithms and tests.
- [x] Wiki rename preserving identity.
- [x] HTTP, workers, recovery, Agent tools.
- [x] Wiki panel, graph overlay, quiz and privacy UI.
- [x] Isolated runtime and real model validation.
- [x] PostgreSQL/race, frontend and browser tests.
- [x] Reproducible evaluation and runbook, final integration checks.

Results and reproduction commands: [acceptance report](guided-learning-acceptance.md).
