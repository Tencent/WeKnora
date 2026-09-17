package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---------------------------------------------------------------------------
// Phases and outcomes
// ---------------------------------------------------------------------------

// ActionPhase says when an image action runs relative to the language models
// that read a document's chunks. The split exists because two kinds of work have
// genuinely different deadlines: taking an image out of what a model will read
// has to happen before the model reads it, while reshuffling stored files has no
// such constraint and must not be allowed to slow a parse down.
type ActionPhase string

const (
	// ActionPhaseInline runs inside post-processing, after the chunks are
	// gathered and before graph selection and the subtask fan-out. An action
	// belongs here when skipping it would feed a model content the action was
	// meant to remove, or would make the expected-subtask count disagree with
	// what is actually enqueued.
	ActionPhaseInline ActionPhase = "inline"
	// ActionPhaseDeferred is for work that only touches stored files, so it need
	// not finish before the models run. The engine is built to report deferred
	// work, but executing it is deliberately out of scope until a file-level
	// action exists to exercise that path: an unexercised code path is a
	// liability, not a feature.
	ActionPhaseDeferred ActionPhase = "deferred"
)

// Action outcomes. An outcome always describes stored state, never intent.
const (
	// ActionResultApplied means the action changed stored state.
	ActionResultApplied = "applied"
	// ActionResultSkipped means the action inspected the image and decided
	// against changing it, so nothing was written.
	ActionResultSkipped = "skipped"
	// ActionResultDryRun means the action would have changed the image and did
	// not. It is distinct from "skipped" because it is the evidence a preview
	// shows.
	ActionResultDryRun = "dry_run"
	// ActionResultFailed means the action tried and did not succeed. The engine
	// records the failure and moves on: one image's failure must not cost the
	// document its remaining work.
	ActionResultFailed = "failed"
)

// ---------------------------------------------------------------------------
// Action contract
// ---------------------------------------------------------------------------

// ParamField is one configurable parameter of an action. It is what lets a user
// interface render an action's settings without being taught about the action.
type ParamField struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Type        string `json:"type"` // "string" | "bool" | "int"
	Description string `json:"description,omitempty"`
}

// ActionMeta describes an action to a caller that wants to present or validate
// it, as opposed to run it.
type ActionMeta struct {
	Name        string       `json:"name"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Phase       ActionPhase  `json:"phase"`
	ParamFields []ParamField `json:"param_fields,omitempty"`
}

// ActionRequest is the engine's call into an action: one image's unified view,
// the rule that selected it, and whether the caller wants a report instead of a
// change.
type ActionRequest struct {
	TenantID    uint64
	KnowledgeID string
	// Rule is the rule that matched, which carries the action's parameters and
	// the identity an audit record needs.
	Rule types.ImageRule
	// Candidate is the image to act on.
	Candidate *ImageCandidate
	// DryRun asks the action to report what it would do and change nothing.
	DryRun bool
}

// ActionResult reports what one action did to one image.
type ActionResult struct {
	Outcome string         `json:"outcome"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// ImageAction is the extension point. Adding one is meant to be adding a file:
// implement this interface and register the implementation. The rule that
// selects it lives in configuration and the form that configures it is rendered
// from Describe(), so neither the engine nor a user interface changes.
//
// An action that edits a chunk keeps req.Candidate.ParentChunk in step with
// what it stored — content, enabled state and revision. The engine hands the
// same block to every action that touches it, so without that the second edit
// would build on a copy that is already out of date, and its optimism check
// would fail against the revision the first edit produced.
type ImageAction interface {
	// Name is the identifier a rule refers to. It must be stable, because it is
	// what a stored rule means.
	Name() string
	// Phase says whether the action must run before the models read the
	// document.
	Phase() ActionPhase
	// Describe returns the action's presentation and parameter schema.
	Describe() ActionMeta
	// Apply performs the action on one image.
	Apply(ctx context.Context, req *ActionRequest) (*ActionResult, error)
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// ImageActionRegistry holds the actions the engine can dispatch to. It mirrors
// the plugin registry the chat pipeline already uses, so the pattern is one a
// reader of this repository has met before.
type ImageActionRegistry struct {
	mu      sync.RWMutex
	actions map[string]ImageAction
}

// NewImageActionRegistry returns a registry holding the given actions.
func NewImageActionRegistry(actions ...ImageAction) *ImageActionRegistry {
	registry := &ImageActionRegistry{actions: make(map[string]ImageAction, len(actions))}
	for _, action := range actions {
		registry.Register(action)
	}
	return registry
}

// Register adds an action, replacing any earlier one registered under the same
// name so a test can substitute a double.
func (r *ImageActionRegistry) Register(action ImageAction) {
	if action == nil || strings.TrimSpace(action.Name()) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.actions == nil {
		r.actions = make(map[string]ImageAction)
	}
	r.actions[action.Name()] = action
}

// Get looks an action up by the name a rule refers to.
func (r *ImageActionRegistry) Get(name string) (ImageAction, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	action, ok := r.actions[name]
	return action, ok
}

// List returns every registered action's metadata, ordered by name so a user
// interface and a test see the same sequence.
func (r *ImageActionRegistry) List() []ActionMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	metas := make([]ActionMeta, 0, len(r.actions))
	for _, action := range r.actions {
		metas = append(metas, action.Describe())
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Name < metas[j].Name })
	return metas
}

