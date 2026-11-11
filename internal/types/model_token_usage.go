package types

import "time"

// ModelTokenUsage records the usage observed for one chat model invocation.
type ModelTokenUsage struct {
	ID           string `gorm:"primaryKey"`
	TenantID     uint64
	ModelID      string
	ModelName    string
	StartedAt    time.Time
	UsageDate    string
	UsageKnown   bool
	Completed    bool
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}
