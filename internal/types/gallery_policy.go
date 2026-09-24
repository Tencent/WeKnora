package types

// The gallery configuration is resolved from up to four tiers, each more
// specific than the last. Higher tiers override lower ones field by field;
// a tier that does not mention an attribute (or source, or dimension)
// changes nothing about it.
//
//	user tier   (per-user JSON, users.preferences.gallery)      highest
//	KB tier     (per-KB JSON, knowledge base config)             pending
//	system tier (platform JSON, system_settings "gallery.policy")
//	source tier (what each registered attribute source declares) lowest
//
// The three JSON tiers share one schema (GalleryPolicyTier) so the merge
// engine below is tier-agnostic and new storage homes plug in without
// changing the contract. Only the user tier carries mode/status — the
// search-field activation state is personal by definition.
//
// Only attribute USAGE (eligibility) is configurable here; attribute
// definitions (name / type / values / labels) come exclusively from
// registered sources, so no tier can invent an attribute no pipeline
// produces.

// GalleryUsageOverride is a field-level usage override. A nil field means
// "leave the lower tier's value alone"; a non-nil field replaces it.
type GalleryUsageOverride struct {
	InFilter      *bool `json:"in_filter,omitempty"`
	InSearchField *bool `json:"in_searchfield,omitempty"`
	InSortField   *bool `json:"in_sortfield,omitempty"`
}

// GalleryPolicyTier is the unified JSON schema shared by the system, KB and
// user configuration tiers. All fields are optional; an empty tier is a
// no-op.
type GalleryPolicyTier struct {
	// Sources toggles whole attribute sources on/off, keyed by source id.
	// The highest tier that mentions a source decides its state; sources
	// nobody mentions are enabled.
	Sources map[string]bool `json:"sources,omitempty"`
	// Overrides adjusts per-attribute usage, keyed by namespaced attribute
	// id ("system:contain.text"). Overrides apply field by field on top of
	// whatever the lower tiers resolved.
	Overrides map[string]GalleryUsageOverride `json:"overrides,omitempty"`
	// Mode is the user-tier search activation mode: "all" (every eligible
	// search field active) or "custom" (per-field status governs). Empty
	// means "all". Only meaningful in the user tier.
	Mode string `json:"mode,omitempty"`
	// Status is the user-tier per-attribute search-field activation,
	// keyed by namespaced attribute id with values "on"/"off". Only
	// meaningful in the user tier.
	Status map[string]string `json:"status,omitempty"`
}

// Gallery resolved-config constants.
const (
	// GalleryModeAll activates every search-eligible field regardless of
	// status; it is the default so a fresh user's search works out of the
	// box. GalleryModeCustom lets the per-field status map decide, with an
	// unrecorded field defaulting to off.
	GalleryModeAll    = "all"
	GalleryModeCustom = "custom"
	// GalleryStatusOn / GalleryStatusOff are the user-tier status values.
	GalleryStatusOn  = "on"
	GalleryStatusOff = "off"
)

