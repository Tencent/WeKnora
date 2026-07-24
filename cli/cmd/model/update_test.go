package modelcmd

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeUpdateSvc scripts GetModel (fetch baseline) and captures the
// UpdateModelRequest so tests can assert the surgical overlay.
type fakeUpdateSvc struct {
	cur    *sdk.Model
	gotReq *sdk.UpdateModelRequest
	gotID  string
}

func (f *fakeUpdateSvc) GetModel(_ context.Context, _ string) (*sdk.Model, error) {
	return f.cur, nil
}

func (f *fakeUpdateSvc) UpdateModel(_ context.Context, id string, req *sdk.UpdateModelRequest) (*sdk.Model, error) {
	f.gotID = id
	f.gotReq = req
	return &sdk.Model{ID: id, Name: req.Name, DisplayName: req.DisplayName, Parameters: req.Parameters, IsDefault: req.IsDefault}, nil
}

func baseModel() *sdk.Model {
	return &sdk.Model{
		ID: "mdl_x", Name: "keep-name", DisplayName: "old", Description: "olddesc",
		Parameters: sdk.ModelParameters{"base_url": "http://old", "api_key": "SECRET-OLD", "provider": "generic"},
		IsDefault:  false,
	}
}

// TestModelUpdate_SurgicalOverlay: only touched fields change; the rest (name,
// description, existing params) round-trip from the fetched baseline.
func TestModelUpdate_SurgicalOverlay(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{cur: baseModel()}
	opts := &UpdateOptions{DisplayName: "new-display", flags: modelUpdateFlags{displayName: true}}
	require.NoError(t, runUpdate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "mdl_x", nil))

	require.NotNil(t, svc.gotReq)
	assert.Equal(t, "mdl_x", svc.gotID, "id preserved (in-place update)")
	assert.Equal(t, "new-display", svc.gotReq.DisplayName, "display-name overlaid")
	assert.Equal(t, "keep-name", svc.gotReq.Name, "untouched name round-trips")
	assert.Equal(t, "olddesc", svc.gotReq.Description, "untouched description round-trips")
	assert.Equal(t, "http://old", svc.gotReq.Parameters["base_url"], "untouched params round-trip")
	assert.Equal(t, "SECRET-OLD", svc.gotReq.Parameters["api_key"], "existing key preserved when not rotating")
}

// TestModelUpdate_RotateKeyAndBaseURL: --api-key-stdin + --base-url overlay the
// parameters map; the new key comes from stdin, never argv.
func TestModelUpdate_RotateKeyAndBaseURL(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{cur: baseModel()}
	opts := &UpdateOptions{
		BaseURL: "http://new", APIKeyStdin: true,
		StdinReader: strings.NewReader("NEW-KEY\n"),
		flags:       modelUpdateFlags{baseURL: true},
	}
	require.NoError(t, runUpdate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "mdl_x", nil))
	assert.Equal(t, "http://new", svc.gotReq.Parameters["base_url"])
	assert.Equal(t, "NEW-KEY", svc.gotReq.Parameters["api_key"], "rotated key read from stdin")
}

// TestModelUpdate_RequiresAtLeastOneFlag: a bare `model update <id>` is rejected
// before any network call, matching agent update.
func TestModelUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	assert.False(t, modelUpdateHasFlag(&UpdateOptions{}))
	assert.True(t, modelUpdateHasFlag(&UpdateOptions{flags: modelUpdateFlags{displayName: true}}))
	assert.True(t, modelUpdateHasFlag(&UpdateOptions{APIKeyStdin: true}))
	assert.True(t, modelUpdateHasFlag(&UpdateOptions{Params: []string{"k=v"}}))
}
