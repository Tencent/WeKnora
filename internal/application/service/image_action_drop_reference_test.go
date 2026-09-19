package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// dropReferenceChunkService stands in for the chunk service. It behaves like the
// real one where this action depends on it: it refuses empty content, checks the
// expected revision, and bumps the revision on every accepted write. That last
// part is what lets a test prove a second edit on the same block sees the
// revision the first edit produced.
//
// The stored rows are deliberately copies of the rows the test holds, so the two
// only agree after the action syncs them back.
type dropReferenceChunkService struct {
	interfaces.ChunkService
	stored  map[string]*types.Chunk
	calls   []dropReferenceCall
	failure error
	lastCtx context.Context
}

type dropReferenceCall struct {
	id       string
	content  *string
	enabled  *bool
	expected *int
}

func newDropReferenceChunkService(rows ...*types.Chunk) *dropReferenceChunkService {
	stored := make(map[string]*types.Chunk, len(rows))
	for _, row := range rows {
		storedCopy := *row
		stored[row.ID] = &storedCopy
	}
	return &dropReferenceChunkService{stored: stored}
}

func (s *dropReferenceChunkService) UpdateDocumentChunk(
	ctx context.Context, chunkID string, content *string, isEnabled *bool, expectedRevision *int,
) (*types.Chunk, error) {
	s.lastCtx = ctx
	s.calls = append(s.calls, dropReferenceCall{
		id: chunkID, content: content, enabled: isEnabled, expected: expectedRevision,
	})
	if s.failure != nil {
		return nil, s.failure
	}
	row, ok := s.stored[chunkID]
	if !ok {
		return nil, errors.New("no such chunk")
	}
	if expectedRevision != nil && *expectedRevision != row.ContentRevision {
		return nil, ErrChunkRevisionConflict
	}
	if content != nil {
		if strings.TrimSpace(*content) == "" {
			return nil, errors.New("chunk content cannot be empty")
		}
		row.Content = strings.TrimSpace(*content)
	}
	if isEnabled != nil {
		row.IsEnabled = *isEnabled
	}
	row.ContentRevision++
	return row, nil
}

// textBlock builds a text block that references the given image URLs.
func textBlock(id string, revision int, urls ...string) *types.Chunk {
	block := &types.Chunk{
		ID:              id,
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		ContentRevision: revision,
		Content:         "Pump overview.",
	}
	for _, url := range urls {
		block.Content += "\n\n![image](" + url + ")"
	}
	block.Content += "\n\nEnd of section."
	return block
}

// decorativeDocument is a block holding one decorative image and one diagram,
// plus the image children the engine pairs them up by.
func decorativeDocument() []*types.Chunk {
	block := &types.Chunk{
		ID:              "text-1",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		ContentRevision: 1,
		Content: "Pump overview.\n\n![divider](local://img/div.png)\n\n" +
			"![wiring](local://img/wire.png)\n\nEnd of section.",
	}
	dividerCaption := imageChild("cap-div", "text-1", types.ChunkTypeImageCaption, "[[decorative]]", true,
		types.ImageInfo{URL: "local://img/div.png", Class: "decorative", Caption: "[[decorative]]"})
	wireCaption := imageChild("cap-wire", "text-1", types.ChunkTypeImageCaption, "A wiring diagram.", true,
		types.ImageInfo{URL: "local://img/wire.png", Class: "chart", Caption: "A wiring diagram."})
	return []*types.Chunk{block, dividerCaption, wireCaption}
}

func dropRuleKB(enabled bool) *types.KnowledgeBase {
	kb := &types.KnowledgeBase{}
	kb.ImageProcessingConfig.PostProcessImageEnabled = enabled
	kb.ImageProcessingConfig.PostProcessImageRules = []types.ImageRule{{
		ID:      "retire-decorative",
		Name:    "retire decorative images",
		Enabled: boolPtr(true),
		Match:   types.ImageMatchSpec{Classes: []string{"decorative"}},
		Action:  DropImageReferenceActionName,
	}}
	return kb
}

// ---------------------------------------------------------------------------
// The action
// ---------------------------------------------------------------------------

