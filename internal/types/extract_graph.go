package types

// ChunkContext represents chunk content with surrounding context
type ChunkContext struct {
	ChunkID     string `json:"chunk_id"`
	Content     string `json:"content"`
	PrevContent string `json:"prev_content,omitempty"` // Previous chunk content for context
	NextContent string `json:"next_content,omitempty"` // Next chunk content for context
}

// PromptTemplateStructured represents the prompt template structured
type PromptTemplateStructured struct {
	Description string      `json:"description"`
	Tags        []string    `json:"tags"`
	Examples    []GraphData `json:"examples"`
}

type GraphNode struct {
	Name       string   `json:"name,omitempty"`
	Chunks     []string `json:"chunks,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

// GraphRelation represents the relation of the graph
type GraphRelation struct {
	Node1 string `json:"node1,omitempty"`
	Node2 string `json:"node2,omitempty"`
	Type  string `json:"type,omitempty"`
}

type GraphData struct {
	Text        string            `json:"text,omitempty"`
	Node        []*GraphNode      `json:"node,omitempty"`
	Relation    []*GraphRelation  `json:"relation,omitempty"`
	Diagnostics *GraphDiagnostics `json:"-"`
}

// MaxGraphFailureDetails bounds the diagnostic payload persisted in a
// processing span. Counters always contain the full totals; only the example
// failure list is capped so one noisy LLM response cannot create a huge row.
const MaxGraphFailureDetails = 20

// GraphFailureDetail describes one item that could not be retained or
// written. Stage is currently "validation" or "neo4j_write"; Kind is
// "item", "node", or "relation".
type GraphFailureDetail struct {
	Stage     string `json:"stage"`
	Kind      string `json:"kind"`
	ItemIndex int    `json:"item_index"`
	Name      string `json:"name,omitempty"`
	Node1     string `json:"node1,omitempty"`
	Node2     string `json:"node2,omitempty"`
	Type      string `json:"type,omitempty"`
	Reason    string `json:"reason"`
}

// GraphDiagnostics records tolerant parser decisions. Duplicate entities and
// relationships are counted as merges rather than failures because they are
// normal, lossless normalization.
type GraphDiagnostics struct {
	ItemsReceived      int                  `json:"items_received"`
	ItemsRejected      int                  `json:"items_rejected"`
	NodesExtracted     int                  `json:"nodes_extracted"`
	NodesAccepted      int                  `json:"nodes_accepted"`
	NodesRejected      int                  `json:"nodes_rejected"`
	NodesMerged        int                  `json:"nodes_merged"`
	RelationsExtracted int                  `json:"relations_extracted"`
	RelationsAccepted  int                  `json:"relations_accepted"`
	RelationsRejected  int                  `json:"relations_rejected"`
	RelationsMerged    int                  `json:"relations_merged"`
	Failures           []GraphFailureDetail `json:"failures,omitempty"`
}

// AddFailure keeps full failure counters outside this helper while capping
// only the human-readable examples stored in the trace.
func (d *GraphDiagnostics) AddFailure(detail GraphFailureDetail) {
	if d == nil || len(d.Failures) >= MaxGraphFailureDetails {
		return
	}
	d.Failures = append(d.Failures, detail)
}

func (d *GraphDiagnostics) HasFailures() bool {
	return d != nil && d.ItemsRejected > 0
}

// GraphWriteResult reports what was actually committed to Neo4j. A non-nil
// result may accompany an error, allowing callers to retain statistics for
// batches committed before an infrastructure failure.
type GraphWriteResult struct {
	NodesAttempted     int                  `json:"nodes_attempted"`
	NodesWritten       int                  `json:"nodes_written"`
	NodesFailed        int                  `json:"nodes_failed"`
	RelationsAttempted int                  `json:"relations_attempted"`
	RelationsWritten   int                  `json:"relations_written"`
	RelationsFailed    int                  `json:"relations_failed"`
	Failures           []GraphFailureDetail `json:"failures,omitempty"`
}

func (r *GraphWriteResult) AddFailure(detail GraphFailureDetail) {
	if r == nil || len(r.Failures) >= MaxGraphFailureDetails {
		return
	}
	r.Failures = append(r.Failures, detail)
}

func (r *GraphWriteResult) HasFailures() bool {
	return r != nil && (r.NodesFailed > 0 || r.RelationsFailed > 0)
}

// NameSpace represents the name space of the knowledge base and knowledge
type NameSpace struct {
	KnowledgeBase string `json:"knowledge_base"`
	Knowledge     string `json:"knowledge"`
}

// Labels returns the labels of the name space
func (n NameSpace) Labels() []string {
	res := make([]string, 0)
	if n.KnowledgeBase != "" {
		res = append(res, n.KnowledgeBase)
	}
	if n.Knowledge != "" {
		res = append(res, n.Knowledge)
	}
	return res
}
