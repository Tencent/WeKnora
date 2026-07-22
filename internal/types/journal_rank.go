package types

import "time"

// JournalRankMetadata is the cached UI-facing result of a journal ranking
// lookup. It is stored inside Knowledge.Metadata to avoid a schema migration.
type JournalRankMetadata struct {
	SchemaVersion int                        `json:"schema_version"`
	Publication   string                     `json:"publication"`
	MatchedAt     time.Time                  `json:"matched_at"`
	Source        string                     `json:"source"`
	Found         bool                       `json:"found"`
	Official      map[string]string          `json:"official,omitempty"`
	OfficialAll   map[string]string          `json:"official_all,omitempty"`
	Custom        []JournalRankCustomDataset `json:"custom,omitempty"`
}

type JournalRankCustomDataset struct {
	Label string `json:"label"`
	Level string `json:"level"`
}
