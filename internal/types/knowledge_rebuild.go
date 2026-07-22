package types

import (
	"fmt"
	"strings"
)

const (
	RebuildStageDocReader   = StageDocReader
	RebuildStageChunking    = StageChunking
	RebuildStageEmbedding   = StageEmbedding
	RebuildStageMultimodal  = StageMultimodal
	RebuildStagePostProcess = StagePostProcess

	RebuildStageSummary     = "summary"
	RebuildStageQuestions   = "questions"
	RebuildStageGraph       = "graph"
	RebuildStageWiki        = "wiki"
	RebuildStageJournalRank = "journal_rank"
	RebuildStageContentType = "content_type"
)

var rebuildStageOrder = []string{
	RebuildStageDocReader,
	RebuildStageChunking,
	RebuildStageEmbedding,
	RebuildStageMultimodal,
	RebuildStagePostProcess,
	RebuildStageSummary,
	RebuildStageQuestions,
	RebuildStageGraph,
	RebuildStageWiki,
	RebuildStageJournalRank,
	RebuildStageContentType,
}

var postProcessStageOrder = []string{
	RebuildStageSummary,
	RebuildStageQuestions,
	RebuildStageGraph,
	RebuildStageWiki,
	RebuildStageJournalRank,
	RebuildStageContentType,
}

// KnowledgeRebuildPlan is the normalized execution contract for one rebuild.
// An omitted request is deliberately represented as Full to preserve the
// historical /reparse behavior for existing callers.
type KnowledgeRebuildPlan struct {
	Full              bool
	Stages            []string
	PostProcessStages []string
}

func (p KnowledgeRebuildPlan) Includes(stage string) bool {
	for _, selected := range p.Stages {
		if selected == stage {
			return true
		}
	}
	return false
}

func (p KnowledgeRebuildPlan) IncludesPostProcess(stage string) bool {
	for _, selected := range p.PostProcessStages {
		if selected == stage {
			return true
		}
	}
	return false
}

// PlanKnowledgeRebuild validates requested stages and expands dependencies.
// DocReader, chunking and multimodal currently require the transient parser
// output, so selecting any of them safely expands to the existing full parse.
func PlanKnowledgeRebuild(requested []string) (KnowledgeRebuildPlan, error) {
	if len(requested) == 0 {
		return fullKnowledgeRebuildPlan(), nil
	}

	known := make(map[string]struct{}, len(rebuildStageOrder))
	for _, stage := range rebuildStageOrder {
		known[stage] = struct{}{}
	}
	selected := make(map[string]struct{}, len(requested))
	for _, raw := range requested {
		stage := strings.ToLower(strings.TrimSpace(raw))
		if _, ok := known[stage]; !ok {
			return KnowledgeRebuildPlan{}, fmt.Errorf("unsupported rebuild stage %q", raw)
		}
		selected[stage] = struct{}{}
	}

	for _, transient := range []string{RebuildStageDocReader, RebuildStageChunking, RebuildStageMultimodal} {
		if _, ok := selected[transient]; ok {
			return fullKnowledgeRebuildPlan(), nil
		}
	}

	postSelected := false
	if _, ok := selected[RebuildStagePostProcess]; ok {
		postSelected = true
		for _, stage := range postProcessStageOrder {
			selected[stage] = struct{}{}
		}
	}
	for _, stage := range postProcessStageOrder {
		if _, ok := selected[stage]; ok {
			postSelected = true
		}
	}
	if postSelected {
		selected[RebuildStagePostProcess] = struct{}{}
	}

	if _, embedding := selected[RebuildStageEmbedding]; !embedding && !postSelected {
		return KnowledgeRebuildPlan{}, fmt.Errorf("at least one executable rebuild stage is required")
	}

	return KnowledgeRebuildPlan{
		Stages:            orderedSelected(selected, rebuildStageOrder[:5]),
		PostProcessStages: orderedSelected(selected, postProcessStageOrder),
	}, nil
}

func fullKnowledgeRebuildPlan() KnowledgeRebuildPlan {
	return KnowledgeRebuildPlan{
		Full:              true,
		Stages:            append([]string(nil), rebuildStageOrder[:5]...),
		PostProcessStages: append([]string(nil), postProcessStageOrder...),
	}
}

func orderedSelected(selected map[string]struct{}, order []string) []string {
	var result []string
	for _, stage := range order {
		if _, ok := selected[stage]; ok {
			result = append(result, stage)
		}
	}
	return result
}