// Names returns the registered action names, ordered.
func (r *ImageActionRegistry) Names() []string {
	metas := r.List()
	names := make([]string, 0, len(metas))
	for _, meta := range metas {
		names = append(names, meta.Name)
	}
	return names
}

// ---------------------------------------------------------------------------
// Candidates
// ---------------------------------------------------------------------------

// ImageCandidate is one image's unified view: everything the matcher and the
// actions need, assembled once from the text chunk the image sits in and the
// image_caption / image_ocr children that hang off it.
type ImageCandidate struct {
	URL         string
	OriginalURL string
	// Class is the coarse category the describe round assigned, empty when no
	// classification ran for this image.
	Class string
	// Caption and OCRText are the children's own content. They are the same
	// text the language models see, which is what makes them usable as match
	// evidence.
	Caption string
	OCRText string
	// ParentChunk is the text chunk that references the image. An action that
	// edits the body edits this chunk.
	ParentChunk *types.Chunk
	// CaptionChunk and OCRChunk are the rows the text came from, so an action
	// can act on the exact row instead of re-deriving which one it read.
	CaptionChunk *types.Chunk
	OCRChunk     *types.Chunk
}

// BuildImageCandidates groups one text chunk's image children by image URL, so
// an image's description and its OCR text meet on the same candidate.
//
// Pairing by URL rather than by parent chunk is the point. A text chunk
// routinely holds several images, and pairing by parent would let one image's
// OCR text answer for its neighbours — which for a cleanup rule means a
// decorative image gets vouched for by an unrelated diagram in the same block.
//
// Disabled children are ignored. A child that was already switched off is not
// evidence about the image's content, and reading it would let a rule act on
// data that was deliberately retired.
func BuildImageCandidates(parent *types.Chunk, children []*types.Chunk) []*ImageCandidate {
	if parent == nil {
		return nil
	}
	byURL := make(map[string]*ImageCandidate)
	var ordered []*ImageCandidate

	for _, child := range children {
		if child == nil || !child.IsEnabled || child.ParentChunkID != parent.ID {
			continue
		}
		if child.ChunkType != types.ChunkTypeImageCaption && child.ChunkType != types.ChunkTypeImageOCR {
			continue
		}
		for _, info := range decodeChunkImageInfos(child.ImageInfo) {
			key := infoKey(info)
			if key == "" {
				// Without a URL the image cannot be told apart from any other,
				// so it cannot be matched by URL — which is the only safe way to
				// pair its signals. Leave it out instead of guessing.
				continue
			}
			candidate, ok := byURL[key]
			if !ok {
				candidate = &ImageCandidate{
					URL:         info.URL,
					OriginalURL: info.OriginalURL,
					Class:       info.Class,
					ParentChunk: parent,
				}
				byURL[key] = candidate
				ordered = append(ordered, candidate)
			}
			mergeCandidateImageInfo(candidate, info)
			// The child row's own content is what the language models are given,
			// so it wins for the field its chunk type owns; image_info only fills
			// in what the row itself does not carry. Reading image_info alone
			// would hand a rule a description the model never saw.
			switch child.ChunkType {
			case types.ChunkTypeImageCaption:
				candidate.CaptionChunk = child
				if content := strings.TrimSpace(child.Content); content != "" {
					candidate.Caption = content
				}
			case types.ChunkTypeImageOCR:
				candidate.OCRChunk = child
				if content := strings.TrimSpace(child.Content); content != "" {
					candidate.OCRText = content
				}
			}
		}
	}

	// A stable order keeps a plan reproducible and a test readable.
	sort.SliceStable(ordered, func(i, j int) bool { return imageCandidateKey(ordered[i]) < imageCandidateKey(ordered[j]) })
	return ordered
}

// mergeCandidateImageInfo folds one image_info entry into a candidate. The
// child chunk's own content wins for the text it carries, because that is the
// copy the models are given; the image_info payload fills in whatever the
// child's content leaves out.
func mergeCandidateImageInfo(candidate *ImageCandidate, info types.ImageInfo) {
	if candidate.Class == "" {
		candidate.Class = info.Class
	}
	if candidate.URL == "" {
		candidate.URL = info.URL
	}
	if candidate.OriginalURL == "" {
		candidate.OriginalURL = info.OriginalURL
	}
	if candidate.Caption == "" {
		candidate.Caption = info.Caption
	}
	if candidate.OCRText == "" {
		candidate.OCRText = info.OCRText
	}
}

