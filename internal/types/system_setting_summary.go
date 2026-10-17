package types

// SystemSettingSummary is an assistant-safe projection using registry metadata.
type SystemSettingSummary struct {
	Key             string   `json:"key"                  extensions:"x-agent"`
	ValueJSON       string   `json:"value_json,omitempty" extensions:"x-agent,x-agent-max-runes=4096"`
	ValueHidden     bool     `json:"value_hidden"         extensions:"x-agent"`
	ValueType       string   `json:"value_type"           extensions:"x-agent"`
	Category        string   `json:"category"             extensions:"x-agent"`
	Description     string   `json:"description"          extensions:"x-agent"`
	RequiresRestart bool     `json:"requires_restart"     extensions:"x-agent"`
	Enum            []string `json:"enum,omitempty"       extensions:"x-agent"`
}
