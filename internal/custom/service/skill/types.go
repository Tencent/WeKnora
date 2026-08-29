// Package skill defines the content jobs and their artifact contracts.
package skill

import "strings"

// JobType 5 个内容生产 job 类型（写入 video_processing_jobs.job_type）
const (
	JobGraph          = "graph"   // extract-video-knowledge
	JobOutline        = "outline" // generate-transcript-outline
	JobSummary        = "summary" // generate-typed-transcript-summary
	JobSummaryEnhance = "summary_enhance"
	JobAssemble       = "assemble" // assemble-transcript-page
)

// SkillName WeKnora skill 名称（传给 Agent Chat API 的 skill_names）
const (
	SkillExtractKnowledge = "extract-video-knowledge"
	SkillGenerateOutline  = "generate-transcript-outline"
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
	JobSummary: {
		SkillName:     SkillTypedSummary,
		ArtifactType:  "typed_summary",
		WikiPageTypes: []string{"index"},
		SlugPrefixes:  []string{"typed-summary", "summary"},
		VideoField:    "summary_wiki_page_id",
	},
	JobSummaryEnhance: {
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

var FoundationJobs = []string{JobOutline, JobSummary}
var EnhancementJobs = []string{JobGraph, JobSummaryEnhance}

func Contract(jobType string) (JobContract, bool) {
	contract, ok := JobContracts[jobType]
	return contract, ok
}

func NextJob(currentJobType string) string {
	// Jobs are independently triggered after the transcript generation is activated.
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

func (c JobContract) WriteSlug(videoID string) string {
	if c.MatchVideoSlug {
		return "video/" + videoID
	}
	if len(c.SlugPrefixes) == 0 {
		return ""
	}
	return c.SlugPrefixes[0] + "/" + videoID
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
