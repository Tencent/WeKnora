package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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

func TestMergeKnowledgeReleaseURLsIncludesOwnerBindings(t *testing.T) {
	legacy := "local://7/exports/old.png"
	bound := handleRef("m")
	catalog := &fakeCatalog{
		ownerRefs:        map[string][]string{"kn-1": {bound}},
		releaseRemaining: map[string]int64{bound: 0},
	}
	files := &deleteRecorder{}

	urls := mergeKnowledgeReleaseURLs(context.Background(), catalog, []string{"kn-1"}, []string{legacy})
	deleteExtractedImages(context.Background(), files, knowledgeResourceOwners(catalog, "kn-1"), urls)

	want := []string{legacy, bound}
	if len(files.deleted) != len(want) {
		t.Fatalf("deleted %v, want %v", files.deleted, want)
	}
	for i, url := range want {
		if files.deleted[i] != url {
			t.Fatalf("deleted[%d] = %q, want %q", i, files.deleted[i], url)
		}
	}
}

func TestMergeKnowledgeReleaseURLsReleasesMarkdownOnlyBindings(t *testing.T) {
	bound := handleRef("n")
	catalog := &fakeCatalog{
		ownerRefs:        map[string][]string{"kn-1": {bound}},
		releaseRemaining: map[string]int64{bound: 0},
	}
	files := &deleteRecorder{}

	urls := mergeKnowledgeReleaseURLs(context.Background(), catalog, []string{"kn-1"}, nil)
	deleteExtractedImages(context.Background(), files, knowledgeResourceOwners(catalog, "kn-1"), urls)

	if len(files.deleted) != 1 || files.deleted[0] != bound {
		t.Fatalf("deleted %v, want markdown-bound handle %q", files.deleted, bound)
	}
}

// Cleanup runs before a re-index (manual update, reparse), not before the
// knowledge goes away. A markdown-only attachment never appears in ImageInfo,
// so unioning the catalog bindings in here released it, DeleteFile marked the
// resource deleted, and the re-claim that follows could no longer resolve the
// handle -- the republished body was left pointing at a deleted image. The
// file and its binding must both outlive the cleanup.
func TestCleanupKeepsMarkdownOnlyAttachment(t *testing.T) {
	catalog, _ := newResourceCatalogForTest(t)
	handle, err := catalog.Register(
		context.Background(), 7, "local://7/exports/markdown-only.png",
		interfaces.ResourceRegistration{Kind: "image"},
	)
	if err != nil {
		t.Fatalf("register markdown-only image: %v", err)
	}
	if err := catalog.Bind(
		context.Background(), handle, types.ResourceOwnerKnowledge, "doc",
		types.ResourceRelationAttachment,
	); err != nil {
		t.Fatalf("bind markdown-only image: %v", err)
	}

	f := newDocumentWriteFixture(t)
	f.svc.resourceCatalog = catalog
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	if err != nil {
		t.Fatalf("load knowledge: %v", err)
	}
	// The fixture chunk carries no ImageInfo, which is what makes this case
	// invisible to the pre-change release set.
	if err := f.svc.cleanupKnowledgeResources(f.ctx, row); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if len(f.files.deleted) != 0 {
		t.Fatalf("cleanup deleted %v, want the solely owned image kept", f.files.deleted)
	}
	refs, err := catalog.ListReferencesByOwner(f.ctx, types.ResourceOwnerKnowledge, "doc")
	if err != nil {
		t.Fatalf("list owner references: %v", err)
	}
	if len(refs) != 1 || refs[0] != handle {
		t.Fatalf("owner references = %v, want the attachment %q still bound", refs, handle)
	}
	// Still resolvable, so the re-claim after cleanup can bind it again.
	resource, err := catalog.Resolve(f.ctx, handle)
	if err != nil || resource == nil {
		t.Fatalf("resolve after cleanup = (%v, %v), want the live resource", resource, err)
	}
}

// The delete paths keep the union: the knowledge is going away, so a
// markdown-only attachment has no re-claim to survive for.
func TestKnowledgeDeleteReleasesMarkdownOnlyAttachment(t *testing.T) {
	catalog, _ := newResourceCatalogForTest(t)
	handle, err := catalog.Register(
		context.Background(), 7, "local://7/exports/markdown-only.png",
		interfaces.ResourceRegistration{Kind: "image"},
	)
	if err != nil {
		t.Fatalf("register markdown-only image: %v", err)
	}
	if err := catalog.Bind(
		context.Background(), handle, types.ResourceOwnerKnowledge, "doc",
		types.ResourceRelationAttachment,
	); err != nil {
		t.Fatalf("bind markdown-only image: %v", err)
	}

	f := newDocumentWriteFixture(t)
	f.svc.resourceCatalog = catalog
	if err := f.svc.DeleteKnowledge(f.ctx, "doc"); err != nil {
		t.Fatalf("delete knowledge: %v", err)
	}

	if len(f.files.deleted) != 1 || f.files.deleted[0] != handle {
		t.Fatalf("deleted %v, want the released markdown-only handle %q", f.files.deleted, handle)
	}
	refs, err := catalog.ListReferencesByOwner(f.ctx, types.ResourceOwnerKnowledge, "doc")
	if err != nil {
		t.Fatalf("list owner references: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("owner references = %v, want every claim released", refs)
	}
}

func TestMergeKnowledgeReleaseURLsOmitsSourceFileBinding(t *testing.T) {
	catalog, _ := newResourceCatalogForTest(t)
	ctx := context.Background()
	image, err := catalog.Register(ctx, 7, "local://7/exports/a.png", interfaces.ResourceRegistration{})
	if err != nil {
		t.Fatalf("register image: %v", err)
	}
	source, err := catalog.Register(ctx, 7, "local://7/docs/source.pdf", interfaces.ResourceRegistration{})
	if err != nil {
		t.Fatalf("register source: %v", err)
	}
	if err := catalog.Bind(
		ctx, image, types.ResourceOwnerKnowledge, "kn-1", types.ResourceRelationExtractedImage,
	); err != nil {
		t.Fatalf("bind image: %v", err)
	}
	if err := catalog.Bind(
		ctx, source, types.ResourceOwnerKnowledge, "kn-1", types.ResourceRelationSourceFile,
	); err != nil {
		t.Fatalf("bind source: %v", err)
	}

	urls := mergeKnowledgeReleaseURLs(ctx, catalog, []string{"kn-1"}, nil)
	if len(urls) != 1 || urls[0] != image {
		t.Fatalf("release URLs = %v, want only extracted image %q", urls, image)
	}
}
