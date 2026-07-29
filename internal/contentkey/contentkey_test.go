package contentkey

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNormalizeChunkContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "LF", input: "alpha\nbeta", want: "alpha\nbeta"},
		{name: "CRLF", input: "alpha\r\nbeta", want: "alpha\nbeta"},
		{name: "standalone CR", input: "alpha\rbeta", want: "alpha\nbeta"},
		{name: "Unicode NFD", input: "Cafe\u0301", want: "Café"},
		{name: "outer whitespace", input: " \t\nalpha\n\t ", want: "alpha"},
		{name: "internal spaces", input: "alpha  \t beta", want: "alpha  \t beta"},
		{name: "consecutive newlines", input: "alpha\n\n\nbeta", want: "alpha\n\n\nbeta"},
		{name: "Markdown table", input: "| A | B |\n|---|---|\n| 1 | 2 |", want: "| A | B |\n|---|---|\n| 1 | 2 |"},
		{name: "code block", input: "```go\nfunc  main() {}\n```", want: "```go\nfunc  main() {}\n```"},
		{name: "image reference", input: "before\n\n![alt](image.png)\n\nafter", want: "before\n\n![alt](image.png)\n\nafter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, NormalizeChunkContent(tt.input))
		})
	}
}

func TestChunkContentDigestNormalizationEquivalence(t *testing.T) {
	require.Equal(t, ChunkContentDigest(" Café\r\n"), ChunkContentDigest("Cafe\u0301\n"))
	require.NotEqual(t, ChunkContentDigest("Case"), ChunkContentDigest("case"))
	require.NotEqual(t, ChunkContentDigest("value."), ChunkContentDigest("value!"))
	require.NotEqual(t, ChunkContentDigest("a b"), ChunkContentDigest("a  b"))
}

func TestStableChunkID(t *testing.T) {
	base := testIdentity("content")
	first := StableChunkID(base)

	require.Equal(t, first, StableChunkID(base))
	parsed, err := uuid.Parse(first)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(5), parsed.Version())
	require.Len(t, first, 36)

	tests := []struct {
		name   string
		mutate func(*StableChunkIdentity)
	}{
		{name: "tenant", mutate: func(v *StableChunkIdentity) { v.TenantID++ }},
		{name: "knowledge", mutate: func(v *StableChunkIdentity) { v.KnowledgeID += "-other" }},
		{name: "chunk type", mutate: func(v *StableChunkIdentity) { v.ChunkType = "parent_text" }},
		{name: "parent", mutate: func(v *StableChunkIdentity) { v.ParentIdentity = "parent-id" }},
		{name: "content digest", mutate: func(v *StableChunkIdentity) { v.ContentDigest = ChunkContentDigest("other") }},
		{name: "context digest", mutate: func(v *StableChunkIdentity) { v.ContextDigest = ChunkContentDigest("section") }},
		{name: "ordinal", mutate: func(v *StableChunkIdentity) { v.DuplicateOrdinal++ }},
		{name: "normalization version", mutate: func(v *StableChunkIdentity) { v.NormalizationVersion += "-other" }},
		{name: "identity version", mutate: func(v *StableChunkIdentity) { v.IdentityVersion += "-other" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := base
			tt.mutate(&changed)
			require.NotEqual(t, first, StableChunkID(changed))
		})
	}
}

func TestStableChunkIDFieldBoundariesAreUnambiguous(t *testing.T) {
	left := testIdentity("content")
	left.KnowledgeID = "ab"
	left.ChunkType = "c"

	right := left
	right.KnowledgeID = "a"
	right.ChunkType = "bc"

	require.NotEqual(t, StableChunkID(left), StableChunkID(right))
}

func TestAssignDuplicateOrdinals(t *testing.T) {
	repeated := testIdentity("same")
	different := testIdentity("different")

	assigned := AssignDuplicateOrdinals([]StableChunkIdentity{
		repeated,
		different,
		repeated,
	})

	require.Equal(t, []int{0, 0, 1}, []int{
		assigned[0].DuplicateOrdinal,
		assigned[1].DuplicateOrdinal,
		assigned[2].DuplicateOrdinal,
	})
	require.NotEqual(t, StableChunkID(assigned[0]), StableChunkID(assigned[2]))

	repeatedRun := AssignDuplicateOrdinals([]StableChunkIdentity{
		repeated,
		different,
		repeated,
	})
	require.Equal(t, stableIDs(assigned), stableIDs(repeatedRun))
	require.Equal(t, 0, repeated.DuplicateOrdinal)
}

func TestAssignDuplicateOrdinalsIgnoresUnrelatedInsertion(t *testing.T) {
	repeated := testIdentity("same")
	different := testIdentity("different")

	withoutInsertion := AssignDuplicateOrdinals([]StableChunkIdentity{repeated, repeated})
	withInsertion := AssignDuplicateOrdinals([]StableChunkIdentity{repeated, different, repeated})

	require.Equal(t, StableChunkID(withoutInsertion[0]), StableChunkID(withInsertion[0]))
	require.Equal(t, StableChunkID(withoutInsertion[1]), StableChunkID(withInsertion[2]))
}

func TestAssignDuplicateOrdinalsEqualPrefixShiftsLaterOccurrences(t *testing.T) {
	repeated := testIdentity("same")

	before := AssignDuplicateOrdinals([]StableChunkIdentity{repeated, repeated})
	after := AssignDuplicateOrdinals([]StableChunkIdentity{repeated, repeated, repeated})

	require.Equal(t, 0, after[0].DuplicateOrdinal)
	require.Equal(t, StableChunkID(before[0]), StableChunkID(after[0]))
	require.Equal(t, StableChunkID(before[1]), StableChunkID(after[1]))
	require.NotEqual(t, StableChunkID(before[1]), StableChunkID(after[2]))
}

