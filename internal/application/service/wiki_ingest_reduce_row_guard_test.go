package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// liveKnowledgeStub keeps filterLiveUpdates from dropping the test's addition:
// reduce treats an unreadable knowledge as deleted and skips the whole slug.
type liveKnowledgeStub struct{ interfaces.KnowledgeService }

func (liveKnowledgeStub) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "k-row-guard", ParseStatus: types.ParseStatusCompleted}, nil
}

// reduceGuardedEntityPage runs one reduce round for an entity page over a real
// wiki page store, with modelResponse as the editor's answer. The returned
// updateDeferred is what ProcessWikiIngest turns into collectUnapplied — the
// set of knowledge ids kept out of the trim phase so a later batch retries them.
func reduceGuardedEntityPage(
	t *testing.T, svc *wikiIngestService, slug, modelResponse string,
) (deferred bool, err error) {
	t.Helper()
	model := &templateCaptureChatModel{response: modelResponse}
	batchCtx := &WikiBatchContext{
		SlugTitleMany: func(context.Context, []string) map[string]string { return nil },
	}
	updates := []SlugUpdate{{
		Slug:        slug,
		Type:        types.WikiPageTypeEntity,
		KnowledgeID: "k-row-guard",
		DocTitle:    "证书台账",
		SourceRef:   "k-row-guard|证书台账",
		Item:        extractedItem{Name: "证书台账", Description: "持证人员明细"},
	}}
	_, _, _, deferred, err = svc.reduceSlugUpdates(
		context.Background(), model, "kb-row-guard", slug, updates, 1, batchCtx, nil)
	return deferred, err
}

// Regression for the version-based deferral signal: UpdatePage leaves `version`
// alone for a successful write that changes nothing user-visible (identical
// body with refreshed bookkeeping — see the version-bump policy in
// wiki_page.go). Reading "same version" as "the guard refused" re-queued those
// applied contributions forever, so reduce must see no refusal here.
func TestReduceSlugUpdatesDoesNotDeferAWriteThatChangedNothing(t *testing.T) {
	// reduce splits the SUMMARY line off with strings.TrimSpace, so the stored
	// body has to be the trimmed form for the editor's identical answer to be
	// a true no-op.
	stored := strings.TrimSpace(ledger("PMP", "张三", "李四", "王五"))
	pageSvc, page := newWikiWriteGuardStore(t, stored)
	svc := &wikiIngestService{wikiService: pageSvc, knowledgeSvc: liveKnowledgeStub{}}

	// The editor re-emits the stored page verbatim; only source_refs change.
	deferred, err := reduceGuardedEntityPage(t, svc, "entity/pmp", stored)

	require.NoError(t, err)
	require.False(t, deferred,
		"a successful write that did not bump the version is applied, not deferred")

	after, err := pageSvc.GetPageBySlug(context.Background(), "kb-row-guard", "entity/pmp")
	require.NoError(t, err)
	require.Equal(t, stored, after.Content)
	require.Equal(t, page.Version, after.Version,
		"the write really was a no-op, so the version must not move")
}

// The other half of the same regression: a write the row guard really refused
// was never applied, so its contributing documents must survive the trim phase
// and come back in a later batch.
func TestReduceSlugUpdatesDefersAWriteTheRowGuardRefused(t *testing.T) {
	stored := ledger("PMP", "张三", "李四", "王五", "赵六", "钱七")
	pageSvc, page := newWikiWriteGuardStore(t, stored)
	svc := &wikiIngestService{wikiService: pageSvc, knowledgeSvc: liveKnowledgeStub{}}

	// Four of the five stored identities are gone: far above the ingest limit,
	// so UpdatePage refuses and reduce must defer instead of reporting success.
	deferred, err := reduceGuardedEntityPage(t, svc, "entity/pmp", ledger("PMP", "张三"))

	require.NoError(t, err, "a guard refusal is not a reduce failure")
	require.True(t, deferred,
		"the refused contribution must be kept for a later batch")

	after, err := pageSvc.GetPageBySlug(context.Background(), "kb-row-guard", "entity/pmp")
	require.NoError(t, err)
	require.Equal(t, stored, after.Content)
	require.Equal(t, page.Version, after.Version)
}

// A same-identity merge is inside the ingest tolerance, so the write lands and
// the documents are not re-queued.
func TestReduceSlugUpdatesAppliesAMergeInsideTheLossLimit(t *testing.T) {
	stored := ledger("证书", "张三", "张三", "李四", "王五")
	pageSvc, page := newWikiWriteGuardStore(t, stored)
	svc := &wikiIngestService{wikiService: pageSvc, knowledgeSvc: liveKnowledgeStub{}}

	merged := ledger("证书", "张三", "李四", "王五")
	deferred, err := reduceGuardedEntityPage(t, svc, "entity/pmp", merged)

	require.NoError(t, err)
	require.False(t, deferred)
	after, err := pageSvc.GetPageBySlug(context.Background(), "kb-row-guard", "entity/pmp")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(merged), after.Content)
	require.Equal(t, page.Version+1, after.Version)
}

// Only the guard's own signal counts as a deferral: a genuine write failure is
// still an error, and the caller's reduceErr path (not the deferred path) owns
// it.
func TestReduceSlugUpdatesSurfacesAGenuineWriteError(t *testing.T) {
	writeErr := errors.New("postgres unavailable")
	svc := &wikiIngestService{
		wikiService:  &failingPageWriteStub{page: &types.WikiPage{Slug: "entity/pmp"}, err: writeErr},
		knowledgeSvc: liveKnowledgeStub{},
	}

	deferred, err := reduceGuardedEntityPage(t, svc, "entity/pmp", ledger("PMP", "张三"))

	require.ErrorIs(t, err, writeErr)
	require.False(t, deferred)
}

type failingPageWriteStub struct {
	interfaces.WikiPageService
	page *types.WikiPage
	err  error
}

func (s *failingPageWriteStub) GetPageBySlug(context.Context, string, string) (*types.WikiPage, error) {
	return s.page, nil
}

func (s *failingPageWriteStub) UpdatePage(context.Context, *types.WikiPage) (*types.WikiPage, error) {
	return nil, s.err
}

// classifyWikiPageWrite is the whole decision table: the sentinel (bare or
// wrapped, as UpdatePage returns it) defers; everything else is passed through.
func TestClassifyWikiPageWrite(t *testing.T) {
	writeErr := errors.New("postgres unavailable")
	unrelated := errors.New("dropped rows")
	cases := []struct {
		name     string
		in       error
		deferred bool
		wantErr  error
	}{
		{"applied write", nil, false, nil},
		{"bare refusal", ErrWikiWriteDroppedTableRows, true, nil},
		{
			"wrapped refusal",
			fmt.Errorf("update wiki page: %w", ErrWikiWriteDroppedTableRows),
			true, nil,
		},
		{"write failure", writeErr, false, writeErr},
		{"unrelated error", unrelated, false, unrelated},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			deferred, err := classifyWikiPageWrite(testCase.in)
			require.Equal(t, testCase.deferred, deferred)
			if testCase.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, testCase.wantErr)
		})
	}
}
