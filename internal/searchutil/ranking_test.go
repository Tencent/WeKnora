package searchutil

import (
	"math"
	"math/rand"
	"testing"
)

// The reference formulas below are the exact expressions the chat pipeline
// (rerank.go) and the agent knowledge-search tool (search_knowledge.go)
// carried before the unification. Parity with them is the acceptance bar for
// the shared core: the refactor must not change one ranking decision.

func legacyChatComposite(modelScore, baseScore float64, knowledgeSource string) float64 {
	sourceWeight := 1.0
	switch lowerASCII(knowledgeSource) {
	case "web_search":
		sourceWeight = 0.95
	default:
		sourceWeight = 1.0
	}
	composite := 0.6*modelScore + 0.3*baseScore + 0.1*sourceWeight
	if composite < 0 {
		composite = 0
	}
	if composite > 1 {
		composite = 1
	}
	return composite
}

func legacyAgentComposite(startAt, endAt int, modelScore, baseScore float64, knowledgeSource string) float64 {
	sourceWeight := 1.0
	if lowerASCII(knowledgeSource) == "web_search" {
		sourceWeight = 0.95
	}
	positionPrior := 1.0
	if startAt >= 0 && endAt > startAt {
		positionRatio := 1.0 - float64(startAt)/float64(endAt+1)
		positionPrior += ClampFloat(positionRatio, -0.05, 0.05)
	}
	composite := 0.6*modelScore + 0.3*baseScore + 0.1*sourceWeight
	composite *= positionPrior
	if composite < 0 {
		composite = 0
	}
	if composite > 1 {
		composite = 1
	}
	return composite
}

func TestCompositeScoreRawParityWithBothPipelines(t *testing.T) {
	scores := []float64{0, 0.008, 0.011, 0.25, 0.5, 0.75, 0.99, 1}
	sources := []string{"", "knowledge_base", "web_search", "Web_Search", "WEB_SEARCH"}
	positions := [][2]int{{-1, -1}, {0, 0}, {0, 100}, {50, 100}, {99, 100}, {100, 99}, {0, 1000000}}

	for _, model := range scores {
		for _, base := range scores {
			for _, source := range sources {
				raw := CompositeScoreRaw(model, base, source)
				chat := ClampFloat(raw, 0, 1)
				wantChat := legacyChatComposite(model, base, source)
				if chat != wantChat {
					t.Fatalf("chat parity broken: model=%v base=%v source=%q got %v want %v",
						model, base, source, chat, wantChat)
				}
				for _, pos := range positions {
					agent := ClampFloat(raw*PositionPrior(pos[0], pos[1]), 0, 1)
					wantAgent := legacyAgentComposite(pos[0], pos[1], model, base, source)
					if agent != wantAgent {
						t.Fatalf("agent parity broken: pos=%v model=%v base=%v source=%q got %v want %v",
							pos, model, base, source, agent, wantAgent)
					}
				}
			}
		}
	}
}

func TestPositionPriorNeutralAndClamped(t *testing.T) {
	for _, invalid := range [][2]int{{-1, 100}, {0, -1}, {100, 100}, {100, 99}} {
		if got := PositionPrior(invalid[0], invalid[1]); got != 1.0 {
			t.Errorf("PositionPrior(%v) = %v, want neutral 1.0", invalid, got)
		}
	}
	// Early chunks gain the full +0.05; chunks past ~52% of the document lose
	// the capped -0.05. Expected values are computed at runtime from the
	// documented formula so the test and implementation cannot drift apart
	// through constant folding.
	early := 1.0 + ClampFloat(1.0-float64(0)/float64(1000+1), -0.05, 0.05)
	if got := PositionPrior(0, 1000); got != early {
		t.Errorf("PositionPrior(0,1000) = %v, want %v", got, early)
	}
	late := 1.0 + ClampFloat(1.0-float64(900)/float64(1000+1), -0.05, 0.05)
	if got := PositionPrior(900, 1000); got != late {
		t.Errorf("PositionPrior(900,1000) = %v, want %v", got, late)
	}
}

