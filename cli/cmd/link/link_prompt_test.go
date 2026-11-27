package linkcmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

type promptKBLister struct {
	kbs []sdk.KnowledgeBase
}

func (l promptKBLister) ListKnowledgeBases(context.Context) ([]sdk.KnowledgeBase, error) {
	return l.kbs, nil
}

type fixedPrompter struct{ answer string }

func (p fixedPrompter) Input(string, string) (string, error) { return p.answer, nil }
func (p fixedPrompter) Password(string) (string, error)      { return "", nil }
func (p fixedPrompter) Confirm(string, bool) (bool, error)   { return false, nil }

func promptFactory(answer string) *cmdutil.Factory {
	return &cmdutil.Factory{
		Prompter: func() prompt.Prompter { return fixedPrompter{answer: answer} },
	}
}

// Knowledge base names are not unique, so an interactive name that matches
// more than one must fail instead of binding the project link to whichever
// row the server listed first (same rule as `--kb`, #6037).
func TestPromptForKBRejectsADuplicateName(t *testing.T) {
	iostreams.SetForTest(t)
	lister := promptKBLister{kbs: []sdk.KnowledgeBase{
		{ID: "id-a", Name: "prod"},
		{ID: "id-b", Name: "prod"},
	}}

	_, _, err := promptForKB(context.Background(), lister, promptFactory("prod"))
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	require.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	require.Contains(t, typed.Message, "2 knowledge bases")
	require.NotEmpty(t, typed.Hint)
}

func TestPromptForKBPrefersAnExactIDWhenTheNameCollides(t *testing.T) {
	iostreams.SetForTest(t)
	lister := promptKBLister{kbs: []sdk.KnowledgeBase{
		{ID: "id-a", Name: "prod"},
		{ID: "prod", Name: "other"},
	}}

	id, name, err := promptForKB(context.Background(), lister, promptFactory("prod"))
	require.NoError(t, err)
	require.Equal(t, "prod", id)
	require.Equal(t, "other", name)
}

func TestPromptForKBResolvesAUniqueName(t *testing.T) {
	iostreams.SetForTest(t)
	lister := promptKBLister{kbs: []sdk.KnowledgeBase{
		{ID: "id-a", Name: "prod"},
		{ID: "id-b", Name: "staging"},
	}}

	id, name, err := promptForKB(context.Background(), lister, promptFactory("staging"))
	require.NoError(t, err)
	require.Equal(t, "id-b", id)
	require.Equal(t, "staging", name)
}

func TestPromptForKBUnknownAnswer(t *testing.T) {
	iostreams.SetForTest(t)
	lister := promptKBLister{kbs: []sdk.KnowledgeBase{{ID: "id-a", Name: "prod"}}}

	_, _, err := promptForKB(context.Background(), lister, promptFactory("missing"))
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	require.Equal(t, cmdutil.CodeKBNotFound, typed.Code)
}
