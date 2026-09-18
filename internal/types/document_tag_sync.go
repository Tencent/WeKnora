package types

// An empty KnowledgeIDs list requests full bootstrap. Force also permits a
// manual full repair of an initialized KB without resetting its ready marker.
type DocumentTagSyncPayload struct {
	TenantID        uint64   `json:"tenant_id"`
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	KnowledgeIDs    []string `json:"knowledge_ids,omitempty"`
	Force           bool     `json:"force,omitempty"`
}
