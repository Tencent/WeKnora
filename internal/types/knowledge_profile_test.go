package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeProfileNormalizeBoundsAndDedupes(t *testing.T) {
	p := (&KnowledgeProfile{
		Gist: "  A   guide to   Kubernetes networking  ",
		Topics: []string{
			"Kubernetes", " kubernetes ", "Networking.", "", "CNI", "Ingress", "Service mesh", "extra",
		},
		DocType:         " user manual ",
		TypicalQuestion: "How do pods talk to each other?",
	}).Normalize()
	require.NotNil(t, p)
	assert.Equal(t, "A guide to Kubernetes networking", p.Gist)
	assert.Equal(t, []string{"Kubernetes", "Networking.", "CNI", "Ingress", "Service mesh"}, p.Topics,
		"case-insensitive duplicates collapse and the list is capped at five")
	assert.Equal(t, "user manual", p.DocType)
}

func TestKnowledgeProfileNormalizeEmptyBecomesNil(t *testing.T) {
	assert.Nil(t, (&KnowledgeProfile{Topics: []string{"  ", "..."}}).Normalize())
	assert.Nil(t, (*KnowledgeProfile)(nil).Normalize())
	assert.True(t, (*KnowledgeProfile)(nil).IsEmpty())
}

func TestTopicKeyFoldsCaseWhitespaceAndPunctuation(t *testing.T) {
	assert.Equal(t, "kubernetes", TopicKey(" Kubernetes. "))
	assert.Equal(t, "service mesh", TopicKey("Service   Mesh!"))
	assert.Equal(t, "", TopicKey("..."))
	assert.NotEqual(t, TopicKey("K8s"), TopicKey("Kubernetes"), "synonyms are left to the model")
}

func TestKnowledgeProfileCloneIsIndependent(t *testing.T) {
	assert.Nil(t, (*KnowledgeProfile)(nil).Clone())
	src := &KnowledgeProfile{Gist: "g", Topics: []string{"a", "b"}}
	dst := src.Clone()
	dst.Topics[0] = "changed"
	assert.Equal(t, "a", src.Topics[0])
	assert.Equal(t, "g", dst.Gist)
}

func TestKnowledgeProfileScanRoundTrip(t *testing.T) {
	src := KnowledgeProfile{Gist: "g", Topics: []string{"a"}, DocType: "t", TypicalQuestion: "q"}
	raw, err := src.Value()
	require.NoError(t, err)
	var dst KnowledgeProfile
	require.NoError(t, dst.Scan(raw))
	assert.Equal(t, src, dst)
	var untouched KnowledgeProfile
	require.NoError(t, untouched.Scan(nil))
	assert.True(t, untouched.IsEmpty())
}

func profileRow(
	id, title, fileType, folder string, day int, profile *KnowledgeProfile, tags ...string,
) *KnowledgeProfileRow {
	return &KnowledgeProfileRow{
		ID: id, Title: title, FileType: fileType, FolderPath: folder,
		CreatedAt: time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC),
		Profile:   profile, Tags: tags,
	}
}

func TestBuildKnowledgeBaseProfileAggregateCountsAndSamples(t *testing.T) {
	rows := []*KnowledgeProfileRow{
		profileRow("d1", "Cluster setup", "pdf", "guides/k8s", 1, &KnowledgeProfile{
			Gist: "Setting up a cluster", Topics: []string{"Kubernetes", "Networking"},
			DocType: "user manual", TypicalQuestion: "How do I set up a cluster?",
		}, "Ops"),
		profileRow("d2", "Ingress guide", "md", "guides", 3, &KnowledgeProfile{
			Gist: "Ingress routing", Topics: []string{"kubernetes.", "Ingress"},
			DocType: "User Manual", TypicalQuestion: "How does ingress routing work?",
		}, "Ops", "Networking"),
		profileRow("d3", "Untitled scan", "pdf", "", 2, nil),
		profileRow("d4", "", "docx", "policies", 4, &KnowledgeProfile{
			Gist: "Leave policy", Topics: []string{"HR"}, DocType: "policy",
			TypicalQuestion: "How many leave days do I get?",
		}),
	}
	rows[3].FileName = "leave.docx"

	agg := BuildKnowledgeBaseProfileAggregate(rows)
	require.NotNil(t, agg)
	assert.False(t, agg.IsEmpty())
	assert.Equal(t, 4, agg.Stats.DocumentCount)
	assert.Equal(t, 3, agg.Stats.ProfiledCount)
	assert.Equal(t, []NamedCount{{"pdf", 2}, {"docx", 1}, {"md", 1}}, agg.Stats.FileTypes)
	assert.Equal(t, []NamedCount{{"Ops", 2}, {"Networking", 1}}, agg.Stats.Tags)
	assert.Equal(t, []string{"guides", "policies"}, agg.Stats.Folders)
	assert.Equal(t, []NamedCount{{"user manual", 2}, {"policy", 1}}, agg.Stats.DocTypes,
		"doc types are folded case-insensitively, first spelling wins")
	require.Len(t, agg.Stats.RawTopics, 4)
	assert.Equal(t, NamedCount{"Kubernetes", 2}, agg.Stats.RawTopics[0],
		"'kubernetes.' folds into 'Kubernetes' and the pair leads by count")
	assert.Equal(t, "2026-01-01", agg.Stats.EarliestAt.Format("2006-01-02"))
	assert.Equal(t, "2026-01-04", agg.Stats.LatestAt.Format("2006-01-02"))
	assert.Equal(t, []string{"Cluster setup", "Ingress guide", "Untitled scan", "leave.docx"}, agg.SampleTitles,
		"a document without a title contributes its file name")
	assert.Equal(t, []string{
		"How do I set up a cluster?", "How many leave days do I get?", "How does ingress routing work?",
	}, agg.SampleQuestions,
		"one question per topic per round: both Kubernetes documents attach to that bucket, "+
			"so HR's question comes before the second Kubernetes one")
	assert.Len(t, agg.Hash, 64)
}

