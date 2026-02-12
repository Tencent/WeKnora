package types

import "time"

// Episode represents a conversation episode or a distinct interaction event
type Episode struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

// MemoryContext represents the retrieved memory context for a conversation
type MemoryContext struct {
	RelatedEpisodes []Episode      `json:"related_episodes"`
	RelatedEntities []Entity       `json:"related_entities"`
	RelatedRelations []Relationship `json:"related_relations"`
}
