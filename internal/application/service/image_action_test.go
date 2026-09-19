package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// fakeImageAction is a registered action that does nothing but report. It is how
// the planner's decisions are observed without any action implementation in the
// way.
type fakeImageAction struct {
	name  string
	phase ActionPhase
	meta  ActionMeta
}

func (f *fakeImageAction) Name() string { return f.name }

func (f *fakeImageAction) Phase() ActionPhase {
	if f.phase == "" {
		return ActionPhaseInline
	}
	return f.phase
}

func (f *fakeImageAction) Describe() ActionMeta {
	meta := f.meta
	if meta.Name == "" {
		meta.Name = f.name
	}
	if meta.Phase == "" {
		meta.Phase = f.Phase()
	}
	return meta
}

func (f *fakeImageAction) Apply(_ context.Context, _ *ActionRequest) (*ActionResult, error) {
	return &ActionResult{Outcome: ActionResultApplied}, nil
}

// imageInfoJSON renders image_info entries the way the pipeline stores them.
func imageInfoJSON(t *testing.T, infos ...types.ImageInfo) string {
	t.Helper()
	data, err := json.Marshal(infos)
	if err != nil {
		t.Fatalf("marshal image info: %v", err)
	}
	return string(data)
}

// imageChild builds an image_caption / image_ocr child row.
func imageChild(id, parentID, chunkType, content string, enabled bool, infos ...types.ImageInfo) *types.Chunk {
	return &types.Chunk{
		ID:            id,
		ParentChunkID: parentID,
		ChunkType:     chunkType,
		Content:       content,
		IsEnabled:     enabled,
		ImageInfo:     mustMarshalImageInfos(infos),
	}
}

func mustMarshalImageInfos(infos []types.ImageInfo) string {
	if len(infos) == 0 {
		return ""
	}
	data, err := json.Marshal(infos)
	if err != nil {
		return ""
	}
	return string(data)
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

func TestImageActionRegistryRegistersAndLists(t *testing.T) {
	t.Parallel()

	beta := &fakeImageAction{name: "beta", meta: ActionMeta{Title: "Beta"}}
	alpha := &fakeImageAction{name: "alpha", meta: ActionMeta{Title: "Alpha"}}
	registry := NewImageActionRegistry(beta, alpha)

	if names := registry.Names(); len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("Names() = %v, want [alpha beta] (sorted)", names)
	}
	if metas := registry.List(); len(metas) != 2 || metas[0].Name != "alpha" || metas[0].Title != "Alpha" {
		t.Fatalf("List() = %+v, want alpha first with its title", metas)
	}
	if got, ok := registry.Get("beta"); !ok || got != ImageAction(beta) {
		t.Fatalf("Get(beta) = %v, %v; want the registered action", got, ok)
	}
	if _, ok := registry.Get("missing"); ok {
		t.Error("Get(missing) reported an action")
	}
}

func TestImageActionRegistryIgnoresUnnameableActions(t *testing.T) {
	t.Parallel()

	registry := NewImageActionRegistry()
	registry.Register(nil)
	registry.Register(&fakeImageAction{name: "   "})
	if names := registry.Names(); len(names) != 0 {
		t.Fatalf("Names() = %v, want none: an action with no name cannot be referred to", names)
	}
}

func TestImageActionRegistryReplacesSameName(t *testing.T) {
	t.Parallel()

	first := &fakeImageAction{name: "same", meta: ActionMeta{Title: "First"}}
	second := &fakeImageAction{name: "same", meta: ActionMeta{Title: "Second"}}
	registry := NewImageActionRegistry(first)
	registry.Register(second)

	got, ok := registry.Get("same")
	if !ok || got != ImageAction(second) {
		t.Fatalf("Get(same) = %v, want the later registration", got)
	}
	if metas := registry.List(); len(metas) != 1 {
		t.Fatalf("List() = %+v, want one entry: the name is the key", metas)
	}
}

// ---------------------------------------------------------------------------
// Caption normalisation
// ---------------------------------------------------------------------------

