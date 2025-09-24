package types

const (
	TypeChunkExtract = "chunk:extract"
)

type ExtractChunkPayload struct {
	TenantID uint   `json:"tenant_id"`
	ChunkID  string `json:"chunk_id"`
	ModelID  string `json:"model_id"`
}

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

type GraphRelation struct {
	Node1 string `json:"node1,omitempty"`
	Node2 string `json:"node2,omitempty"`
	Type  string `json:"type,omitempty"`
}

type GraphData struct {
	Text     string           `json:"text,omitempty"`
	Node     []*GraphNode     `json:"node,omitempty"`
	Relation []*GraphRelation `json:"relation,omitempty"`
}

type NameSpace struct {
	KnowledgeBase string `json:"knowledge_base"`
	Knowledge     string `json:"knowledge"`
}

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
