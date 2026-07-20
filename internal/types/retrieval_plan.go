package types

const (
	RetrievalReasonNoRetrieval        = "semantic_no_retrieval"
	RetrievalReasonExplicitKB         = "explicit_knowledge_base"
	RetrievalReasonExplicitWeb        = "explicit_web"
	RetrievalReasonExplicitBoth       = "explicit_both"
	RetrievalReasonFreshness          = "freshness_requires_web"
	RetrievalReasonDefaultKB          = "default_knowledge_base"
	RetrievalReasonDefaultWeb         = "default_web"
	RetrievalReasonKBThenWeb          = "knowledge_then_web"
	RetrievalReasonAlwaysCombine      = "always_combine"
	RetrievalReasonWebUnavailable     = "web_required_but_unavailable"
	RetrievalReasonKBUnavailable      = "knowledge_base_required_but_unavailable"
	RetrievalReasonSourcesUnavailable = "required_sources_unavailable"
)

// ResolveRetrievalPlan combines semantic requirements with source capability
// and agent policy. It never silently substitutes an explicitly requested
// source with another source.
func ResolveRetrievalPlan(
	u QueryUnderstanding,
	hasKB bool,
	hasWeb bool,
	webMode WebSearchMode,
) RetrievalPlan {
	// Availability and policy are separate inputs; an off policy removes web
	// from the executable source set even if a provider exists.
	hasWeb = hasWeb && webMode != WebSearchModeOff

	if u.RetrievalNeed == RetrievalNeedNone {
		return RetrievalPlan{Mode: RetrievalPlanNone, ReasonCode: RetrievalReasonNoRetrieval}
	}

	switch u.SourceRequirement {
	case SourceRequirementKB:
		if hasKB {
			return RetrievalPlan{Mode: RetrievalPlanKBOnly, ReasonCode: RetrievalReasonExplicitKB}
		}
		return RetrievalPlan{Mode: RetrievalPlanNone, ReasonCode: RetrievalReasonKBUnavailable}
	case SourceRequirementWeb:
		if hasWeb {
			return RetrievalPlan{Mode: RetrievalPlanWebOnly, ReasonCode: RetrievalReasonExplicitWeb}
		}
		return RetrievalPlan{Mode: RetrievalPlanNone, ReasonCode: RetrievalReasonWebUnavailable}
	case SourceRequirementBoth:
		switch {
		case hasKB && hasWeb:
			return RetrievalPlan{Mode: RetrievalPlanParallel, ReasonCode: RetrievalReasonExplicitBoth}
		case !hasKB && !hasWeb:
			return RetrievalPlan{Mode: RetrievalPlanNone, ReasonCode: RetrievalReasonSourcesUnavailable}
		case !hasKB:
			return RetrievalPlan{Mode: RetrievalPlanNone, ReasonCode: RetrievalReasonKBUnavailable}
		default:
			return RetrievalPlan{Mode: RetrievalPlanNone, ReasonCode: RetrievalReasonWebUnavailable}
		}
	}

	if u.Freshness == FreshnessCurrent {
		if hasWeb {
			return RetrievalPlan{Mode: RetrievalPlanWebOnly, ReasonCode: RetrievalReasonFreshness}
		}
		return RetrievalPlan{Mode: RetrievalPlanNone, ReasonCode: RetrievalReasonWebUnavailable}
	}

	switch {
	case hasKB && hasWeb && webMode == WebSearchModeAlways:
		return RetrievalPlan{Mode: RetrievalPlanParallel, ReasonCode: RetrievalReasonAlwaysCombine}
	case hasKB && hasWeb:
		return RetrievalPlan{Mode: RetrievalPlanKBThenWeb, ReasonCode: RetrievalReasonKBThenWeb}
	case hasKB:
		return RetrievalPlan{Mode: RetrievalPlanKBOnly, ReasonCode: RetrievalReasonDefaultKB}
	case hasWeb:
		return RetrievalPlan{Mode: RetrievalPlanWebOnly, ReasonCode: RetrievalReasonDefaultWeb}
	default:
		return RetrievalPlan{Mode: RetrievalPlanNone, ReasonCode: RetrievalReasonSourcesUnavailable}
	}
}

func DefaultQueryUnderstanding() QueryUnderstanding {
	return QueryUnderstanding{
		ResponseMode:      ResponseModeAnswer,
		RetrievalNeed:     RetrievalNeedRequired,
		SourceRequirement: SourceRequirementAuto,
		Freshness:         FreshnessAny,
	}
}
