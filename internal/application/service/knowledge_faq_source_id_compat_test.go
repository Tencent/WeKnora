package service

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// The similar-question source ID suffix switched from MD5 (first 4 bytes) to
// SHA-256 (first 8 bytes). Vectors written before the upgrade keep the old
// suffix, so every delete/update path must cover both hashes; otherwise the
// old vectors survive and keep answering retrieval.
func TestFAQSimilarQuestionSourceIDsCoverLegacyHash(t *testing.T) {
	const chunkID = "chunk-123"
	const question = "如何重置密码？"

	current := fmt.Sprintf("%s-%s", chunkID, hashQuestion(question))
	ids := faqSimilarQuestionSourceIDs(chunkID, question)

	if len(ids) != 2 {
		t.Fatalf("faqSimilarQuestionSourceIDs returned %d ids, want 2: %v", len(ids), ids)
	}
	if ids[0] != current {
		t.Fatalf("ids[0] = %q, want current hash %q", ids[0], current)
	}

	// The second ID must be exactly what the pre-upgrade code wrote, so a
	// delete that covers both hashes also removes pre-upgrade vectors.
	legacyMD5 := md5.Sum([]byte(question))
	wantLegacy := fmt.Sprintf("%s-%s", chunkID, hex.EncodeToString(legacyMD5[:4]))
	if ids[1] != wantLegacy {
		t.Fatalf("ids[1] = %q, want legacy MD5 id %q", ids[1], wantLegacy)
	}
	if ids[0] == ids[1] {
		t.Fatal("current and legacy source IDs must differ")
	}
}

func TestHashQuestionLegacyMatchesPreUpgradeAlgorithm(t *testing.T) {
	// Pin the documented pre-upgrade algorithm: MD5, first 4 bytes, hex.
	question := "支持哪些文件格式？"
	sum := md5.Sum([]byte(question))
	want := hex.EncodeToString(sum[:4])
	if got := hashQuestionLegacy(question); got != want {
		t.Fatalf("hashQuestionLegacy = %q, want %q", got, want)
	}
	if len(hashQuestionLegacy(question)) != 8 {
		t.Fatalf("legacy hash length = %d, want 8 hex chars", len(hashQuestionLegacy(question)))
	}
	// The two hashes must not collide for realistic inputs.
	if hashQuestionLegacy(question) == hashQuestion(question) {
		t.Fatal("legacy hash unexpectedly equals the current hash")
	}
}

// Upgrade scenario pinned end-to-end at the ID level: a similar question
// indexed before the upgrade (legacy ID) must be deletable and updatable
// after the upgrade.
func TestFAQUpgradeScenarioLegacyIDIsCovered(t *testing.T) {
	const chunkID = "chunk-abc"
	const oldQuestion = "旧版本添加的相似问"

	// What the pre-upgrade incremental index wrote:
	legacyID := fmt.Sprintf("%s-%s", chunkID, hashQuestionLegacy(oldQuestion))

	// After the upgrade the same question is removed: the delete list must
	// contain the legacy ID alongside the current one.
	all := strings.Join(faqSimilarQuestionSourceIDs(chunkID, oldQuestion), ",")
	if !strings.Contains(all, legacyID) {
		t.Fatalf("delete coverage %q does not include the pre-upgrade id %q", all, legacyID)
	}

	// After the upgrade the same question's answers change: the update path
	// also schedules the legacy ID for deletion (the upsert writes the
	// current-hash ID), which is what the helper's IDs describe.
	current := fmt.Sprintf("%s-%s", chunkID, hashQuestion(oldQuestion))
	if current == legacyID {
		t.Fatal("pre-upgrade and post-upgrade IDs must differ for the scenario to matter")
	}
}
