// Package skill 内容生产 skill 链定义与编排常量（CP-T004）。
//
// 5 个 skill 顺序固定（spec §3.1），依赖关系如下：
//   - extract-video-knowledge → 其他 4 个都依赖
//   - generate-transcript-outline → 依赖知识原子
//   - summarize-transcript-content → 依赖知识原子审计状态
//   - generate-typed-transcript-summary → 依赖知识原子 + 实体 + 关系
//   - assemble-transcript-page → 依赖前 4 个产物
package skill

// JobType 5 个内容生产 job 类型（写入 video_processing_jobs.job_type）
const (
	JobGraph    = "graph"    // extract-video-knowledge
	JobOutline  = "outline"  // generate-transcript-outline
	JobOverview = "overview" // summarize-transcript-content
	JobSummary  = "summary"  // generate-typed-transcript-summary
	JobAssemble = "assemble" // assemble-transcript-page
)

// SkillName WeKnora skill 名称（传给 Agent Chat API 的 skill_names）
const (
	SkillExtractKnowledge  = "extract-video-knowledge"
	SkillGenerateOutline   = "generate-transcript-outline"
	SkillSummarizeContent  = "summarize-transcript-content"
	SkillTypedSummary      = "generate-typed-transcript-summary"
	SkillAssemblePage      = "assemble-transcript-page"
)

// SkillJobType 映射：job_type → skill name
var SkillJobType = map[string]string{
	JobGraph:    SkillExtractKnowledge,
	JobOutline:  SkillGenerateOutline,
	JobOverview: SkillSummarizeContent,
	JobSummary:  SkillTypedSummary,
	JobAssemble: SkillAssemblePage,
}

// JobSkillType 映射：job_type → 期望产物 frontmatter type（用于在 KB 中找新页）
var JobSkillType = map[string]string{
	JobGraph:    "knowledge_base",  // 知识底座索引页
	JobOutline:  "outline",
	JobOverview: "overview",
	JobSummary:  "typed_summary",
	JobAssemble: "transcript_page",
}

// VideoField 映射：job_type → videos 表回写字段
var VideoField = map[string]string{
	JobGraph:    "knowledge_base_wiki_page_id",
	JobOutline:  "outline_wiki_page_id",
	JobOverview: "overview_wiki_page_id",
	JobSummary:  "summary_wiki_page_id",
	JobAssemble: "transcript_page_wiki_page_id",
}

// ChainOrder 串行触发顺序（每个 job 完成后由 orchestrator 调度下一个）
var ChainOrder = []string{JobGraph, JobOutline, JobOverview, JobSummary, JobAssemble}

// IdempotencyKey 生成幂等键（CP-T004）
// 同一视频同一 job_type 重复触发幂等
func IdempotencyKey(videoID, jobType string) string {
	return jobType + ":" + videoID
}