func TestNormalizeCaptionText(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"[[decorative]]":     "[[decorative]]",
		"  [[decorative]]  ": "[[decorative]]",
		`"[[decorative]]"`:   "[[decorative]]",
		"'[[decorative]]'":   "[[decorative]]",
		"[[decorative]].":    "[[decorative]]",
		"[[decorative]]。":    "[[decorative]]",
		"[[decorative]]，":    "[[decorative]]",
		"[[DECORATIVE]]":     "[[decorative]]",
		"[[Decorative]]。":    "[[decorative]]",
		"“[[decorative]]”":   "[[decorative]]",
		"“[[DECORATIVE]]”。":  "[[decorative]]",
		"":                   "",
		"   ":                "",
		"A wiring diagram.":  "a wiring diagram",
		"No text content":    "no text content",
		// Brackets are not peeled, so a bracket wrapper around a bracket-less
		// marker survives the comparison. That is the accepted cost of never
		// stripping the marker's own characters: the image is kept, which is a
		// miss rather than a wrongful removal.
		"（[[decorative]]）":    "（[[decorative]]）",
		"[[decorative]]extra": "[[decorative]]extra",
	}
	for raw, want := range cases {
		if got := normalizeCaptionText(raw); got != want {
			t.Errorf("normalizeCaptionText(%q) = %q, want %q", raw, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Match spec compilation
// ---------------------------------------------------------------------------

func TestCompileMatchSpecRejectsUnusableRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec types.ImageMatchSpec
	}{
		{"unknown class", types.ImageMatchSpec{Classes: []string{"decorativ"}}},
		{"class that is only punctuation", types.ImageMatchSpec{Classes: []string{"***"}}},
		{"pattern does not compile", types.ImageMatchSpec{CaptionRegex: "([unclosed"}},
		{
			"pattern over the cap",
			types.ImageMatchSpec{CaptionRegex: strings.Repeat("a", types.MaxImageRuleRegexLength+1)},
		},
		{"unknown ocr_state", types.ImageMatchSpec{OCRState: "sometimes"}},
	}
	for _, tc := range cases {
		if compiled := compileMatchSpec(tc.spec); compiled.err == nil {
			t.Errorf("%s: compileMatchSpec accepted an unusable rule: %+v", tc.name, tc.spec)
		}
	}

	valid := compileMatchSpec(types.ImageMatchSpec{
		Classes:         []string{"decorative", "logo"},
		CaptionEquals:   []string{"[[decorative]]"},
		CaptionContains: []string{"divider"},
		CaptionRegex:    `^\[\[decorative\]\]$`,
		OCRState:        types.OCRStateEmpty,
	})
	if valid.err != nil {
		t.Fatalf("a well-formed spec was rejected: %v", valid.err)
	}
}

func TestCompiledMatchSpecMatches(t *testing.T) {
	t.Parallel()

	decorative := &ImageCandidate{Class: "decorative", Caption: "[[decorative]]"}
	decorativeWithOCR := &ImageCandidate{Class: "decorative", Caption: "[[decorative]]", OCRText: "ALARM 12"}
	logo := &ImageCandidate{Class: "logo", Caption: "B-ENERGY 博能控股"}
	chart := &ImageCandidate{Class: "chart", Caption: "A wiring diagram.", OCRText: "PIN 1"}
	unclassified := &ImageCandidate{Caption: "[[decorative]]"}

	cases := []struct {
		name      string
		spec      types.ImageMatchSpec
		candidate *ImageCandidate
		want      bool
	}{
		{"class match", types.ImageMatchSpec{Classes: []string{"decorative"}}, decorative, true},
		{"logo class match", types.ImageMatchSpec{Classes: []string{"logo"}}, logo, true},
		{
			"logo is no longer an alias of decorative",
			types.ImageMatchSpec{Classes: []string{"logo"}},
			decorative, false,
		},
		{
			"decorative rule does not take a logo",
			types.ImageMatchSpec{Classes: []string{"decorative"}},
			logo, false,
		},
		{"class mismatch", types.ImageMatchSpec{Classes: []string{"decorative"}}, chart, false},
		{"class list is alternatives", types.ImageMatchSpec{Classes: []string{"chart", "decorative"}}, chart, true},
		{
			"unclassified never matches a class rule",
			types.ImageMatchSpec{Classes: []string{"other"}},
			unclassified, false,
		},
		{"caption equals", types.ImageMatchSpec{CaptionEquals: []string{"[[decorative]]"}}, decorative, true},
		{
			"caption equals compares the normalised forms",
			types.ImageMatchSpec{CaptionEquals: []string{`"[[Decorative]]"。`}},
			decorative, true,
		},
		{
			"caption equals is equality, not containment",
			types.ImageMatchSpec{CaptionEquals: []string{"decorative"}},
			&ImageCandidate{Caption: "this is not a decorative image"},
			false,
		},
		{"caption contains", types.ImageMatchSpec{CaptionContains: []string{"divider"}}, decorative, false},
		{
			"caption contains hits",
			types.ImageMatchSpec{CaptionContains: []string{"divider"}},
			&ImageCandidate{Caption: "A plain divider rule."}, true,
		},
		{"regex hits", types.ImageMatchSpec{CaptionRegex: `^\[\[decorative\]\]$`}, decorative, true},
		{"regex misses", types.ImageMatchSpec{CaptionRegex: `^\[\[decorative\]\]$`}, chart, false},
		{"ocr empty holds", types.ImageMatchSpec{OCRState: types.OCRStateEmpty}, decorative, true},
		{"ocr empty fails on text", types.ImageMatchSpec{OCRState: types.OCRStateEmpty}, decorativeWithOCR, false},
		{"ocr non-empty holds", types.ImageMatchSpec{OCRState: types.OCRStateNonEmpty}, chart, true},
		{"ocr non-empty fails on empty", types.ImageMatchSpec{OCRState: types.OCRStateNonEmpty}, decorative, false},
		{"ocr any places no condition", types.ImageMatchSpec{OCRState: types.OCRStateAny}, chart, true},
		{
			"conditions are conjunctive",
			types.ImageMatchSpec{Classes: []string{"decorative"}, OCRState: types.OCRStateEmpty},
			decorativeWithOCR, false,
		},
		{
			"the double signal holds together",
			types.ImageMatchSpec{Classes: []string{"decorative"}, OCRState: types.OCRStateEmpty},
			decorative, true,
		},
	}
	for _, tc := range cases {
		compiled := compileMatchSpec(tc.spec)
		if compiled.err != nil {
			t.Errorf("%s: unexpected compile error: %v", tc.name, compiled.err)
			continue
		}
		if got := compiled.matches(tc.candidate); got != tc.want {
			t.Errorf("%s: matches() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCompiledMatchSpecEmptyDetection(t *testing.T) {
	t.Parallel()

	if !compileMatchSpec(types.ImageMatchSpec{}).empty() {
		t.Error("a spec with no conditions should be reported empty")
	}
	if compileMatchSpec(types.ImageMatchSpec{OCRState: "  "}).empty() != true {
		t.Error("whitespace-only ocr_state should not count as a condition")
	}
	if compileMatchSpec(types.ImageMatchSpec{Classes: []string{"photo"}}).empty() {
		t.Error("a spec with a class condition should not be reported empty")
	}
}

// ---------------------------------------------------------------------------
// Candidate assembly
// ---------------------------------------------------------------------------

// TestBuildImageCandidatesPairsByURL is the load-bearing test for cleanup rules.
// A text chunk holding two images must produce two candidates, each carrying its
// OWN description and OCR text — pairing by parent chunk instead would let the
// diagram's OCR text vouch for the decorative image beside it.
func TestBuildImageCandidatesPairsByURL(t *testing.T) {
	t.Parallel()

	parent := &types.Chunk{ID: "text-1", ChunkType: types.ChunkTypeText, Content: "body"}
	children := []*types.Chunk{
		imageChild("cap-a", "text-1", types.ChunkTypeImageCaption, "[[decorative]]", true,
			types.ImageInfo{URL: "local://img/a.png", Class: "decorative", Caption: "[[decorative]]"}),
		imageChild("ocr-b", "text-1", types.ChunkTypeImageOCR, "PIN 1", true,
			types.ImageInfo{URL: "local://img/b.png", Class: "chart", OCRText: "PIN 1"}),
		imageChild("cap-b", "text-1", types.ChunkTypeImageCaption, "A wiring diagram.", true,
			types.ImageInfo{URL: "local://img/b.png", Class: "chart", Caption: "A wiring diagram."}),
	}

	candidates := BuildImageCandidates(parent, children)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2 (one per image URL): %+v", len(candidates), candidates)
	}

	byURL := make(map[string]*ImageCandidate, len(candidates))
	for _, candidate := range candidates {
		byURL[candidate.URL] = candidate
	}

	decorative, ok := byURL["local://img/a.png"]
	if !ok {
		t.Fatal("the decorative image has no candidate")
	}
	if decorative.Class != "decorative" || decorative.Caption != "[[decorative]]" {
		t.Errorf("decorative candidate = %+v, want its own class and caption", decorative)
	}
	if decorative.OCRText != "" {
		t.Errorf("decorative candidate picked up OCR text %q from another image", decorative.OCRText)
	}
	if decorative.CaptionChunk == nil || decorative.CaptionChunk.ID != "cap-a" {
		t.Errorf("decorative candidate should point at its caption row, got %+v", decorative.CaptionChunk)
	}

	diagram, ok := byURL["local://img/b.png"]
	if !ok {
		t.Fatal("the diagram has no candidate")
	}
	if diagram.Caption != "A wiring diagram." || diagram.OCRText != "PIN 1" {
		t.Errorf("diagram candidate = %+v, want both its caption and its OCR text joined", diagram)
	}
	if diagram.CaptionChunk == nil || diagram.CaptionChunk.ID != "cap-b" ||
		diagram.OCRChunk == nil || diagram.OCRChunk.ID != "ocr-b" {
		t.Errorf("diagram candidate should hold both of its rows: %+v", diagram)
	}
	if diagram.ParentChunk != parent {
		t.Error("candidate should carry the chunk an action must edit")
	}
}

func TestBuildImageCandidatesIgnoresDisabledAndForeignChildren(t *testing.T) {
	t.Parallel()

	parent := &types.Chunk{ID: "text-1", ChunkType: types.ChunkTypeText}
	children := []*types.Chunk{
		imageChild("cap-off", "text-1", types.ChunkTypeImageCaption, "[[decorative]]", false,
			types.ImageInfo{URL: "local://img/off.png", Class: "decorative"}),
		imageChild("cap-foreign", "text-2", types.ChunkTypeImageCaption, "elsewhere", true,
			types.ImageInfo{URL: "local://img/foreign.png"}),
		imageChild("cap-text", "text-1", types.ChunkTypeText, "not an image child", true,
			types.ImageInfo{URL: "local://img/text.png"}),
		imageChild("cap-nourl", "text-1", types.ChunkTypeImageCaption, "no url", true,
			types.ImageInfo{Class: "decorative", Caption: "no url"}),
	}

	if candidates := BuildImageCandidates(parent, children); len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want none: disabled, foreign, non-image and URL-less children are not evidence",
			candidates)
	}
	if candidates := BuildImageCandidates(nil, children); candidates != nil {
		t.Errorf("a nil parent should yield no candidates, got %+v", candidates)
	}
}

func TestBuildImageCandidatesToleratesMalformedImageInfo(t *testing.T) {
	t.Parallel()

	parent := &types.Chunk{ID: "text-1", ChunkType: types.ChunkTypeText}
	children := []*types.Chunk{{
		ID:            "cap-broken",
		ParentChunkID: "text-1",
		ChunkType:     types.ChunkTypeImageCaption,
		Content:       "a caption with no metadata",
		IsEnabled:     true,
		ImageInfo:     "{not json",
	}}

	if candidates := BuildImageCandidates(parent, children); len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want none: without a URL the image cannot be paired by one", candidates)
	}
}

