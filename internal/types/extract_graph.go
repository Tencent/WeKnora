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
	Examples    []GraphData `json:"examples"`
}

type GraphNode struct {
	ID         string            `json:"id,omitempty"`
	ChunkIDs   []string          `json:"chunk_ids,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type GraphRelation struct {
	Source     *GraphNode        `json:"source,omitempty"`
	Target     *GraphNode        `json:"target,omitempty"`
	Type       string            `json:"type,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
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

func DefaultTemplate() PromptTemplateStructured {
	return PromptTemplateStructured{
		Description: "依据给定文本，按逻辑顺序提取关键信息作为实体，全面补充实体的详细属性。在此基础上，准确提取实体间有效关系，并根据需要补充实体相关属性，确保信息完整、准确且逻辑清晰。" +
			"同时，务必准确识别出关系涉及的两个主体，明确它们分别是谁。",
		Examples: []GraphData{{
			Text: "《红楼梦》，又名《石头记》，是清代作家曹雪芹创作的中国古典四大名著之一，被誉为中国封建社会的百科全书。该书前80回由曹雪芹所著，后40回一般认为是高鹗所续。小说以贾、史、王、薛四大家族的兴衰为背景，以贾宝玉、林黛玉和薛宝钗的爱情悲剧为主线，刻画了以贾宝玉和金陵十二钗为中心的正邪两赋、贤愚并出的高度复杂的人物群像。成书于乾隆年间（1743年前后），是中国文学史上现实主义的高峰，对后世影响深远。",
			Node: []*GraphNode{
				{
					ID: "红楼梦",
					Attributes: map[string]string{
						"作者": "曹雪芹",
						"地位": "中国古典四大名著之一",
					},
				},
				{
					ID: "曹雪芹",
					Attributes: map[string]string{
						"职业": "作者",
						"介绍": "曹雪芹是清代作家，红楼梦的作者，创作了前80回",
					},
				},
			},
			Relation: []*GraphRelation{
				{
					Source: &GraphNode{
						ID: "红楼梦",
					},
					Target: &GraphNode{
						ID: "曹雪芹",
					},
					Type: "作者与作品",
					Attributes: map[string]string{
						"关系": "曹雪芹是红楼梦的主要作者，创作了前80回",
					},
				},
			},
		}},
	}
}
