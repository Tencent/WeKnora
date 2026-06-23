package doc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeCreateSvc captures call arguments and returns canned responses.
type fakeCreateSvc struct {
	resp *sdk.Knowledge
	err  error
	got  struct {
		kbID string
		req  *sdk.CreateManualKnowledgeRequest
	}
}

func (f *fakeCreateSvc) CreateManualKnowledge(
	_ context.Context,
	kbID string,
	req *sdk.CreateManualKnowledgeRequest,
) (*sdk.Knowledge, error) {
	f.got.kbID = kbID
	f.got.req = req
	return f.resp, f.err
}

func TestCreate_Success_Text(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeCreateSvc{resp: &sdk.Knowledge{ID: "doc_manual_1", Title: "Sprint Notes"}}
	opts := &CreateOptions{Text: "# Sprint Notes\n\nAction items: ...", Name: "Sprint Notes"}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx"))

	assert.Equal(t, "kb_xxx", svc.got.kbID)
	assert.Equal(t, "# Sprint Notes\n\nAction items: ...", svc.got.req.Content)
	assert.Equal(t, "Sprint Notes", svc.got.req.Title)
	assert.Contains(t, out.String(), "Created")
	assert.Contains(t, out.String(), "doc_manual_1")
}

func TestCreate_Success_NoName_FallsBackToTitle(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeCreateSvc{resp: &sdk.Knowledge{ID: "doc_manual_2", Title: "Server Title"}}
	opts := &CreateOptions{Text: "Some content"}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx"))
	// When --name is omitted the display falls back to k.Title from the server response.
	assert.Contains(t, out.String(), "Server Title")
}

func TestCreate_Success_NoName_NoTitle_FallsBackToID(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeCreateSvc{resp: &sdk.Knowledge{ID: "doc_manual_3"}}
	opts := &CreateOptions{Text: "Some content"}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx"))
	assert.Contains(t, out.String(), "doc_manual_3")
}

func TestCreate_TagID_Forwarded(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{resp: &sdk.Knowledge{ID: "doc_t"}}
	opts := &CreateOptions{Text: "content", TagID: "tag_42"}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx"))
	assert.Equal(t, "tag_42", svc.got.req.TagID)
}

func TestCreate_Channel_Override(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{resp: &sdk.Knowledge{ID: "doc_ch"}}
	opts := &CreateOptions{Text: "content", Channel: "browser_extension"}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx"))
	assert.Equal(t, "browser_extension", svc.got.req.Channel)
}

func TestCreate_Channel_DefaultIsAPI(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{resp: &sdk.Knowledge{ID: "doc_ch"}}
	opts := &CreateOptions{Text: "content"}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx"))
	assert.Equal(t, uploadChannel, svc.got.req.Channel)
}

func TestCreate_JSON_Envelope(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeCreateSvc{resp: &sdk.Knowledge{ID: "doc_manual_json", Title: "My Note"}}
	opts := &CreateOptions{Text: "# My Note", Name: "My Note"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	require.NoError(t, runCreate(context.Background(), opts, fopts, svc, "kb_xxx"))

	got := out.String()
	var env struct {
		OK   bool          `json:"ok"`
		Data sdk.Knowledge `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &env), "expected valid JSON envelope, got %q", got)
	assert.True(t, env.OK, "envelope.ok must be true")
	assert.Equal(t, "doc_manual_json", env.Data.ID, "envelope.data.id must match")
}

func TestCreate_ServerError_Wraps(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{err: errors.New("HTTP error 500: internal server error")}
	err := runCreate(context.Background(),
		&CreateOptions{Text: "content"}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestCreate_HTTPError_400_Wraps(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{err: errors.New("HTTP error 400: bad request")}
	err := runCreate(context.Background(),
		&CreateOptions{Text: "content"}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestCreate_EmptyText_RejectsBeforeSDK(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	// The SDK should never be called when --text is empty (runCreate has a
	// defensive guard; cobra's MarkFlagRequired catches it earlier in RunE).
	svc := &fakeCreateSvc{resp: &sdk.Knowledge{ID: "should-not-reach"}}
	err := runCreate(context.Background(),
		&CreateOptions{Text: ""}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx")
	require.Error(t, err)
	// Verify the SDK was NOT called.
	assert.Nil(t, svc.got.req, "SDK must not be called when --text is empty")
}

// TestCreate_AgentHelp_EmitsUsedFor verifies that when WEKNORA_AGENT_HELP=1
// the `doc create --help` path emits a JSON blob containing "used_for".
// This is the representative test for 3.2; the mechanism is covered by
// internal/cmdutil/agenthelp_test.go — we test the wiring here.
func TestCreate_AgentHelp_EmitsUsedFor(t *testing.T) {
	t.Setenv("WEKNORA_AGENT_HELP", "1")
	_, _ = iostreams.SetForTest(t)

	f := &cmdutil.Factory{}
	cmd := NewCmdCreate(f)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.HelpFunc()(cmd, nil)

	assert.Contains(t, buf.String(), `"used_for"`)
}
