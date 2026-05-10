package cmdutil

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/format"
)

func TestJSONExporter_WritesEnvelope(t *testing.T) {
	var buf bytes.Buffer
	exp := NewJSONExporter()
	require.NoError(t, exp.Write(&buf, format.Success(map[string]string{"k": "v"}, nil)))
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, true, got["ok"])
}

func TestFlagError_IsSentinel(t *testing.T) {
	err := NewFlagError(assert.AnError)
	_, ok := err.(*FlagError)
	assert.True(t, ok)
}