func TestBuildImageCandidatesFillsFromImageInfo(t *testing.T) {
	t.Parallel()

	parent := &types.Chunk{ID: "text-1", ChunkType: types.ChunkTypeText}
	children := []*types.Chunk{{
		ID:            "cap-1",
		ParentChunkID: "text-1",
		ChunkType:     types.ChunkTypeImageCaption,
		// Empty content: the description lives only in the image_info payload,
		// which is what a row written before the child content was populated
		// looks like.
		Content:   "",
		IsEnabled: true,
		ImageInfo: imageInfoJSON(t, types.ImageInfo{
			URL: "local://img/c.png", Class: "photo", Caption: "a pump housing",
		}),
	}}

	candidates := BuildImageCandidates(parent, children)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if candidates[0].Caption != "a pump housing" || candidates[0].Class != "photo" {
		t.Errorf("candidate = %+v, want caption and class read from image_info", candidates[0])
	}
}

func TestBuildImageCandidatesPrefersChildContent(t *testing.T) {
	t.Parallel()

	parent := &types.Chunk{ID: "text-1", ChunkType: types.ChunkTypeText}
	caption := imageChild("cap-1", "text-1", types.ChunkTypeImageCaption, "the current caption", true,
		types.ImageInfo{URL: "local://img/d.png", Caption: "a stale caption"})
	ocr := imageChild("ocr-1", "text-1", types.ChunkTypeImageOCR, "the current OCR text", true,
		types.ImageInfo{URL: "local://img/d.png", OCRText: "a stale OCR text"})

	candidates := BuildImageCandidates(parent, []*types.Chunk{ocr, caption})
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if candidates[0].Caption != "the current caption" {
		t.Errorf("caption = %q, want the child row's own content", candidates[0].Caption)
	}
	if candidates[0].OCRText != "the current OCR text" {
		t.Errorf("ocr = %q, want the child row's own content", candidates[0].OCRText)
	}
}

