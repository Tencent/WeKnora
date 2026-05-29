package handler

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelUpdateRequestDisplayNamePresence(t *testing.T) {
	var omitted UpdateModelRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"gpt-4o"}`), &omitted))
	assert.Nil(t, omitted.DisplayName)

	var cleared UpdateModelRequest
	require.NoError(t, json.Unmarshal([]byte(`{"display_name":""}`), &cleared))
	require.NotNil(t, cleared.DisplayName)
	assert.Equal(t, "", *cleared.DisplayName)
}
