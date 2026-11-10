package cmdutil

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestSetRisk_WritesAnnotations(t *testing.T) {
	cmd := &cobra.Command{Use: "delete"}
	SetRisk(cmd, "kb.delete")
	assert.Equal(t, "destructive", cmd.Annotations["risk.level"])
	assert.Equal(t, "kb.delete", cmd.Annotations["risk.action"])
}

func TestSetRisk_NilMapGuard(t *testing.T) {
	cmd := &cobra.Command{Use: "delete"}
	// cmd.Annotations is nil by default
	assert.Nil(t, cmd.Annotations)
	SetRisk(cmd, "agent.delete")
	// Should not panic; should initialize map
	assert.NotNil(t, cmd.Annotations)
	assert.Equal(t, "agent.delete", cmd.Annotations["risk.action"])
}

func TestGetRisk_ReturnsWritten(t *testing.T) {
	cmd := &cobra.Command{Use: "delete"}
	SetRisk(cmd, "doc.delete")
	level, action, ok := GetRisk(cmd)
	assert.True(t, ok)
	assert.Equal(t, "destructive", level)
	assert.Equal(t, "doc.delete", action)
}

func TestGetRisk_NotFound(t *testing.T) {
	cmd := &cobra.Command{Use: "list"}
	level, action, ok := GetRisk(cmd)
	assert.False(t, ok)
	assert.Empty(t, level)
	assert.Empty(t, action)
}

func TestGetRisk_NilAnnotationsMap(t *testing.T) {
	cmd := &cobra.Command{Use: "list"}
	// Annotations is nil — should not panic, return ok=false
	assert.Nil(t, cmd.Annotations)
	_, _, ok := GetRisk(cmd)
	assert.False(t, ok)
}

func TestRiskDestructive_Const(t *testing.T) {
	assert.Equal(t, "destructive", RiskDestructive)
}
