package pluginapi

// Pipeline hook stages (contributes.pipelineHooks[].stages).
const (
	StageRewriteQuery  = "rewriteQuery"
	StageFilterResults = "filterResults"
	StageAnswer        = "answer"
)

// HookPath is the endpoint of pipeline hook id at a stage.
func HookPath(id, stage string) string { return "/v1/hooks/" + id + "/" + stage }

// RewriteQueryInput is the question after WeKnora's own rewriting.
type RewriteQueryInput struct {
	// Query is what the user asked; RewrittenQuery what retrieval will use.
	Query          string `json:"query"`
	RewrittenQuery string `json:"rewrittenQuery"`
	SessionID      string `json:"sessionId,omitempty"`
}

// RewriteQueryOutput replaces the rewritten question; empty keeps it.
type RewriteQueryOutput struct {
	Query string `json:"query,omitempty"`
}

// FilterResultsInput is what retrieval found, best first.
type FilterResultsInput struct {
	Query   string            `json:"query"`
	Results []RetrievedResult `json:"results"`
}

// RetrievedResult is one retrieved passage.
type RetrievedResult struct {
	ID             string  `json:"id"`
	KnowledgeID    string  `json:"knowledgeId,omitempty"`
	KnowledgeTitle string  `json:"knowledgeTitle,omitempty"`
	Content        string  `json:"content"`
	Score          float64 `json:"score"`
}

// FilterResultsOutput keeps the results with these IDs, in this order. A
// hook can drop and reorder passages but not add any; nil keeps them all.
type FilterResultsOutput struct {
	Keep []string `json:"keep"`
}

// AnswerInput is the finished answer.
type AnswerInput struct {
	Query     string `json:"query"`
	Answer    string `json:"answer"`
	SessionID string `json:"sessionId,omitempty"`
}

// AnswerOutput adds Markdown after the answer, such as a disclaimer. The
// answer itself has already reached the user and cannot change.
type AnswerOutput struct {
	Append string `json:"append,omitempty"`
}