// ---------------------------------------------------------------------------
// Planning
// ---------------------------------------------------------------------------

func TestPlanImageActionsFirstMatchWins(t *testing.T) {
	t.Parallel()

	first := &fakeImageAction{name: "first"}
	second := &fakeImageAction{name: "second"}
	registry := NewImageActionRegistry(first, second)

	rules := []types.ImageRule{
		{ID: "r1", Name: "narrow", Match: types.ImageMatchSpec{Classes: []string{"decorative"}}, Action: "first"},
		{ID: "r2", Name: "broad", Match: types.ImageMatchSpec{OCRState: types.OCRStateEmpty}, Action: "second"},
	}
	candidates := []*ImageCandidate{
		{URL: "local://img/a.png", Class: "decorative", Caption: "[[decorative]]"},
		{URL: "local://img/b.png", Class: "photo", Caption: "a pump housing"},
	}

	matches, notes := PlanImageActions(rules, registry, candidates)
	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none", notes)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2 (one per image)", len(matches))
	}
	if matches[0].Candidate.URL != "local://img/a.png" || matches[0].Action.Name() != "first" {
		t.Errorf("first image should be owned by the earlier rule, got %+v", matches[0])
	}
	// The photo has no OCR text, so the second rule matches it — and it is the
	// only rule left that can.
	if matches[1].Candidate.URL != "local://img/b.png" || matches[1].Action.Name() != "second" {
		t.Errorf("second image should be owned by the later rule, got %+v", matches[1])
	}
}

