package agent

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestFormatKnowledgeBaseListRendersGeneratedProfile(t *testing.T) {
	kbs := []*KnowledgeBaseInfo{{
		ID: "kb-1", Name: "Ops", Type: "document", Description: "Manual description",
		Profile: &types.KnowledgeBaseProfile{
			Gist:             "Runbooks & guides for the <cluster>",
			Topics:           []string{"Kubernetes", "Ingress"},
			TypicalQuestions: []string{"How do I roll back a deploy?", "q2", "q3", "q4", "q5", "q6"},
		},
	}}
	text := formatKnowledgeBaseList(kbs)
	require.Contains(t, text, "<description>Manual description</description>")
	require.Contains(t, text, "<generated_profile>")
	require.Contains(t, text, "<gist>Runbooks &amp; guides for the &lt;cluster&gt;</gist>")
	require.Contains(t, text, "<topics>Kubernetes, Ingress</topics>")
	require.Contains(t, text, "<question>How do I roll back a deploy?</question>")
	require.NotContains(t, text, "<question>q6</question>", "questions are capped")
	decoder := xml.NewDecoder(strings.NewReader(text))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			break
		}
		require.NoError(t, err, "untrusted profile text must be escaped")
	}
}

func TestFormatKnowledgeBaseListOmitsEmptyProfile(t *testing.T) {
	text := formatKnowledgeBaseList([]*KnowledgeBaseInfo{
		{ID: "kb-1", Name: "Ops", Profile: &types.KnowledgeBaseProfile{Status: types.KnowledgeBaseProfileStatusEmpty}},
		{ID: "kb-2", Name: "Docs"},
	})
	require.NotContains(t, text, "<generated_profile>")
}