// GalleryResolvedAttr is one attribute as the gallery contract serves it to
// the frontend: the source's declaration with every tier's overrides already
// merged in. ID is the namespaced attribute id; Name stays source-local for
// raw lookups in image_info.
type GalleryResolvedAttr struct {
	ID          string            `json:"id"`
	Source      string            `json:"source"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Values      []string          `json:"values,omitempty"`
	Label       string            `json:"label"`
	Description string            `json:"description,omitempty"`
	ValueLabels map[string]string `json:"value_labels,omitempty"`
	Usage       GalleryUsage      `json:"usage"`
	// UsageFrom names the highest tier that touched this attribute's usage:
	// "source" | "system" | "kb" | "user". Debugging aid for "why does this
	// attribute (not) show up".
	UsageFrom string `json:"usage_from"`
}

// GalleryResolvedConfig is the merged gallery contract: which sources are
// live, the resolved attribute list, and the user's search activation state.
type GalleryResolvedConfig struct {
	// AttributeSources lists the live source ids in priority order.
	AttributeSources []string `json:"attribute_sources"`
	Attributes       []GalleryResolvedAttr `json:"attributes"`
	// Mode is the user's search activation mode ("all" by default).
	Mode string `json:"mode"`
	// Status mirrors the user's per-field search toggles; nil means the
	// user has not recorded any yet.
	Status map[string]string `json:"status"`
}

// galleryUsageFrom labels for UsageFrom, ordered low -> high.
const (
	galleryFromSource = "source"
	galleryFromSystem = "system"
	galleryFromKB     = "kb"
	galleryFromUser   = "user"
)

// ResolveGalleryConfig merges the registered sources with the configuration
// tiers (system -> KB -> user, each overriding field by field) and returns
// the contract the gallery frontend renders from. A nil tier simply does not
// exist; the KB tier is reserved for the per-KB config (not wired yet — the
// engine already accepts it).
func ResolveGalleryConfig(kbID string, system, kb, user *GalleryPolicyTier) *GalleryResolvedConfig {
	res := &GalleryResolvedConfig{
		AttributeSources: []string{},
		Attributes:       []GalleryResolvedAttr{},
		Mode:             GalleryModeAll,
	}

	// Source on/off: the highest tier that mentions the source wins.
	for _, src := range GalleryAttrSources(kbID) {
		if !gallerySourceEnabled(src.ID, system, kb, user) {
			continue
		}
		res.AttributeSources = append(res.AttributeSources, src.ID)
		for _, def := range src.Attrs {
			attr := GalleryResolvedAttr{
				ID:          GalleryAttrID(src.ID, def.Name),
				Source:      src.ID,
				Name:        def.Name,
				Type:        def.Type,
				Values:      def.Values,
				Label:       def.Label,
				Description: def.Description,
				ValueLabels: def.ValueLabels,
				Usage:       def.Usage,
				UsageFrom:   galleryFromSource,
			}
			galleryApplyTier(&attr, system, galleryFromSystem)
			galleryApplyTier(&attr, kb, galleryFromKB)
			galleryApplyTier(&attr, user, galleryFromUser)
			res.Attributes = append(res.Attributes, attr)
		}
	}

	// User-tier activation state. Mode is validated; anything else falls
	// back to the inclusive default.
	if user != nil {
		if user.Mode == GalleryModeAll || user.Mode == GalleryModeCustom {
			res.Mode = user.Mode
		}
		if len(user.Status) > 0 {
			status := make(map[string]string, len(user.Status))
			for id, v := range user.Status {
				if v == GalleryStatusOn || v == GalleryStatusOff {
					status[id] = v
				}
			}
			if len(status) > 0 {
				res.Status = status
			}
		}
	}
	return res
}

// gallerySourceEnabled reports whether a source is enabled under the tiers:
// user > KB > system, and an unmentioned source defaults to enabled.
func gallerySourceEnabled(id string, tiers ...*GalleryPolicyTier) bool {
	for i := len(tiers) - 1; i >= 0; i-- {
		t := tiers[i]
		if t == nil {
			continue
		}
		if enabled, mentioned := t.Sources[id]; mentioned {
			return enabled
		}
	}
	return true
}

// galleryApplyTier folds one tier's override for this attribute into the
// resolved usage, field by field, and records the tier in UsageFrom when it
// touched at least one field.
func galleryApplyTier(attr *GalleryResolvedAttr, tier *GalleryPolicyTier, from string) {
	if tier == nil {
		return
	}
	if o, ok := tier.Overrides[attr.ID]; ok {
		applyGalleryOverride(&attr.Usage, &attr.UsageFrom, from, &o)
	}
}

// applyGalleryOverride applies one attribute override; reports whether the
// override existed.
func applyGalleryOverride(usage *GalleryUsage, usageFrom *string, from string, o *GalleryUsageOverride) bool {
	if o == nil {
		return false
	}
	if o.InFilter != nil {
		usage.InFilter = *o.InFilter
		*usageFrom = from
	}
	if o.InSearchField != nil {
		usage.InSearchField = *o.InSearchField
		*usageFrom = from
	}
	if o.InSortField != nil {
		usage.InSortField = *o.InSortField
		*usageFrom = from
	}
	return true
}
