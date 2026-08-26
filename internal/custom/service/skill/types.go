// Package skill 内容生产 skill 链定义与编排常量（CP-T004）。
//
// 5 个 skill 顺序固定（spec §3.1），依赖关系如下：
//   - extract-video-knowledge → 其他 4 个都依赖
//   - generate-transcript-outline → 依赖知识原子
//   - summarize-transcript-content → 依赖知识原子审计状态
//   - generate-typed-transcript-summary → 依赖知识原子 + 实体 + 关系
//   - assemble-transcript-page → 依赖前 4 个产物
package skill

import "strings"

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
	SkillExtractKnowledge = "extract-video-knowledge"
	SkillGenerateOutline  = "generate-transcript-outline"
	SkillSummarizeContent = "summarize-transcript-content"
	SkillTypedSummary     = "generate-typed-transcript-summary"
	SkillAssemblePage     = "assemble-transcript-page"
)

type JobContract struct {
	SkillName      string
	ArtifactType   string
	WikiPageTypes  []string
	SlugPrefixes   []string
	MatchVideoSlug bool
	VideoField     string
}

var JobContracts = map[string]JobContract{
	JobGraph: {
		SkillName:      SkillExtractKnowledge,
		ArtifactType:   "knowledge_base",
		WikiPageTypes:  []string{"index"},
		SlugPrefixes:   []string{"knowledge-base"},
		MatchVideoSlug: true,
		VideoField:     "knowledge_base_wiki_page_id",
	},
	JobOutline: {
		SkillName:     SkillGenerateOutline,
		ArtifactType:  "outline",
		WikiPageTypes: []string{"index"},
		SlugPrefixes:  []string{"outline"},
		VideoField:    "outline_wiki_page_id",
	},
	JobOverview: {
		SkillName:     SkillSummarizeContent,
		ArtifactType:  "overview",
		WikiPageTypes: []string{"index"},
		SlugPrefixes:  []string{"overview"},
		VideoField:    "overview_wiki_page_id",
	},
	JobSummary: {
		SkillName:     SkillTypedSummary,
		ArtifactType:  "typed_summary",
		WikiPageTypes: []string{"index"},
		SlugPrefixes:  []string{"typed-summary", "summary"},
		VideoField:    "summary_wiki_page_id",
	},
	JobAssemble: {
		SkillName:     SkillAssemblePage,
		ArtifactType:  "transcript_page",
		WikiPageTypes: []string{"index"},
		SlugPrefixes:  []string{"transcript-page", "transcript"},
		VideoField:    "transcript_page_wiki_page_id",
	},
}

// ChainOrder 串行触发顺序（每个 job 完成后由 orchestrator 调度下一个）
var ChainOrder = []string{JobGraph, JobOutline, JobOverview, JobSummary, JobAssemble}

func Contract(jobType string) (JobContract, bool) {
	contract, ok := JobContracts[jobType]
	return contract, ok
}

func NextJob(currentJobType string) string {
	for index, jobType := range ChainOrder {
		if jobType == currentJobType && index+1 < len(ChainOrder) {
			return ChainOrder[index+1]
		}
	}
	return ""
}

func (c JobContract) MatchesPageType(pageType string) bool {
	for _, allowed := range c.WikiPageTypes {
		if pageType == allowed {
			return true
		}
	}
	return false
}

func (c JobContract) MatchesSlug(slug, videoID string) bool {
	for _, prefix := range c.SlugPrefixes {
		if strings.HasPrefix(slug, prefix+"/") {
			return true
		}
	}
	if c.MatchVideoSlug {
		videoSlug := "video/" + videoID
		return slug == videoSlug || strings.HasPrefix(slug, videoSlug+"/")
	}
	return false
}

// IdempotencyKey 生成幂等键（CP-T004）
// 同一视频同一 job_type 重复触发幂等
func IdempotencyKey(videoID, jobType string) string {
	return jobType + ":" + videoID
}