// imageCandidateKey is the identity a candidate is ordered and deduplicated by.
func imageCandidateKey(candidate *ImageCandidate) string {
	if candidate == nil {
		return ""
	}
	if candidate.URL != "" {
		return candidate.URL
	}
	return candidate.OriginalURL
}

func infoKey(info types.ImageInfo) string {
	if info.URL != "" {
		return info.URL
	}
	return info.OriginalURL
}

// decodeChunkImageInfos reads a child chunk's image_info payload. A malformed
// payload yields nothing rather than an error: the image simply has no
// metadata, which the matcher already treats as "cannot be classified".
func decodeChunkImageInfos(raw string) []types.ImageInfo {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(raw), &infos); err != nil {
		return nil
	}
	return infos
}

// ---------------------------------------------------------------------------
// Matching
//
// The rule data types (ImageRule, ImageMatchSpec) live in types so that
// knowledge base configuration can reference them without importing this
// package. What stays here is the behaviour: compiling, matching, planning.
// ---------------------------------------------------------------------------

// compiledMatchSpec is a spec with its pattern compiled once, so a rule is not
// recompiled for every image in a document.
type compiledMatchSpec struct {
	spec     types.ImageMatchSpec
	regex    *regexp.Regexp
	ocrState string
	// err records why the spec cannot be applied at all. A rule the engine
	// cannot evaluate must not act, so this fails the rule closed rather than
	// open.
	err error
}

// compileMatchSpec validates a spec and prepares it for repeated matching. The
// returned value carries an error instead of failing loudly, because a single
// malformed rule should be reported and skipped, not stop a document from being
// processed.
func compileMatchSpec(spec types.ImageMatchSpec) compiledMatchSpec {
	compiled := compiledMatchSpec{spec: spec}

	for _, class := range spec.Classes {
		if !types.IsKnownImageClass(class) {
			compiled.err = fmt.Errorf("class %q is not a known image class (%s)",
				class, types.ImageClassList())
			return compiled
		}
	}

	if spec.CaptionRegex != "" {
		if len(spec.CaptionRegex) > types.MaxImageRuleRegexLength {
			compiled.err = fmt.Errorf("caption_regex is %d characters, over the %d limit",
				len(spec.CaptionRegex), types.MaxImageRuleRegexLength)
			return compiled
		}
		regex, err := regexp.Compile(spec.CaptionRegex)
		if err != nil {
			compiled.err = fmt.Errorf("caption_regex does not compile: %w", err)
			return compiled
		}
		compiled.regex = regex
	}

	ocrState := strings.ToLower(strings.TrimSpace(spec.OCRState))
	switch ocrState {
	case types.OCRStateAny, types.OCRStateEmpty, types.OCRStateNonEmpty:
		compiled.ocrState = ocrState
	default:
		compiled.err = fmt.Errorf("ocr_state %q is not one of %q or %q",
			spec.OCRState, types.OCRStateEmpty, types.OCRStateNonEmpty)
	}
	return compiled
}

// matches reports whether one image satisfies the spec.
func (c compiledMatchSpec) matches(candidate *ImageCandidate) bool {
	if c.err != nil || candidate == nil {
		return false
	}
	if len(c.spec.Classes) > 0 && !matchesAnyClass(c.spec.Classes, candidate.Class) {
		return false
	}

	caption := normalizeCaptionText(candidate.Caption)
	if len(c.spec.CaptionEquals) > 0 && !matchesAnyNormalizedCaption(c.spec.CaptionEquals, caption) {
		return false
	}
	if len(c.spec.CaptionContains) > 0 && !containsAnyNormalizedSubstring(c.spec.CaptionContains, caption) {
		return false
	}
	if c.regex != nil && !c.regex.MatchString(caption) {
		return false
	}

	hasOCR := strings.TrimSpace(candidate.OCRText) != ""
	switch c.ocrState {
	case types.OCRStateEmpty:
		return !hasOCR
	case types.OCRStateNonEmpty:
		return hasOCR
	default:
		return true
	}
}

// empty reports whether the spec states no condition at all. Such a rule would
// match every image, so the planner refuses it rather than letting a
// configuration slip become a document-wide action.
func (c compiledMatchSpec) empty() bool {
	spec := c.spec
	return len(spec.Classes) == 0 &&
		len(spec.CaptionEquals) == 0 &&
		len(spec.CaptionContains) == 0 &&
		spec.CaptionRegex == "" &&
		strings.TrimSpace(spec.OCRState) == ""
}

