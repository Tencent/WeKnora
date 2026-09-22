package types

// RecalcTotal sets Total to the sum of the classified buckets.
func (u *ContextUsage) RecalcTotal() {
	if u == nil {
		return
	}
	u.Total = u.SystemPrompt + u.Memory + u.Skills + u.Tools + u.MCP +
		u.Conversation + u.Reasoning + u.ToolResults
}

// Calibrate proportionally scales the classified buckets so they sum to the
// provider's prompt_tokens. Estimates stay as-is when the provider did not
// report a prompt count.
func (u *ContextUsage) Calibrate(promptTokens int) {
	if u == nil {
		return
	}
	u.RecalcTotal()
	if promptTokens <= 0 || u.Total <= 0 {
		return
	}
	type part struct {
		val  *int
		orig int
	}
	parts := []part{
		{&u.SystemPrompt, u.SystemPrompt},
		{&u.Tools, u.Tools},
		{&u.Conversation, u.Conversation},
		{&u.MCP, u.MCP},
		{&u.Skills, u.Skills},
	}
	allocated := 0
	maxI := 0
	for i := range parts {
		*parts[i].val = parts[i].orig * promptTokens / u.Total
		allocated += *parts[i].val
		if parts[i].orig > parts[maxI].orig {
			maxI = i
		}
	}
	*parts[maxI].val += promptTokens - allocated
	if *parts[maxI].val < 0 {
		*parts[maxI].val = 0
	}
	u.Total = promptTokens
}