func TestAssignDuplicateOrdinalsParentScope(t *testing.T) {
	firstParent := testIdentity("child")
	firstParent.ParentIdentity = "parent-1"
	secondParent := firstParent
	secondParent.ParentIdentity = "parent-2"

	assigned := AssignDuplicateOrdinals([]StableChunkIdentity{
		firstParent,
		secondParent,
		firstParent,
	})

	require.Equal(t, []int{0, 0, 1}, []int{
		assigned[0].DuplicateOrdinal,
		assigned[1].DuplicateOrdinal,
		assigned[2].DuplicateOrdinal,
	})
	require.NotEqual(t, StableChunkID(assigned[0]), StableChunkID(assigned[1]))
}

func TestParentChildIdentityAssignment(t *testing.T) {
	parents := AssignDuplicateOrdinals([]StableChunkIdentity{
		testIdentityForType("same parent", "parent_text"),
		testIdentityForType("same parent", "parent_text"),
	})
	parentIDs := stableIDs(parents)
	require.NotEqual(t, parentIDs[0], parentIDs[1])

	childUnderFirst := testIdentity("same child")
	childUnderFirst.ParentIdentity = parentIDs[0]
	childUnderSecond := childUnderFirst
	childUnderSecond.ParentIdentity = parentIDs[1]

	children := AssignDuplicateOrdinals([]StableChunkIdentity{
		childUnderFirst,
		childUnderFirst,
		childUnderSecond,
	})
	childIDs := stableIDs(children)

	require.NotEqual(t, childIDs[0], childIDs[1])
	require.NotEqual(t, childIDs[0], childIDs[2])
	require.Equal(t, 0, children[0].DuplicateOrdinal)
	require.Equal(t, 1, children[1].DuplicateOrdinal)
	require.Equal(t, 0, children[2].DuplicateOrdinal)

	repeatedParents := AssignDuplicateOrdinals([]StableChunkIdentity{
		testIdentityForType("same parent", "parent_text"),
		testIdentityForType("same parent", "parent_text"),
	})
	require.Equal(t, parentIDs, stableIDs(repeatedParents))
}

func TestAssignChunkIdentitiesFlatChunks(t *testing.T) {
	children := []ChunkCandidate{
		{ChunkType: "text", Content: "same", ContextHeader: "# First", ParentIndex: -1},
		{ChunkType: "text", Content: "different", ParentIndex: -1},
		{ChunkType: "text", Content: "same", ContextHeader: "# First", ParentIndex: -1},
	}

	parents, first, err := AssignChunkIdentities(42, "knowledge-id", nil, children)
	require.NoError(t, err)
	require.Empty(t, parents)
	require.Len(t, first, 3)
	require.NotEqual(t, first[0].StableIdentity, first[2].StableIdentity)
	require.Equal(t, ChunkIdentityVersion, first[0].IdentityVersion)

	_, second, err := AssignChunkIdentities(42, "knowledge-id", nil, children)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestAssignChunkIdentitiesParentChild(t *testing.T) {
	parents := []ChunkCandidate{
		{ChunkType: "parent_text", Content: "parent"},
		{ChunkType: "parent_text", Content: "parent"},
	}
	children := []ChunkCandidate{
		{ChunkType: "text", Content: "child", ParentIndex: 0},
		{ChunkType: "text", Content: "child", ParentIndex: 0},
		{ChunkType: "text", Content: "child", ParentIndex: 1},
	}

	assignedParents, assignedChildren, err := AssignChunkIdentities(
		42,
		"knowledge-id",
		parents,
		children,
	)
	require.NoError(t, err)
	require.Len(t, assignedParents, 2)
	require.Len(t, assignedChildren, 3)
	require.NotEqual(t, assignedParents[0].StableIdentity, assignedParents[1].StableIdentity)
	require.NotEqual(t, assignedChildren[0].StableIdentity, assignedChildren[1].StableIdentity)
	require.NotEqual(t, assignedChildren[0].StableIdentity, assignedChildren[2].StableIdentity)
}

func TestAssignChunkIdentitiesRejectsInvalidParentIndex(t *testing.T) {
	_, _, err := AssignChunkIdentities(
		42,
		"knowledge-id",
		[]ChunkCandidate{{ChunkType: "parent_text", Content: "parent"}},
		[]ChunkCandidate{{ChunkType: "text", Content: "child", ParentIndex: 1}},
	)
	require.EqualError(t, err, "child 0 references parent index 1, but only 1 parents exist")
}

func TestStableChunkIdentityTitleIsNotAnInput(t *testing.T) {
	// StableChunkIdentity intentionally has no title field. Embedding inputs may
	// include a title, but database chunk identity does not.
	identity := testIdentity("content")
	require.Equal(t, StableChunkID(identity), StableChunkID(identity))
}

func testIdentity(content string) StableChunkIdentity {
	return testIdentityForType(content, "text")
}

func testIdentityForType(content, chunkType string) StableChunkIdentity {
	return StableChunkIdentity{
		TenantID:             42,
		KnowledgeID:          "knowledge-id",
		ChunkType:            chunkType,
		ContentDigest:        ChunkContentDigest(content),
		NormalizationVersion: ChunkNormalizationVersion,
		IdentityVersion:      ChunkIdentityVersion,
	}
}

func stableIDs(inputs []StableChunkIdentity) []string {
	result := make([]string, len(inputs))
	for i, input := range inputs {
		result[i] = StableChunkID(input)
	}
	return result
}
