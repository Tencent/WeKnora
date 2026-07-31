package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// f is a compact builder for subtree-resolution tests.
type f struct {
	id       string
	parentID string
}

func mkFolders(kbID string, fs []f) []*types.DocumentFolder {
	out := make([]*types.DocumentFolder, 0, len(fs))
	for _, x := range fs {
		out = append(out, &types.DocumentFolder{
			ID: x.id, KnowledgeBaseID: kbID, ParentID: x.parentID,
			Name: x.id, Path: x.id,
		})
	}
	return out
}

// TestResolveSubtreeFolderIDs_RootIncludesSelfAndDescendants confirms the BFS
// returns the root plus every descendant in its subtree — and only its
// subtree. Tree:
//
//	root
//	├─ a
//	│  ├─ a1
//	│  └─ a2
//	└─ b
//	   └─ b1
//
// Querying root returns {root,a,a1,a2,b,b1}; querying a returns {a,a1,a2};
// querying b returns {b,b1}; querying a1 returns {a1}.
func TestResolveSubtreeFolderIDs_RootIncludesSelfAndDescendants(t *testing.T) {
	all := mkFolders("kb-1", []f{
		{"root", ""},
		{"a", "root"},
		{"a1", "a"},
		{"a2", "a"},
		{"b", "root"},
		{"b1", "b"},
	})

	cases := []struct {
		name   string
		rootID string
		want   []string
	}{
		{"root subtree", "root", []string{"root", "a", "a1", "a2", "b", "b1"}},
		{"a subtree", "a", []string{"a", "a1", "a2"}},
		{"b subtree", "b", []string{"b", "b1"}},
		{"leaf a1", "a1", []string{"a1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSubtreeFolderIDs(all, tc.rootID)
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.want, got)
		})
	}
}

// TestResolveSubtreeFolderIDs_SiblingNotIncluded is the no-sibling-leak
// invariant — the core correctness property of the scalar folder_id design.
// Querying folder "a" must never include "b" or "b1", and querying "b" must
// never include "a" or its descendants, even though they share root as an
// ancestor.
func TestResolveSubtreeFolderIDs_SiblingNotIncluded(t *testing.T) {
	all := mkFolders("kb-1", []f{
		{"root", ""},
		{"a", "root"},
		{"a1", "a"},
		{"b", "root"},
		{"b1", "b"},
	})

	got, err := resolveSubtreeFolderIDs(all, "a")
	require.NoError(t, err)
	assert.NotContains(t, got, "b")
	assert.NotContains(t, got, "b1")
	assert.NotContains(t, got, "root") // root is parent, not descendant
}

// TestResolveSubtreeFolderIDs_RootMissing tests the fail-closed guard for a
// root ID not in the folder list.
func TestResolveSubtreeFolderIDs_RootMissing(t *testing.T) {
	all := mkFolders("kb-1", []f{{"a", ""}})
	_, err := resolveSubtreeFolderIDs(all, "missing")
	assert.ErrorIs(t, err, repository.ErrDocumentFolderNotFound)
}

// TestResolveSubtreeFolderIDs_CycleDetected verifies the BFS cycle guard
// raises a sentinel error rather than looping forever. A cycle in the data
// indicates a corruption bug — we surface it rather than spin.
func TestResolveSubtreeFolderIDs_CycleDetected(t *testing.T) {
	// x -> y -> x
	all := mkFolders("kb-1", []f{
		{"x", "y"},
		{"y", "x"},
	})
	_, err := resolveSubtreeFolderIDs(all, "x")
	assert.ErrorIs(t, err, ErrFolderCycleInData)
}

// TestResolveSubtreeFolderIDs_ExceedsLimit verifies the MaxFoldersPerKB guard
// fires when the subtree itself (not the whole KB) exceeds the cap.
func TestResolveSubtreeFolderIDs_ExceedsLimit(t *testing.T) {
	// Build a flat subtree under root with MaxFoldersPerKB+1 children —
	// resolving root must hit the limit.
	fs := make([]f, 0, types.MaxFoldersPerKB+2)
	fs = append(fs, f{"root", ""})
	for i := 0; i < types.MaxFoldersPerKB+1; i++ {
		// ids must be unique; use a stable suffix
		fs = append(fs, f{fmtID("c", i), "root"})
	}
	all := mkFolders("kb-1", fs)
	_, err := resolveSubtreeFolderIDs(all, "root")
	assert.ErrorIs(t, err, ErrFolderLimitExceeded)
}

// fmtID builds "c-0000" style ids so the limit test stays readable. Kept tiny
// and local; tests that need more varied ids can build their own.
func fmtID(prefix string, i int) string {
	return prefix + "-" + pad4(i)
}

func pad4(i int) string {
	// hard-coded padding up to 9999 is plenty for MaxFoldersPerKB=5000
	s := []byte{'0', '0', '0', '0'}
	b := i
	for j := 3; j >= 0 && b > 0; j-- {
		s[j] = byte('0' + b%10)
		b /= 10
	}
	return string(s)
}
