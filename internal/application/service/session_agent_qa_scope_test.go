package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolvePerRequestMCPScope_SelectedIntersection(t *testing.T) {
	effective, mode := resolvePerRequestMCPScope(
		[]string{"mcp-b", "mcp-c"},
		[]string{"mcp-a", "mcp-b"},
		"selected",
		false,
	)
	assert.Equal(t, "selected", mode)
	assert.Equal(t, []string{"mcp-b"}, effective)
}

func TestResolvePerRequestMCPScope_SelectedRejectsOutsidePreset(t *testing.T) {
	effective, mode := resolvePerRequestMCPScope(
		[]string{"mcp-x"},
		[]string{"mcp-a"},
		"selected",
		false,
	)
	assert.Empty(t, effective)
	assert.Equal(t, "selected", mode)
}

func TestResolvePerRequestMCPScope_NoneRejectsMention(t *testing.T) {
	effective, mode := resolvePerRequestMCPScope(
		[]string{"mcp-iwiki"},
		nil,
		"none",
		false,
	)
	assert.Empty(t, effective)
	assert.Equal(t, "none", mode)
}

func TestResolvePerRequestMCPScope_SharedAgentBlocksOutsidePreset(t *testing.T) {
	effective, mode := resolvePerRequestMCPScope(
		[]string{"mcp-x"},
		[]string{"mcp-a"},
		"all",
		true,
	)
	assert.Empty(t, effective)
	assert.Equal(t, "all", mode)
}

func TestResolvePerRequestMCPScope_SharedAgentAllowsPreset(t *testing.T) {
	effective, mode := resolvePerRequestMCPScope(
		[]string{"mcp-a", "mcp-x"},
		[]string{"mcp-a", "mcp-b"},
		"all",
		true,
	)
	assert.Equal(t, "selected", mode)
	assert.Equal(t, []string{"mcp-a"}, effective)
}
