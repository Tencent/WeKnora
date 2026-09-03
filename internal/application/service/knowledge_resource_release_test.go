package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// deleteRecorder captures which files a cleanup actually removed.
type deleteRecorder struct {
	fakeFileService
	deleted []string
}

func (f *deleteRecorder) DeleteFile(_ context.Context, filePath string) error {
	f.deleted = append(f.deleted, filePath)
	return nil
}

func handleRef(char string) string {
	return types.BuildResourcePath(strings.Repeat(char, types.ResourceHandleLength))
}

func TestDeleteExtractedImagesKeepsFilesAnotherOwnerStillClaims(t *testing.T) {
	shared := handleRef("a")    // also shown by the chat message it was saved from
	exclusive := handleRef("b") // only this knowledge references it
	legacy := "local://7/exports/old.png"

	catalog := &fakeCatalog{releaseRemaining: map[string]int64{shared: 1, exclusive: 0}}
	files := &deleteRecorder{}

	deleteExtractedImages(
		context.Background(), files,
		knowledgeResourceOwners(catalog, "kn-1"),
		[]string{shared, exclusive, legacy},
	)

	want := []string{exclusive, legacy}
	if len(files.deleted) != len(want) {
		t.Fatalf("deleted %v, want %v", files.deleted, want)
	}
	for i, url := range want {
		if files.deleted[i] != url {
			t.Fatalf("deleted[%d] = %q, want %q", i, files.deleted[i], url)
		}
	}
	if len(catalog.releases) != 3 {
		t.Fatalf("released %v, want one call per reference", catalog.releases)
	}
	if got := catalog.releases[0]; got != shared+"|"+types.ResourceOwnerKnowledge+"|kn-1" {
		t.Fatalf("unexpected release call %q", got)
	}
}

// A knowledge base delete releases every entry's claim before deciding, so a
// file shared between two entries of the same base is still removed.
func TestDeleteExtractedImagesReleasesEveryOwnerBeforeDeciding(t *testing.T) {
	shared := handleRef("c")
	catalog := &fakeCatalog{releaseRemaining: map[string]int64{shared: 0}}
	files := &deleteRecorder{}

	deleteExtractedImages(
		context.Background(), files,
		knowledgeResourceOwners(catalog, "kn-1", "kn-2"),
		[]string{shared},
	)

	if len(files.deleted) != 1 {
		t.Fatalf("deleted %v, want the shared file removed once", files.deleted)
	}
	if len(catalog.releases) != 2 {
		t.Fatalf("released %v, want both owners released", catalog.releases)
	}
}

// An unreadable binding count must not destroy bytes: an orphaned blob can be
// reclaimed later, an image missing from a document nobody deleted cannot.
func TestDeleteExtractedImagesKeepsFileWhenReleaseFails(t *testing.T) {
	catalog := &fakeCatalog{releaseErr: errors.New("db down")}
	files := &deleteRecorder{}

	deleteExtractedImages(
		context.Background(), files,
		knowledgeResourceOwners(catalog, "kn-1"),
		[]string{handleRef("d")},
	)

	if len(files.deleted) != 0 {
		t.Fatalf("deleted %v, want nothing deleted while the count is unknown", files.deleted)
	}
}

// Without a catalog the guard is inert and cleanup behaves as it always did.
func TestDeleteExtractedImagesWithoutCatalogDeletesEverything(t *testing.T) {
	files := &deleteRecorder{}
	urls := []string{handleRef("e"), "local://7/exports/x.png"}

	deleteExtractedImages(context.Background(), files, knowledgeResourceOwners(nil, "kn-1"), urls)

	if len(files.deleted) != len(urls) {
		t.Fatalf("deleted %v, want %v", files.deleted, urls)
	}
}