func TestPlanImageActionsLeavesUnmatchedImagesAlone(t *testing.T) {
	t.Parallel()

	registry := NewImageActionRegistry(&fakeImageAction{name: "act"})
	rules := []types.ImageRule{{Match: types.ImageMatchSpec{Classes: []string{"decorative"}}, Action: "act"}}
	candidates := []*ImageCandidate{{URL: "local://img/a.png", Class: "chart"}}

	matches, notes := PlanImageActions(rules, registry, candidates)
	if len(matches) != 0 || len(notes) != 0 {
		t.Fatalf("matches = %+v, notes = %v; want nothing for an image no rule claims", matches, notes)
	}
}

func TestPlanImageActionsSkipsDisabledRules(t *testing.T) {
	t.Parallel()

	registry := NewImageActionRegistry(&fakeImageAction{name: "act"})
	rules := []types.ImageRule{{
		ID: "r1", Enabled: boolPtr(false),
		Match: types.ImageMatchSpec{Classes: []string{"decorative"}}, Action: "act",
	}}
	candidates := []*ImageCandidate{{URL: "local://img/a.png", Class: "decorative"}}

	matches, notes := PlanImageActions(rules, registry, candidates)
	if len(matches) != 0 {
		t.Fatalf("matches = %+v, want none: the rule is switched off", matches)
	}
	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none: a disabled rule is a decision, not a problem", notes)
	}
}

