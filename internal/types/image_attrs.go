package types

import "strings"

// AttrType is the shape of an observed attribute value.
type AttrType string

const (
	// AttrTypeExtent is an ordered set of values, e.g. none < sparse < block.
	AttrTypeExtent AttrType = "extent"
	// AttrTypePresence is a boolean observation, e.g. true / false.
	AttrTypePresence AttrType = "presence"
)

// AttrSpec is one observable image attribute. The registry is the single source
// of truth for which attributes exist this release; adding an attribute is one
// line here plus a decision clause, and never a struct change elsewhere. That
// is what makes "add an attribute = add a row" a real, low-risk operation.
//
// AttrValue is one allowed value of an attribute: the raw value plus the
// words to show for it.
//
// Display text is split in two on purpose. Label is the short name that fits
// on a filter checkbox or a table cell; Description is the sentence that
// explains the value where there is room (a tooltip, the settings panel).
// Both are written in the project's default language; the frontend overlays
// its translations on top (see imageAttrDisplay in the web app) and falls
// back to these strings for anything not translated yet.
type AttrValue struct {
	Value       string `json:"value"`                  // raw machine value, never localized
	Label       string `json:"label"`                  // short on-screen name
	Description string `json:"description,omitempty"`  // sentence explaining the value
}

// The same row also carries the attribute's display text. The settings panel
// that explains the attributes to a non-technical operator is rendered from
// these fields, so adding an attribute needs no frontend change either: Label,
// Description and the per-value texts travel to the UI through the schema
// endpoint. An attribute is always accompanied by a short label for compact
// spaces and a sentence for places with room.
type AttrSpec struct {
	Name        string      `json:"name"`             // stable key, e.g. "contain.text"
	Type        AttrType    `json:"type"`             // extent | presence
	Values      []AttrValue `json:"values,omitempty"` // ordered low->high for extent attrs
	Question    string      `json:"question"`         // sentence shown to the model in the describe round
	Label       string      `json:"label"`            // human-readable name for the settings panel
	Description string      `json:"description,omitempty"` // one line explaining what the attribute measures
	Consumers   []string    `json:"consumers,omitempty"`   // downstream steps that read this attribute
}

// ImageAttrRegistry is the canonical, ordered list of observed attributes for
// this release. Two are active:
//   - contain.text drives the OCR decision through its "block" value;
//   - contain.data_visual keeps charts (sparse axis labels) on the OCR path
//     while photos with sparse road signs stay off it, preserving the
//     equivalence with the old 7-class model.
var ImageAttrRegistry = []AttrSpec{
	{
		Name: "contain.text",
		Type: AttrTypeExtent,
		Values: []AttrValue{
			{Value: "none", Label: "None", Description: "no text at all"},
			{Value: "sparse", Label: "Sparse", Description: "a few words — a logo, a road sign, a single label"},
			{Value: "block", Label: "Block", Description: "a block of body text — a screenshot, a table, a document page"},
		},
		Question: "How much body text does the image carry? Answer exactly one of: " +
			"none (no text), sparse (a few words such as a logo or a road sign), " +
			"block (a block of body text such as a screenshot, table, or document page).",
		Label: "Text in the image",
		Description: "How much body text the picture itself carries. " +
			"Decides whether reading its text is worth a separate OCR pass.",
		Consumers: []string{"ocr"},
	},
	{
		Name: "contain.data_visual",
		Type: AttrTypePresence,
		Values: []AttrValue{
			{Value: "true", Label: "Yes", Description: "a chart, graph or diagram with plotted values"},
			{Value: "false", Label: "No", Description: "a photo, drawing, icon or decoration"},
		},
		Question: "Does the image present quantitative data as a chart, graph, diagram, or infographic " +
			"(axis labels, legends, plotted values)? Answer exactly one of: true, false.",
		Label: "Data visual",
		Description: "Whether the picture conveys data as a chart, graph, diagram or infographic. " +
			"Such images keep their labels on the OCR path even when the text looks sparse.",
		Consumers: []string{"ocr"},
	},
}

// ImageAttrSchemaVersion marks the shape and the semantics of
// ImageAttrs.Attrs. It is attrs/2 since an unobserved attribute stopped being
// written as a conservative default: an absent key now means "the observation
// did not answer for this attribute" and is read through the OCR policy's
// OnUnobserved clause, so a consumer of stored observations must not assume
// every registered key is present. Bump it when an attribute's semantics
// change, not when one is merely added.
const ImageAttrSchemaVersion = "attrs/2"

// ImageAttrSource records how an observation was produced, so post-split data
// can be bucketed by provenance and evaluations do not mix sources.
type ImageAttrSource struct {
	Schema string `json:"schema"`           // ImageAttrSchemaVersion
	Prompt string `json:"prompt"`           // prompt fingerprint (version + whether custom instructions were appended)
	Custom bool   `json:"custom,omitempty"` // true if the describe prompt carried user custom instructions
}