func TestBuildKnowledgeBaseProfileAggregateHashIsOrderIndependentAndDeletionSensitive(t *testing.T) {
	a := profileRow("d1", "A", "pdf", "", 1, &KnowledgeProfile{Gist: "a", Topics: []string{"x"}})
	b := profileRow("d2", "B", "pdf", "", 2, &KnowledgeProfile{Gist: "b", Topics: []string{"y"}})
	h1 := BuildKnowledgeBaseProfileAggregate([]*KnowledgeProfileRow{a, b}).Hash
	h2 := BuildKnowledgeBaseProfileAggregate([]*KnowledgeProfileRow{b, a}).Hash
	assert.Equal(t, h1, h2, "row order must not change the hash")

	deleted := BuildKnowledgeBaseProfileAggregate([]*KnowledgeProfileRow{a}).Hash
	assert.NotEqual(t, h1, deleted, "removing a document changes the hash")

	retitled := profileRow("d2", "B renamed", "pdf", "", 2, &KnowledgeProfile{Gist: "b", Topics: []string{"y"}})
	assert.NotEqual(t, h1, BuildKnowledgeBaseProfileAggregate([]*KnowledgeProfileRow{a, retitled}).Hash)

	reprofiled := profileRow("d2", "B", "pdf", "", 2, &KnowledgeProfile{Gist: "b", Topics: []string{"z"}})
	assert.NotEqual(t, h1, BuildKnowledgeBaseProfileAggregate([]*KnowledgeProfileRow{a, reprofiled}).Hash)

	tagged := profileRow("d2", "B", "pdf", "", 2, &KnowledgeProfile{Gist: "b", Topics: []string{"y"}}, "T")
	assert.NotEqual(t, h1, BuildKnowledgeBaseProfileAggregate([]*KnowledgeProfileRow{a, tagged}).Hash)
}

func TestBuildKnowledgeBaseProfileAggregateEmpty(t *testing.T) {
	agg := BuildKnowledgeBaseProfileAggregate(nil)
	assert.True(t, agg.IsEmpty())
	assert.Equal(t, agg.Hash, BuildKnowledgeBaseProfileAggregate([]*KnowledgeProfileRow{}).Hash)
	assert.Nil(t, agg.SampleTitles)
}

func TestSpreadSampleCoversWholeRange(t *testing.T) {
	items := make([]string, 100)
	for i := range items {
		items[i] = string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	sample := spreadSample(items, 10)
	require.Len(t, sample, 10)
	assert.Equal(t, items[0], sample[0])
	assert.Equal(t, items[90], sample[9], "the last pick comes from the tail, not the head")
	assert.Equal(t, items[:3], spreadSample(items[:3], 10))
}

func TestKnowledgeBaseProfileReadyAndNormalize(t *testing.T) {
	var nilProfile *KnowledgeBaseProfile
	assert.False(t, nilProfile.IsReady("h"))
	assert.False(t, nilProfile.HasText())

	p := &KnowledgeBaseProfile{Status: KnowledgeBaseProfileStatusReady, AggregateHash: "h", Gist: " x "}
	assert.True(t, p.IsReady("h"))
	assert.True(t, p.IsReady(""))
	assert.False(t, p.IsReady("other"))
	p.Status = KnowledgeBaseProfileStatusFailed
	assert.False(t, p.IsReady("h"))
	assert.True(t, p.HasText())

	long := &KnowledgeBaseProfile{
		Topics:           []string{"a", "A", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"},
		TypicalQuestions: []string{"q1", "Q1", "q2", "q3", "q4", "q5", "q6"},
	}
	long.Normalize()
	assert.Len(t, long.Topics, 10)
	assert.Equal(t, []string{"q1", "q2", "q3", "q4", "q5"}, long.TypicalQuestions)
}

func TestKnowledgeBaseProfileConfigIsEnabledNilSafe(t *testing.T) {
	var cfg *KnowledgeBaseProfileConfig
	assert.False(t, cfg.IsEnabled())
	assert.True(t, (&KnowledgeBaseProfileConfig{Enabled: true}).IsEnabled())
}

func TestEnsureDefaultsClearsProfileConfigForNonDocumentKB(t *testing.T) {
	kb := &KnowledgeBase{Type: KnowledgeBaseTypeFAQ, ProfileConfig: &KnowledgeBaseProfileConfig{Enabled: true}}
	kb.EnsureDefaults()
	assert.Nil(t, kb.ProfileConfig)
	doc := &KnowledgeBase{Type: KnowledgeBaseTypeDocument, ProfileConfig: &KnowledgeBaseProfileConfig{Enabled: true}}
	doc.EnsureDefaults()
	assert.NotNil(t, doc.ProfileConfig)
}