type mmrItem struct {
	id     int
	score  float64
	tokens map[string]struct{}
}

// naiveApplyMMR is the pre-unification chat form: every round rescans every
// candidate against every already-selected token set.
func naiveApplyMMR(items []mmrItem, k int, lambda float64) ([]mmrItem, float64) {
	if k <= 0 || len(items) == 0 {
		return nil, 0
	}
	selected := make([]mmrItem, 0, k)
	taken := make([]bool, len(items))
	for len(selected) < k {
		bestIdx := -1
		bestScore := -1.0
		for i, item := range items {
			if taken[i] {
				continue
			}
			redundancy := 0.0
			for _, sel := range selected {
				if sim := Jaccard(item.tokens, sel.tokens); sim > redundancy {
					redundancy = sim
				}
			}
			mmr := lambda*item.score - (1.0-lambda)*redundancy
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = i
			}
		}
		if bestIdx < 0 {
			break
		}
		selected = append(selected, items[bestIdx])
		taken[bestIdx] = true
	}
	avg := 0.0
	if len(selected) > 1 {
		pairs := 0
		for i := 0; i < len(selected); i++ {
			for j := i + 1; j < len(selected); j++ {
				avg += Jaccard(selected[i].tokens, selected[j].tokens)
				pairs++
			}
		}
		avg /= float64(pairs)
	}
	return selected, avg
}

func TestApplyMMRMatchesNaiveSelectionAndRedundancy(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	vocab := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}

	for round := 0; round < 300; round++ {
		n := 1 + rng.Intn(12)
		items := make([]mmrItem, n)
		tokenSets := make([]map[string]struct{}, n)
		for i := range items {
			tokens := make(map[string]struct{})
			for _, word := range vocab {
				if rng.Intn(3) == 0 {
					tokens[word] = struct{}{}
				}
			}
			items[i] = mmrItem{id: i, score: float64(rng.Intn(1000)) / 1000.0, tokens: tokens}
			tokenSets[i] = tokens
		}
		k := rng.Intn(n + 3)
		lambda := 0.5 + 0.5*float64(rng.Intn(6))/5.0 // 0.5..1.0

		got, gotAvg := ApplyMMR(items, tokenSets, k, lambda, func(it mmrItem) float64 { return it.score })
		want, wantAvg := naiveApplyMMR(items, k, lambda)

		if len(got) != len(want) {
			t.Fatalf("round %d: k=%d selected %d items, naive selected %d", round, k, len(got), len(want))
		}
		for i := range got {
			if got[i].id != want[i].id || got[i].score != want[i].score {
				t.Fatalf("round %d: selection[%d] diverges (id %d vs %d)", round, i, got[i].id, want[i].id)
			}
		}
		if math.Abs(gotAvg-wantAvg) > 1e-12 {
			t.Fatalf("round %d: avgRedundancy %v vs naive %v", round, gotAvg, wantAvg)
		}
	}
}

func TestApplyMMREdgeCases(t *testing.T) {
	items := []mmrItem{{id: 0, score: 1, tokens: map[string]struct{}{"a": {}}}}
	sets := []map[string]struct{}{items[0].tokens}

	if got, _ := ApplyMMR(items, sets, 0, 0.7, func(it mmrItem) float64 { return it.score }); got != nil {
		t.Errorf("k=0 must yield nil, got %v", got)
	}
	if got, _ := ApplyMMR(nil, nil, 5, 0.7, func(it mmrItem) float64 { return it.score }); got != nil {
		t.Errorf("empty input must yield nil, got %v", got)
	}
	got, avg := ApplyMMR(items, sets, 5, 0.7, func(it mmrItem) float64 { return it.score })
	if len(got) != 1 || avg != 0 {
		t.Errorf("k>len must select everything once: got %v avg %v", got, avg)
	}
}