// ImageAttrs is the persisted observation result. Attrs is a map, not a fixed
// struct, so adding an attribute never breaks storage or rows written before
// the attribute existed. Stored inside chunks.image_info as JSON; no migration.
type ImageAttrs struct {
	Schema string          `json:"schema"`
	Source ImageAttrSource `json:"source"`
	Attrs  map[string]any  `json:"attrs,omitempty"`
}

// Observed reports whether an attribute key was present in the observation.
func (a ImageAttrs) Observed(name string) bool {
	if a.Attrs == nil {
		return false
	}
	_, ok := a.Attrs[name]
	return ok
}

// TextExtent returns the observed contain.text value, or "" when the attribute
// was not observed. Use Observed("contain.text") to tell an absent attribute
// apart from one the model answered; "" is never a legal observed value.
func (a ImageAttrs) TextExtent() string {
	if a.Attrs == nil {
		return ""
	}
	if v, ok := a.Attrs["contain.text"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// DataVisual returns the observed contain.data_visual value, or false when the
// attribute was not observed. Absence is not the same as an observed "false" —
// use Observed to distinguish them, which is what DecideOCR does.
func (a ImageAttrs) DataVisual() bool {
	if a.Attrs == nil {
		return false
	}
	if v, ok := a.Attrs["contain.data_visual"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// ImageActionsConfig is the attribute -> work policy. It is keyed by downstream
// action (today only "ocr"); each action lists the attribute conditions that
// turn it on, plus the conservative value to use when the driving attribute was
// not observed.
type ImageActionsConfig struct {
	OCR ImageOCRAction `json:"ocr"`
}

// ImageOCRAction is the OCR clause of the image_actions policy.
type ImageOCRAction struct {
	// On is the list of attribute conditions that enable OCR. Matching any one
	// turns OCR on.
	On []ImageAttrCondition `json:"on"`
	// OnUnobserved is the OCR decision used when the observation does not
	// answer for one of the attributes the policy reads — the attribute is
	// absent because the model skipped the line, or because the value it wrote
	// was not one of the allowed ones. The default is true: an incomplete
	// observation keeps extracting, so a missed block of text costs one extra
	// call instead of being lost. Turn it off only with a model reliable enough
	// to answer every attribute line.
	OnUnobserved bool `json:"on_unobserved"`
}

// ImageAttrCondition is one attribute match: attr Prop equals Is.
type ImageAttrCondition struct {
	Prop string `json:"prop"`
	Is   string `json:"is"`
}

// DefaultImageActions is the built-in attribute -> work table. It is deliberately
// conservative: OCR runs when contain.text is "block", or contain.data_visual is
// true, or the observation did not answer for an attribute the policy reads (the
// OnUnobserved clause). It is equivalent to the old class table: decorative /
// logo / photo (text reliably absent or captured by the description) skip OCR;
// text_screenshot / table / chart / other run OCR.
func DefaultImageActions() ImageActionsConfig {
	return ImageActionsConfig{
		OCR: ImageOCRAction{
			On: []ImageAttrCondition{
				{Prop: "contain.text", Is: "block"},
				{Prop: "contain.data_visual", Is: "true"},
			},
			OnUnobserved: true,
		},
	}
}

// MergeImageActions folds a knowledge base's custom action table on top of the
// built-in one, per action key. A custom OCR.On replaces the default OCR.On
// wholesale (the "on" list is a unit); anything the custom table does not
// mention keeps the default. This lets a knowledge base tune OCR without
// spelling out every condition, and keeps a future action key added to the
// default from being wiped by an older stored config.
func MergeImageActions(custom ImageActionsConfig) ImageActionsConfig {
	merged := DefaultImageActions()
	if len(custom.OCR.On) > 0 {
		merged.OCR = custom.OCR
	}
	return merged
}

// ResolveImageActions returns the effective policy: a nil config uses the
// default; a non-nil config is merged so forward-compatible.
func ResolveImageActions(p *ImageActionsConfig) ImageActionsConfig {
	if p == nil {
		return DefaultImageActions()
	}
	return MergeImageActions(*p)
}

// ImageAttrPromptVersion is the fingerprint of the describe-round observation
// prompt. It is recorded in ImageAttrSource.Prompt so evaluations can tell
// observations produced by different prompt versions apart.
const ImageAttrPromptVersion = "observe/1"

// AttrObservation is a parsed observe-and-describe answer: the observed
// attributes and the description written for the image.
type AttrObservation struct {
	Attrs       ImageAttrs
	Description string
	// Observed reports whether the model returned at least one well-formed
	// attribute line with a usable value. When false no attribute was observed
	// at all and the observation round effectively produced nothing — the
	// caller mirrors this into the trace (observation_failed) so a prompt that
	// derailed the attribute protocol is visible instead of passing for a
	// confident read. It is a runtime signal only and is never persisted: only
	// Attrs is stored with the chunk.
	Observed bool
}

// ParseImageAttrsResponse splits an observe-and-describe answer into its
// attribute lines and its DESCRIPTION. The boolean reports whether the answer
// yielded a description worth storing; false means the image ends up with
// attributes but no caption, which the caller records so the missing caption is
// not read as an observation failure.
//
// Only a reply with no text at all, or one that is nothing but attribute lines,
// yields false. Prose that skips the labels is kept as the description: the
// model answered the question, it just did not format the answer.
//
// An attribute the answer does not spell out — because the model skipped the
// line, or because the value it wrote is not one of the allowed ones — is
// simply absent from Attrs. Nothing is invented in its place: writing a
// conservative default there would make an unanswered question look like a
// confident "block", and would leave the OCR policy's OnUnobserved clause
// unreachable. The absence is what DecideOCR turns into the conservative
// decision instead.
func ParseImageAttrsResponse(raw string) (AttrObservation, bool) {
	obs := AttrObservation{
		Attrs: ImageAttrs{
			Schema: ImageAttrSchemaVersion,
			Source: ImageAttrSource{Schema: ImageAttrSchemaVersion, Prompt: ImageAttrPromptVersion},
			Attrs:  map[string]any{},
		},
	}
	seen := map[string]bool{}
	var descriptions []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := trimAttrMarkers(line)
		if key, val, ok := cutAttrLine(trimmed); ok {
			if spec, found := lookupAttrSpec(key); found {
				// Unusable values are dropped rather than defaulted, so the
				// attribute stays absent and the policy's OnUnobserved clause
				// decides what to do about it.
				if v, usable := coerceAttrValue(spec, val); usable {
					obs.Attrs.Attrs[spec.Name] = v
					seen[spec.Name] = true
				}
				continue
			}
			if strings.EqualFold(key, "DESCRIPTION") {
				descriptions = append(descriptions, val)
				continue
			}
		}
		if strings.EqualFold(trimmed, "DESCRIPTION") {
			continue
		}
		if trimmed != "" {
			descriptions = append(descriptions, trimmed)
		}
	}
	// Observed records whether the model emitted any well-formed attribute
	// line. A prose-only or empty answer yields false; such replies are kept as
	// the description but carry no observation, so the caller must know the
	// attribute table says nothing about the image.
	obs.Observed = len(seen) > 0
	joined := strings.TrimSpace(strings.Join(descriptions, " "))
	// A model may prefix the description with a list marker; drop it so the
	// caption starts with prose.
	obs.Description = strings.TrimSpace(strings.TrimLeft(joined, "-*# "))
	return obs, obs.Description != ""
}

// lookupAttrSpec matches a model-written key (e.g. "CONTAIN.TEXT",
// "contain_text") onto a registry attribute.
func lookupAttrSpec(key string) (AttrSpec, bool) {
	norm := strings.ToLower(strings.TrimSpace(key))
	norm = strings.ReplaceAll(norm, " ", "_")
	norm = strings.ReplaceAll(norm, "-", "_")
	// Models wrap keys in markdown emphasis ("**contain.text**"); strip it so
	// the trailing markers never break the match.
	norm = strings.ReplaceAll(norm, "*", "")
	norm = strings.ReplaceAll(norm, "#", "")
	for _, spec := range ImageAttrRegistry {
		if spec.Name == norm || strings.ReplaceAll(spec.Name, ".", "_") == norm {
			return spec, true
		}
	}
	return AttrSpec{}, false
}

// coerceAttrValue validates a raw value against the attribute's allowed set. It
// reports false when the model wrote nothing usable — an empty value, or one
// outside the allowed set — and the caller then leaves the attribute absent
// instead of substituting a default.
func coerceAttrValue(spec AttrSpec, raw string) (any, bool) {
	v := strings.TrimSpace(strings.ToLower(raw))
	// Models may emphasise the value ("**block**"); strip it before matching.
	v = strings.ReplaceAll(v, "*", "")
	v = strings.ReplaceAll(v, "#", "")
	switch spec.Type {
	case AttrTypeExtent:
		for _, allowed := range spec.Values {
			if v == allowed.Value {
				return allowed.Value, true
			}
		}
	case AttrTypePresence:
		switch v {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return nil, false
}

// cutAttrLine returns the key and value of a "KEY: value" line.
func cutAttrLine(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

// trimAttrMarkers strips the markdown emphasis and list markers models wrap
// around attribute lines.
func trimAttrMarkers(line string) string {
	return strings.TrimLeft(line, "*_#- \t")
}