func TestPlanImageActionsReportsUnusableRules(t *testing.T) {
	t.Parallel()

	registry := NewImageActionRegistry(&fakeImageAction{name: "act"})
	candidates := []*ImageCandidate{{URL: "local://img/a.png", Class: "decorative"}}
	rules := []types.ImageRule{
		{
			ID: "r1", Name: "no such action", Action: "ghost",
			Match: types.ImageMatchSpec{Classes: []string{"decorative"}},
		},
		{ID: "r2", Name: "no conditions", Action: "act"},
		{ID: "r3", Name: "bad pattern", Match: types.ImageMatchSpec{CaptionRegex: "("}, Action: "act"},
		{Name: "bad state", Match: types.ImageMatchSpec{OCRState: "maybe"}, Action: "act"},
	}

	matches, notes := PlanImageActions(rules, registry, candidates)
	if len(matches) != 0 {
		t.Fatalf("matches = %+v, want none: every rule here is unusable", matches)
	}
	if len(notes) != 4 {
		t.Fatalf("notes = %d, want 4 — one per unusable rule: %v", len(notes), notes)
	}
	for _, want := range []string{"no such action", "no conditions", "bad pattern", "bad state"} {
		if !containsNote(notes, want) {
			t.Errorf("notes %v do not mention %q", notes, want)
		}
	}
}

func TestPlanImageActionsHandlesEmptyInputs(t *testing.T) {
	t.Parallel()

	registry := NewImageActionRegistry(&fakeImageAction{name: "act"})
	candidates := []*ImageCandidate{{URL: "local://img/a.png"}}
	rules := []types.ImageRule{{Match: types.ImageMatchSpec{Classes: []string{"decorative"}}, Action: "act"}}

	if matches, notes := PlanImageActions(rules, nil, candidates); matches != nil || notes != nil {
		t.Errorf("a nil registry should yield nothing, got %+v / %v", matches, notes)
	}
	if matches, notes := PlanImageActions(rules, registry, nil); matches != nil || notes != nil {
		t.Errorf("no candidates should yield nothing, got %+v / %v", matches, notes)
	}
	if matches, notes := PlanImageActions(nil, registry, candidates); matches != nil || notes != nil {
		t.Errorf("no rules should yield nothing, got %+v / %v", matches, notes)
	}
}

func TestImageRuleIsEnabledDefaultsOn(t *testing.T) {
	t.Parallel()

	if !(types.ImageRule{}).IsEnabled() {
		t.Error("a rule without an enabled flag should run")
	}
	if !(types.ImageRule{Enabled: boolPtr(true)}).IsEnabled() {
		t.Error("an explicitly enabled rule should run")
	}
	if (types.ImageRule{Enabled: boolPtr(false)}).IsEnabled() {
		t.Error("an explicitly disabled rule should not run")
	}
}

func TestRuleLabelFallsBackToID(t *testing.T) {
	t.Parallel()

	if got := ruleLabel(types.ImageRule{Name: "named", ID: "r1"}); got != "named" {
		t.Errorf("ruleLabel = %q, want the name", got)
	}
	if got := ruleLabel(types.ImageRule{ID: "r1"}); got != "r1" {
		t.Errorf("ruleLabel = %q, want the id", got)
	}
	if got := ruleLabel(types.ImageRule{}); got == "" {
		t.Error("ruleLabel should never be empty")
	}
}

func containsNote(notes []string, want string) bool {
	for _, note := range notes {
		if strings.Contains(note, want) {
			return true
		}
	}
	return false
}