// normalizeCaptionText is the comparison form for captions. It removes what a
// model adds around an answer — surrounding whitespace, wrapping quotes, a
// trailing sentence mark — and folds case, so that a marker meaning
// "decorative" is recognised however the model decorated it.
//
// Brackets are deliberately NOT peeled. The conventional marker is itself
// bracket-wrapped ("[[decorative]]"), so treating brackets as wrapper noise
// would strip the very characters being compared and the marker could never
// match. A bracketed wrapper around a bracket-less marker is therefore left
// alone, which costs a missed match — and a miss keeps the image, the direction
// this comparison errs in on purpose.
//
// It also does no containment and no stemming: a comparison that is forgiving
// about words would match "not a decorative image" too.
func normalizeCaptionText(s string) string {
	// Only quotes are treated as wrappers. Every bracket form is plausible
	// marker syntax, so none of them is removed.
	const (
		wrapCutset  = "\"'“”‘’"
		trailCutset = "。.!！?？,，;；:：、"
	)
	// A model may put the sentence mark outside the quotes or the other way
	// round, so peel both until the string stops shrinking.
	previous := ""
	for previous != s {
		previous = s
		s = strings.TrimSpace(s)
		s = strings.Trim(s, wrapCutset)
		s = strings.TrimSpace(s)
		s = strings.TrimRight(s, trailCutset)
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func matchesAnyClass(classes []string, class string) bool {
	raw := strings.TrimSpace(class)
	if raw == "" {
		// An unclassified row — written before classification shipped, or by a
		// describe round that ignored the format — must not be swept up by a
		// class rule. Folding it into "other" would silently apply the rule to
		// every image the pipeline never classified.
		return false
	}
	normalized := types.NormalizeImageClass(raw)
	for _, want := range classes {
		if types.NormalizeImageClass(want) == normalized {
			return true
		}
	}
	return false
}

func matchesAnyNormalizedCaption(wants []string, caption string) bool {
	for _, want := range wants {
		if normalizeCaptionText(want) == caption {
			return true
		}
	}
	return false
}

func containsAnyNormalizedSubstring(wants []string, caption string) bool {
	if caption == "" {
		return false
	}
	for _, want := range wants {
		needle := normalizeCaptionText(want)
		if needle == "" {
			continue
		}
		if strings.Contains(caption, needle) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Planning
// ---------------------------------------------------------------------------

// ImageActionMatch is one planned execution: the rule that matched, the image it
// matched, and the registered action that will run.
type ImageActionMatch struct {
	Rule      types.ImageRule
	Action    ImageAction
	Candidate *ImageCandidate
}

// PlanImageActions decides what should happen to each image.
//
// Rules are tried in order and the first enabled rule that matches an image owns
// it. A later rule does not get to restate a decision, which is also what keeps
// two rules from both acting on one image.
//
// The returned notes name the rules that were skipped and why (unknown action,
// unusable pattern, no conditions). They are reported rather than returned as an
// error: one malformed rule must not stop a document from being processed, but
// it must not pass silently either.
func PlanImageActions(
	rules []types.ImageRule, registry *ImageActionRegistry, candidates []*ImageCandidate,
) ([]ImageActionMatch, []string) {
	if registry == nil || len(candidates) == 0 {
		return nil, nil
	}

	var matches []ImageActionMatch
	var notes []string
	claimed := make(map[*ImageCandidate]bool, len(candidates))

	for _, rule := range rules {
		if !rule.IsEnabled() {
			continue
		}
		action, ok := registry.Get(rule.Action)
		if !ok {
			notes = append(notes, fmt.Sprintf("rule %s: no action registered as %q",
				ruleLabel(rule), rule.Action))
			continue
		}
		compiled := compileMatchSpec(rule.Match)
		if compiled.err != nil {
			notes = append(notes, fmt.Sprintf("rule %s: %v", ruleLabel(rule), compiled.err))
			continue
		}
		if compiled.empty() {
			notes = append(notes, fmt.Sprintf("rule %s: has no match conditions", ruleLabel(rule)))
			continue
		}
		for _, candidate := range candidates {
			if claimed[candidate] {
				continue
			}
			if !compiled.matches(candidate) {
				continue
			}
			claimed[candidate] = true
			matches = append(matches, ImageActionMatch{Rule: rule, Action: action, Candidate: candidate})
		}
	}
	return matches, notes
}

// ruleLabel names a rule the way a person would recognise it, falling back to
// the id and finally to a placeholder so a note is always readable.
func ruleLabel(rule types.ImageRule) string {
	if name := strings.TrimSpace(rule.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(rule.ID); id != "" {
		return id
	}
	return "(unnamed rule)"
}
