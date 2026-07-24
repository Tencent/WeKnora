package types

import "testing"

func TestResolveRetrievalPlanMatrix(t *testing.T) {
	tests := []struct {
		name    string
		u       QueryUnderstanding
		hasKB   bool
		hasWeb  bool
		webMode WebSearchMode
		want    RetrievalPlanMode
		reason  string
	}{
		{"conversation summary", QueryUnderstanding{RetrievalNeed: RetrievalNeedNone}, true, true, WebSearchModeAlways, RetrievalPlanNone, RetrievalReasonNoRetrieval},
		{"explicit kb stays kb only", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementKB}, true, true, WebSearchModeAlways, RetrievalPlanKBOnly, RetrievalReasonExplicitKB},
		{"explicit web stays web only", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementWeb}, true, true, WebSearchModeOnDemand, RetrievalPlanWebOnly, RetrievalReasonExplicitWeb},
		{"explicit both", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementBoth}, true, true, WebSearchModeOnDemand, RetrievalPlanParallel, RetrievalReasonExplicitBoth},
		{"fresh auto uses web", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementAuto, Freshness: FreshnessCurrent}, true, true, WebSearchModeOnDemand, RetrievalPlanWebOnly, RetrievalReasonFreshness},
		{"generic on demand", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementAuto, Freshness: FreshnessAny}, true, true, WebSearchModeOnDemand, RetrievalPlanKBThenWeb, RetrievalReasonKBThenWeb},
		{"generic always combines", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementAuto, Freshness: FreshnessAny}, true, true, WebSearchModeAlways, RetrievalPlanParallel, RetrievalReasonAlwaysCombine},
		{"explicit web unavailable does not substitute kb", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementWeb}, true, false, WebSearchModeOff, RetrievalPlanNone, RetrievalReasonWebUnavailable},
		{"web policy off overrides provider availability", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementWeb}, true, true, WebSearchModeOff, RetrievalPlanNone, RetrievalReasonWebUnavailable},
		{"explicit kb unavailable does not substitute web", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementKB}, false, true, WebSearchModeOnDemand, RetrievalPlanNone, RetrievalReasonKBUnavailable},
		{"both requires knowledge base", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementBoth}, false, true, WebSearchModeOnDemand, RetrievalPlanNone, RetrievalReasonKBUnavailable},
		{"both requires web", QueryUnderstanding{RetrievalNeed: RetrievalNeedRequired, SourceRequirement: SourceRequirementBoth}, true, false, WebSearchModeOnDemand, RetrievalPlanNone, RetrievalReasonWebUnavailable},
		{"only kb available", DefaultQueryUnderstanding(), true, false, WebSearchModeOff, RetrievalPlanKBOnly, RetrievalReasonDefaultKB},
		{"only web available", DefaultQueryUnderstanding(), false, true, WebSearchModeOnDemand, RetrievalPlanWebOnly, RetrievalReasonDefaultWeb},
		{"no source available", DefaultQueryUnderstanding(), false, false, WebSearchModeOff, RetrievalPlanNone, RetrievalReasonSourcesUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveRetrievalPlan(tt.u, tt.hasKB, tt.hasWeb, tt.webMode)
			if got.Mode != tt.want || got.ReasonCode != tt.reason {
				t.Fatalf("ResolveRetrievalPlan() = (%s, %s), want (%s, %s)", got.Mode, got.ReasonCode, tt.want, tt.reason)
			}
		})
	}
}

func TestEffectiveWebSearchModeMigratesLegacyBoolean(t *testing.T) {
	if got := (CustomAgentConfig{WebSearchEnabled: true}).EffectiveWebSearchMode(); got != WebSearchModeOnDemand {
		t.Fatalf("legacy enabled mode = %s, want %s", got, WebSearchModeOnDemand)
	}
	if got := (CustomAgentConfig{WebSearchMode: WebSearchModeAlways}).EffectiveWebSearchMode(); got != WebSearchModeAlways {
		t.Fatalf("explicit mode = %s, want %s", got, WebSearchModeAlways)
	}
	agent := &CustomAgent{Config: CustomAgentConfig{WebSearchMode: "invalid"}}
	agent.EnsureDefaults()
	if agent.Config.WebSearchMode != WebSearchModeOff || agent.Config.WebSearchEnabled {
		t.Fatalf("invalid mode normalized to (%s, %v), want (off, false)", agent.Config.WebSearchMode, agent.Config.WebSearchEnabled)
	}
}

func TestCustomAgentEnsureDefaultsAcceptsNilReceiver(t *testing.T) {
	var agent *CustomAgent
	agent.EnsureDefaults()
}