func TestDropImageReferenceRemovesTheReferenceAndKeepsTheProse(t *testing.T) {
	t.Parallel()

	block := textBlock("text-1", 3, "local://img/div.png", "local://img/wire.png")
	chunks := newDropReferenceChunkService(block)
	action := newDropImageReferenceAction(chunks)

	result, err := action.Apply(context.Background(), &ActionRequest{
		TenantID:  1,
		Candidate: &ImageCandidate{URL: "local://img/div.png", ParentChunk: block},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Outcome != ActionResultApplied {
		t.Fatalf("outcome = %q, want applied (%v)", result.Outcome, result.Detail)
	}
	if strings.Contains(block.Content, "local://img/div.png") {
		t.Errorf("the referenced image is still in the block: %q", block.Content)
	}
	if !strings.Contains(block.Content, "local://img/wire.png") {
		t.Errorf("the untouched image was removed as well: %q", block.Content)
	}
	for _, want := range []string{"Pump overview.", "End of section."} {
		if !strings.Contains(block.Content, want) {
			t.Errorf("prose %q was lost: %q", want, block.Content)
		}
	}
	// The caller's copy has to track what was stored, because the same block
	// may need another edit and the next one presents this revision.
	if block.ContentRevision != 4 {
		t.Errorf("block revision = %d, want 4 (the revision the write produced)", block.ContentRevision)
	}
	if block.IsEnabled != true {
		t.Error("a block that still has prose must not be disabled")
	}
	if len(chunks.calls) != 1 {
		t.Fatalf("writes = %d, want 1", len(chunks.calls))
	}
	if call := chunks.calls[0]; call.enabled != nil {
		t.Errorf("a body edit should not touch the enabled flag, got %v", call.enabled)
	}
}

func TestDropImageReferenceRemovesAnHTMLReference(t *testing.T) {
	t.Parallel()

	block := &types.Chunk{
		ID: "text-1", ChunkType: types.ChunkTypeText, IsEnabled: true, ContentRevision: 0,
		Content: `before<img src="local://img/div.png" alt="divider">after`,
	}
	chunks := newDropReferenceChunkService(block)
	action := newDropImageReferenceAction(chunks)

	result, err := action.Apply(context.Background(), &ActionRequest{
		Candidate: &ImageCandidate{URL: "local://img/div.png", ParentChunk: block},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Outcome != ActionResultApplied {
		t.Fatalf("outcome = %q, want applied", result.Outcome)
	}
	if strings.Contains(block.Content, "<img") {
		t.Errorf("the img tag survived, so the image's rows would stay enabled: %q", block.Content)
	}
}

func TestDropImageReferenceDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	block := textBlock("text-1", 1, "local://img/div.png")
	original := block.Content
	chunks := newDropReferenceChunkService(block)
	action := newDropImageReferenceAction(chunks)

	result, err := action.Apply(context.Background(), &ActionRequest{
		Candidate: &ImageCandidate{URL: "local://img/div.png", ParentChunk: block},
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Outcome != ActionResultDryRun {
		t.Fatalf("outcome = %q, want dry_run", result.Outcome)
	}
	if result.Detail["reference_removed"] != true {
		t.Errorf("a preview has to say what it would do: %v", result.Detail)
	}
	if len(chunks.calls) != 0 {
		t.Errorf("a preview wrote %d time(s)", len(chunks.calls))
	}
	if block.Content != original || block.ContentRevision != 1 {
		t.Error("a preview must not edit the caller's block either")
	}
}

func TestDropImageReferenceSkipsABodyWithoutTheReference(t *testing.T) {
	t.Parallel()

	block := textBlock("text-1", 1, "local://img/wire.png")
	chunks := newDropReferenceChunkService(block)
	action := newDropImageReferenceAction(chunks)

	result, err := action.Apply(context.Background(), &ActionRequest{
		Candidate: &ImageCandidate{URL: "local://img/div.png", ParentChunk: block},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Outcome != ActionResultSkipped {
		t.Fatalf("outcome = %q, want skipped: editing would record a revision that changes nothing", result.Outcome)
	}
	if len(chunks.calls) != 0 {
		t.Errorf("a no-op edit wrote %d time(s)", len(chunks.calls))
	}
}

// A block that held nothing but the image cannot be emptied: the repository
// rejects empty content. It gets retired instead.
func TestDropImageReferenceRetiresABlockThatWasOnlyImages(t *testing.T) {
	t.Parallel()

	block := &types.Chunk{
		ID: "text-1", ChunkType: types.ChunkTypeText, IsEnabled: true, ContentRevision: 2,
		Content: "![divider](local://img/div.png)",
	}
	chunks := newDropReferenceChunkService(block)
	action := newDropImageReferenceAction(chunks)

	result, err := action.Apply(context.Background(), &ActionRequest{
		Candidate: &ImageCandidate{URL: "local://img/div.png", ParentChunk: block},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Outcome != ActionResultApplied {
		t.Fatalf("outcome = %q, want applied (%v)", result.Outcome, result.Detail)
	}
	if result.Detail["parent_disabled"] != true {
		t.Errorf("the detail should say the block was retired: %v", result.Detail)
	}
	if len(chunks.calls) != 1 {
		t.Fatalf("writes = %d, want 1", len(chunks.calls))
	}
	call := chunks.calls[0]
	if call.content != nil {
		t.Errorf("retiring a block must not try to write empty content, got %q", *call.content)
	}
	if call.enabled == nil || *call.enabled {
		t.Errorf("the block should be disabled, got enabled=%v", call.enabled)
	}
	if block.IsEnabled {
		t.Error("the caller's block should show as disabled")
	}
	if block.ContentRevision != 3 {
		t.Errorf("block revision = %d, want 3", block.ContentRevision)
	}
}

// Two images in one block both match, so the block is edited twice. The second
// edit only succeeds if the first one's revision travelled back to the caller.
func TestDropImageReferenceSecondEditSeesTheFirstRevisionsResult(t *testing.T) {
	t.Parallel()

	block := textBlock("text-1", 5, "local://img/div1.png", "local://img/div2.png")
	chunks := newDropReferenceChunkService(block)
	action := newDropImageReferenceAction(chunks)

	for _, url := range []string{"local://img/div1.png", "local://img/div2.png"} {
		result, err := action.Apply(context.Background(), &ActionRequest{
			Candidate: &ImageCandidate{URL: url, ParentChunk: block},
		})
		if err != nil {
			t.Fatalf("Apply(%s): %v", url, err)
		}
		if result.Outcome != ActionResultApplied {
			t.Fatalf("Apply(%s) outcome = %q, want applied (%v)", url, result.Outcome, result.Detail)
		}
	}

	if len(chunks.calls) != 2 {
		t.Fatalf("writes = %d, want 2", len(chunks.calls))
	}
	for i, call := range chunks.calls {
		if call.expected == nil {
			t.Fatalf("call %d should check a revision", i)
		}
		if want := 5 + i; *call.expected != want {
			t.Errorf("call %d expected revision %d, want %d", i, *call.expected, want)
		}
	}
	if strings.Contains(block.Content, "div") {
		t.Errorf("both references should be gone, got %q", block.Content)
	}
}

func TestDropImageReferenceSkipsWhatItCannotActOn(t *testing.T) {
	t.Parallel()

	action := newDropImageReferenceAction(newDropReferenceChunkService())

	cases := []struct {
		name string
		req  *ActionRequest
	}{
		{"nil request", nil},
		{"no candidate", &ActionRequest{}},
		{"no parent block", &ActionRequest{Candidate: &ImageCandidate{URL: "local://img/a.png"}}},
		{
			"parent is not a text block",
			&ActionRequest{Candidate: &ImageCandidate{
				URL:         "local://img/a.png",
				ParentChunk: &types.Chunk{ID: "img-1", ChunkType: types.ChunkTypeImageCaption},
			}},
		},
		{
			"no url to match on",
			&ActionRequest{Candidate: &ImageCandidate{
				ParentChunk: &types.Chunk{ID: "text-1", ChunkType: types.ChunkTypeText, Content: "body"},
			}},
		},
	}
	for _, tc := range cases {
		result, err := action.Apply(context.Background(), tc.req)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if result.Outcome != ActionResultSkipped {
			t.Errorf("%s: outcome = %q, want skipped", tc.name, result.Outcome)
		}
		if result.Detail["reason"] == nil {
			t.Errorf("%s: a skip has to say why: %v", tc.name, result.Detail)
		}
	}
}

func TestDropImageReferenceReportsAFailedWrite(t *testing.T) {
	t.Parallel()

	block := textBlock("text-1", 1, "local://img/div.png")
	chunks := newDropReferenceChunkService(block)
	sentinel := errors.New("storage is down")
	chunks.failure = sentinel
	action := newDropImageReferenceAction(chunks)

	result, err := action.Apply(context.Background(), &ActionRequest{
		Candidate: &ImageCandidate{URL: "local://img/div.png", ParentChunk: block},
	})
	if err == nil {
		t.Fatal("a failed write must be reported, not swallowed")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error should wrap the storage failure, got %v", err)
	}
	if result == nil || result.Outcome != ActionResultFailed {
		t.Errorf("result = %+v, want a failed outcome alongside the error", result)
	}
}

// A revision conflict means somebody else is editing the document. The action
// stands down rather than overwriting them.
func TestDropImageReferenceStandsDownOnARevisionConflict(t *testing.T) {
	t.Parallel()

	block := textBlock("text-1", 1, "local://img/div.png")
	chunks := newDropReferenceChunkService(block)
	// Somebody else moved the row on after this action read it.
	chunks.stored["text-1"].ContentRevision = 99
	action := newDropImageReferenceAction(chunks)

	result, err := action.Apply(context.Background(), &ActionRequest{
		Candidate: &ImageCandidate{URL: "local://img/div.png", ParentChunk: block},
	})
	if err == nil {
		t.Fatal("a revision conflict must be reported")
	}
	if !errors.Is(err, ErrChunkRevisionConflict) {
		t.Errorf("error should be the revision conflict, got %v", err)
	}
	if result == nil || result.Outcome != ActionResultFailed {
		t.Errorf("result = %+v, want a failed outcome", result)
	}
}

func TestDropImageReferenceNeedsAChunkService(t *testing.T) {
	t.Parallel()

	action := newDropImageReferenceAction(nil)
	block := textBlock("text-1", 1, "local://img/div.png")

	_, err := action.Apply(context.Background(), &ActionRequest{
		Candidate: &ImageCandidate{URL: "local://img/div.png", ParentChunk: block},
	})
	if err == nil {
		t.Fatal("wiring the action without a chunk service must fail loudly, not silently skip")
	}
}

func TestDropImageReferenceDescribesItself(t *testing.T) {
	t.Parallel()

	action := newDropImageReferenceAction(nil)
	if action.Name() != DropImageReferenceActionName {
		t.Errorf("Name() = %q", action.Name())
	}
	if action.Phase() != ActionPhaseInline {
		t.Errorf("Phase() = %q, want inline: the models must not see the image", action.Phase())
	}
	meta := action.Describe()
	if meta.Name != DropImageReferenceActionName || meta.Title == "" || meta.Description == "" {
		t.Errorf("Describe() = %+v, want a presentable entry", meta)
	}
	if meta.Phase != ActionPhaseInline {
		t.Errorf("Describe().Phase = %q, want inline", meta.Phase)
	}
}

// ---------------------------------------------------------------------------
// The orchestration
// ---------------------------------------------------------------------------

func newImagePostProcessTestService(rows ...*types.Chunk) (*KnowledgePostProcessService, *dropReferenceChunkService) {
	chunks := newDropReferenceChunkService(rows...)
	return &KnowledgePostProcessService{chunkService: chunks, spanTracker: noopSpanTracker{}}, chunks
}

func TestApplyImagePostProcessRulesEditsTheReferencingBlock(t *testing.T) {
	t.Parallel()

	textChunks := decorativeDocument()
	service, chunks := newImagePostProcessTestService(textChunks...)

	got := service.applyImagePostProcessRules(
		context.Background(), 1, "k-1", dropRuleKB(true), textChunks, nil, nil)

	if len(got) != len(textChunks) {
		t.Fatalf("returned %d chunk(s), want %d: only retired blocks are dropped", len(got), len(textChunks))
	}
	if len(chunks.calls) != 1 {
		t.Fatalf("writes = %d, want 1 (only the decorative image is matched)", len(chunks.calls))
	}
	block := textChunks[0]
	if strings.Contains(block.Content, "local://img/div.png") {
		t.Errorf("the decorative reference survived: %q", block.Content)
	}
	if !strings.Contains(block.Content, "local://img/wire.png") {
		t.Errorf("the diagram was removed too: %q", block.Content)
	}
}

// A block whose only content was a decorative image is retired, and the caller's
// list must lose it — otherwise graph extraction and the subtask count would go
// on describing a block that no longer holds anything.
func TestApplyImagePostProcessRulesDropsRetiredBlocksFromTheList(t *testing.T) {
	t.Parallel()

	block := &types.Chunk{
		ID: "text-only-image", ChunkType: types.ChunkTypeText, IsEnabled: true, ContentRevision: 1,
		Content: "![divider](local://img/div.png)",
	}
	child := imageChild("cap-div", "text-only-image", types.ChunkTypeImageCaption, "[[decorative]]", true,
		types.ImageInfo{URL: "local://img/div.png", Class: "decorative"})
	textChunks := []*types.Chunk{block, child}
	service, _ := newImagePostProcessTestService(textChunks...)

	got := service.applyImagePostProcessRules(
		context.Background(), 1, "k-1", dropRuleKB(true), textChunks, nil, nil)

	if len(got) != 0 {
		t.Fatalf("returned %d chunk(s), want none: the block and its image child were both retired", len(got))
	}
	if block.IsEnabled {
		t.Error("the block should be disabled rather than emptied")
	}
}

// Switching the engine on without naming any rules takes the built-in set:
// "enabled, not configured yet" is one switch away for a user, and it should do
// the obvious thing.
func TestApplyImagePostProcessRulesFallsBackToTheDefaultRuleSet(t *testing.T) {
	t.Parallel()

	textChunks := decorativeDocument()
	service, chunks := newImagePostProcessTestService(textChunks...)

	kb := &types.KnowledgeBase{}
	kb.ImageProcessingConfig.PostProcessImageEnabled = true

	got := service.applyImagePostProcessRules(context.Background(), 1, "k-1", kb, textChunks, nil, nil)

	if len(chunks.calls) != 1 {
		t.Fatalf("writes = %d, want 1 (the default rule retires the decorative image)", len(chunks.calls))
	}
	if strings.Contains(textChunks[0].Content, "local://img/div.png") {
		t.Error("the default rule should have removed the decorative reference")
	}
	if len(got) != len(textChunks) {
		t.Errorf("returned %d chunk(s), want %d: nothing was retired", len(got), len(textChunks))
	}
	rules := imageDisableRules(types.DefaultImageClassPolicies())
	if len(rules) != 1 || rules[0].Action != DropImageReferenceActionName {
		t.Errorf("default rules = %+v, want the single drop_image_reference rule", rules)
	}
}

// TestImageDisableRulesDerivesPerDisabledClass pins the class-table form of
// the default: every disabled row becomes one drop-reference rule, in enum
// order, and an override that disables photo in addition to decorative
// produces both.
func TestImageDisableRulesDerivesPerDisabledClass(t *testing.T) {
	t.Parallel()

	custom := types.MergeImageClassPolicies(map[string]types.ImageClassPolicy{
		string(types.ImageClassPhoto): {OCR: false, Caption: true, Disabled: true},
	})
	rules := imageDisableRules(custom)

	if len(rules) != 2 {
		t.Fatalf("rules = %+v, want one per disabled class (decorative, photo)", rules)
	}
	if rules[0].ID != "disable-decorative-images" || rules[1].ID != "disable-photo-images" {
		t.Errorf("rule ids = [%s %s], want enum-ordered disable-<class>-images",
			rules[0].ID, rules[1].ID)
	}
	for i, rule := range rules {
		if rule.Action != DropImageReferenceActionName {
			t.Errorf("rule %d action = %q, want %q", i, rule.Action, DropImageReferenceActionName)
		}
	}
}

func TestApplyImagePostProcessRulesIgnoresUnusableRules(t *testing.T) {
	t.Parallel()

	textChunks := decorativeDocument()
	original := textChunks[0].Content
	service, chunks := newImagePostProcessTestService(textChunks...)

	kb := dropRuleKB(true)
	kb.ImageProcessingConfig.PostProcessImageRules = append(kb.ImageProcessingConfig.PostProcessImageRules,
		types.ImageRule{ID: "ghost", Match: types.ImageMatchSpec{Classes: []string{"decorative"}}, Action: "ghost"},
		types.ImageRule{ID: "empty", Match: types.ImageMatchSpec{}, Action: DropImageReferenceActionName},
		types.ImageRule{
			ID: "off", Enabled: boolPtr(false),
			Match: types.ImageMatchSpec{Classes: []string{"decorative"}}, Action: DropImageReferenceActionName,
		},
	)

	got := service.applyImagePostProcessRules(context.Background(), 1, "k-1", kb, textChunks, nil, nil)

	// The one usable rule still runs; the unusable ones change nothing.
	if len(chunks.calls) != 1 {
		t.Errorf("writes = %d, want 1: unusable rules must not act", len(chunks.calls))
	}
	if len(got) != len(textChunks) {
		t.Errorf("returned %d chunk(s), want %d", len(got), len(textChunks))
	}
	if original == textChunks[0].Content {
		t.Error("the usable rule should still have edited the block")
	}
}

func TestApplyImagePostProcessRulesHandlesADocumentWithoutImages(t *testing.T) {
	t.Parallel()

	block := &types.Chunk{ID: "text-1", ChunkType: types.ChunkTypeText, IsEnabled: true, Content: "just prose"}
	textChunks := []*types.Chunk{block}
	service, chunks := newImagePostProcessTestService(textChunks...)

	got := service.applyImagePostProcessRules(
		context.Background(), 1, "k-1", dropRuleKB(true), textChunks, nil, nil)

	if len(chunks.calls) != 0 {
		t.Errorf("writes = %d, want none", len(chunks.calls))
	}
	if len(got) != 1 || got[0] != block {
		t.Errorf("returned %+v, want the untouched block", got)
	}
}

func TestFilterRetiredBlocks(t *testing.T) {
	t.Parallel()

	chunks := []*types.Chunk{
		{ID: "a"},
		{ID: "b"},
		{ID: "b-child", ParentChunkID: "b"},
		{ID: "c"},
	}
	got := filterRetiredBlocks(chunks, map[string]bool{"b": true})
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Fatalf("got %+v, want only the unrelated blocks", got)
	}
	if sameLen := filterRetiredBlocks(chunks, nil); len(sameLen) != 4 {
		t.Errorf("nothing retired should return the list unchanged, got %d", len(sameLen))
	}
}

// The chunk edit path reindexes through the retrieve-engine factory, which
// reads the whole tenant from the context. An Asynq worker context carries only
// the tenant id, so the rules have to put the tenant back before they edit —
// otherwise the content is saved and the reindex fails, leaving the block
// marked failed against a database that already holds the change.
func TestApplyImagePostProcessRulesHydratesTheTenantContext(t *testing.T) {
	t.Parallel()

	textChunks := decorativeDocument()
	service, chunks := newImagePostProcessTestService(textChunks...)
	service.tenantRepo = &summaryRefreshTenantRepo{tenant: &types.Tenant{ID: 7}}

	service.applyImagePostProcessRules(
		context.Background(), 7, "k-1", dropRuleKB(true), textChunks, nil, nil)

	if len(chunks.calls) == 0 {
		t.Fatal("no write happened, so there is no context to inspect")
	}
	tenant, ok := types.TenantInfoFromContext(chunks.lastCtx)
	if !ok || tenant.ID != 7 {
		t.Fatalf("tenant in the write context = %#v, %v; want tenant 7", tenant, ok)
	}
}

// A tenant that cannot be loaded must not stop the rules. The edit is still
// correct and the database stays consistent, so the run continues on the
// context it was handed rather than leaving the decorative images in place.
func TestApplyImagePostProcessRulesRunsWhenTheTenantCannotBeLoaded(t *testing.T) {
	t.Parallel()

	textChunks := decorativeDocument()
	service, chunks := newImagePostProcessTestService(textChunks...)
	service.tenantRepo = &summaryRefreshTenantRepo{err: errors.New("lookup failed")}

	got := service.applyImagePostProcessRules(
		context.Background(), 7, "k-1", dropRuleKB(true), textChunks, nil, nil)

	if len(chunks.calls) != 1 {
		t.Fatalf("writes = %d, want 1: a tenant lookup failure must not skip the rules", len(chunks.calls))
	}
	if len(got) != len(textChunks) {
		t.Errorf("returned %d chunk(s), want %d", len(got), len(textChunks))
	}
	if _, ok := types.TenantInfoFromContext(chunks.lastCtx); ok {
		t.Error("a tenant was injected even though the lookup failed")
	}
}
