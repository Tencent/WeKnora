// Package config loads WeKnora runtime configuration.
package config

import "github.com/Tencent/WeKnora/internal/types"

// ResolveCustomAgentPrompts resolves inherited references at request time. Explicit
// text always wins, including legacy saved prompts; never rewrite user content.
// References are scoped to their field/mode so a context template cannot become
// system instructions just because it has a matching ID.
func (c *Config) ResolveCustomAgentPrompts(agent *types.CustomAgent) (system, context string) {
	if agent == nil {
		return "", ""
	}
	system, context = agent.Config.SystemPrompt, agent.Config.ContextTemplate
	if c == nil || c.PromptTemplates == nil {
		return
	}
	resolve := func(body, id string, templates []PromptTemplate) string {
		if body != "" || id == "" {
			return body
		}
		for _, template := range templates {
			if template.ID == id {
				return template.Content
			}
		}
		return ""
	}
	list := c.PromptTemplates.SystemPrompt
	if agent.IsAgentMode() {
		list = c.PromptTemplates.AgentSystemPrompt
	}
	system = resolve(system, agent.Config.SystemPromptID, list)
	context = resolve(context, agent.Config.ContextTemplateID, c.PromptTemplates.ContextTemplate)
	return
}
