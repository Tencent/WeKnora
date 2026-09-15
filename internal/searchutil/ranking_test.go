package searchutil

import (
	"math/rand"
	"testing"
)

func TestCompositeScore(t *testing.T) {
	// Expectations use the same runtime float64 arithmetic as the
	// implementation (weights times float64 variables), so the test pins the
	// exact values both call sites produced before the extraction.
	var m, b float64 = 0.5, 0.5
	tests := []struct {
		name            string
		knowledgeSource string
		modelScore      float64
		baseScore       float64
		want            float64
	}{
		{"even scores default source", "knowledge_base", m, b, CompositeModelWeight*m + CompositeBaseWeight*b + CompositeSourceWeight*1.0},
		{"web_search down-weighted", "web_search", m, b, CompositeModelWeight*m + CompositeBaseWeight*b + CompositeSourceWeight*webSearchSourceWeight},
		{"source match is case-insensitive", "WEB_SEARCH", m, b, CompositeModelWeight*m + CompositeBaseWeight*b + CompositeSourceWeight*webSearchSourceWeight},
		{"clamped low", "knowledge_base", -2, -2, 0},
		{"clamped high", "knowledge_base", 2, 1, 1},
		{"zero scores keep source floor", "", 0, 0, 0.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompositeScore(tt.knowledgeSource, tt.modelScore, tt.baseScore); got != tt.want {
				t.Fatalf("CompositeScore(%q, %v, %v) = %v, want %v", tt.knowledgeSource, tt.modelScore, tt.baseScore, got, tt.want)
			}
		})
	}
}

func TestCompositeRawScoreNotClamped(t *testing.T) {
	// The agent path multiplies in the position prior before clamping, so the
	// raw variant must preserve out-of-range magnitudes instead of clamping.
	// Operands stay variables so the expectation uses runtime float64
	// arithmetic, exactly like the implementation.
	var modelScore, baseScore float64 = 2, 2
	got := CompositeRawScore("web_search", modelScore, baseScore)
	want := CompositeModelWeight*modelScore + CompositeBaseWeight*baseScore + CompositeSourceWeight*webSearchSourceWeight
	if got != want {
		t.Fatalf("CompositeRawScore = %v, want %v", got, want)
	}
}

func TestPositionPrior(t *testing.T) {
	tests := []struct {
		name           string
		startAt, endAt int
		want           float64
	}{
		{"negative start is neutral", -1, 10, 1.0},
		{"empty span is neutral", 5, 5, 1.0},
		{"inverted span is neutral", 10, 5, 1.0},
		{"document head gets the full boost", 0, 9, 1.05},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PositionPrior(tt.startAt, tt.endAt); got != tt.want {
				t.Fatalf("PositionPrior(%d, %d) = %v, want %v", tt.startAt, tt.endAt, got, tt.want)
			}
		})
	}
	t.Run("mid-document ratio", func(t *testing.T) {
		want := 1.0 + ClampFloat(1.0-9.0/11.0, -0.05, 0.05)
		if got := PositionPrior(9, 10); got != want {
			t.Fatalf("PositionPrior(9, 10) = %v, want %v", got, want)
		}
	})
}

type mmrItem struct {
	id    int
	score float64
}

func tokens(words ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[w] = struct{}{}
	}
	return set
}

func TestApplyMMRGoldenSelection(t *testing.T) {
	items := []mmrItem{
		{1, 0.9}, {2, 0.8}, {3, 0.7},
	}
	tokenSets := []map[string]struct{}{
		tokens("a", "b"), tokens("a", "b"), tokens("c", "d"),
	}
	selected, avgRed := ApplyMMR(items, func(i mmrItem) float64 { return i.score }, tokenSets, 3, 0.7)

	var order []int
	for _, s := range selected {
		order = append(order, s.id)
	}
	// Item 2 is near-duplicate of item 1 (Jaccard 1.0), so it must fall behind
	// the less relevant but diverse item 3.
	wantOrder := []int{1, 3, 2}
	if len(order) != len(wantOrder) {
		t.Fatalf("selected %d items, want %d", len(order), len(wantOrder))
	}
	for i := range wantOrder {
		if order[i] != wantOrder[i] {
			t.Fatalf("selection order = %v, want %v", order, wantOrder)
		}
	}
	wantAvg := (Jaccard(tokenSets[0], tokenSets[2]) + Jaccard(tokenSets[0], tokenSets[1]) + Jaccard(tokenSets[2], tokenSets[1])) / 3
	if avgRed != wantAvg {
		t.Fatalf("avgRedundancy = %v, want %v", avgRed, wantAvg)
	}
}

func TestApplyMMREdgeCases(t *testing.T) {
	items := []mmrItem{{1, 0.9}}
	sets := []map[string]struct{}{tokens("a")}

	if selected, avg := ApplyMMR(items, func(i mmrItem) float64 { return i.score }, sets, 0, 0.7); selected != nil || avg != 0 {
		t.Fatalf("k<=0: got (%v, %v), want (nil, 0)", selected, avg)
	}
	if selected, avg := ApplyMMR(nil, func(i mmrItem) float64 { return i.score }, nil, 3, 0.7); selected != nil || avg != 0 {
		t.Fatalf("empty items: got (%v, %v), want (nil, 0)", selected, avg)
	}
	// k larger than the candidate pool selects everything.
	selected, avg := ApplyMMR(items, func(i mmrItem) float64 { return i.score }, sets, 10, 0.7)
	if len(selected) != 1 || selected[0].id != 1 || avg != 0 {
		t.Fatalf("k>len: got (%v, %v)", selected, avg)
	}
}

// naiveApplyMMR is the rescan-every-round form the chat pipeline used before
// the shared helper: for each remaining candidate it recomputes the maximum
// Jaccard against every selected item. It is the behavioral reference the
// incremental cache in ApplyMMR must match, including tie-breaking.
func naiveApplyMMR(items []mmrItem, tokenSets []map[string]struct{}, k int, lambda float64) ([]mmrItem, float64) {
	if k <= 0 || len(items) == 0 {
		return nil, 0
	}
	selected := make([]mmrItem, 0, k)
	selectedTokenSets := make([]map[string]struct{}, 0, k)
	selectedIndices := make(map[int]struct{})

	for len(selected) < k && len(selectedIndices) < len(items) {
		bestIdx := -1
		bestScore := -1.0
		for i, item := range items {
			if _, isSelected := selectedIndices[i]; isSelected {
				continue
			}
			redundancy := 0.0
			for _, selTokens := range selectedTokenSets {
				if sim := Jaccard(tokenSets[i], selTokens); sim > redundancy {
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
		selectedTokenSets = append(selectedTokenSets, tokenSets[bestIdx])
		selectedIndices[bestIdx] = struct{}{}
	}

	avgRed := 0.0
	if len(selectedTokenSets) > 1 {
		pairs := 0
		for i := 0; i < len(selectedTokenSets); i++ {
			for j := i + 1; j < len(selectedTokenSets); j++ {
				avgRed += Jaccard(selectedTokenSets[i], selectedTokenSets[j])
				pairs++
			}
		}
		if pairs > 0 {
			avgRed /= float64(pairs)
		}
	}
	return selected, avgRed
}

// TestApplyMMRMatchesNaiveSelection pins the consolidation's core claim: the
// incremental maxRedundancy cache picks exactly what the naive rescan picked,
// so moving the chat pipeline onto the shared implementation changes nothing.
func TestApplyMMRMatchesNaiveSelection(t *testing.T) {
	rng := rand.New(rand.NewSource(20260909))
	vocab := []string{"go", "rust", "kb", "rag", "chunk", "score", "mmr", "rank", "query", "doc", "span", "token"}

	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(12)
		items := make([]mmrItem, n)
		tokenSets := make([]map[string]struct{}, n)
		for i := 0; i < n; i++ {
			items[i] = mmrItem{id: i, score: float64(rng.Intn(101)) / 100.0}
			set := map[string]struct{}{}
			for len(set) == 0 {
				for _, w := range vocab {
					if rng.Intn(3) == 0 {
						set[w] = struct{}{}
					}
				}
			}
			tokenSets[i] = set
		}
		k := 1 + rng.Intn(n)
		lambda := []float64{0.3, 0.7, 0.9}[rng.Intn(3)]

		got, gotAvg := ApplyMMR(items, func(i mmrItem) float64 { return i.score }, tokenSets, k, lambda)
		want, wantAvg := naiveApplyMMR(items, tokenSets, k, lambda)

		if len(got) != len(want) {
			t.Fatalf("iter %d n=%d k=%d lambda=%v: selected %d items, naive selected %d", iter, n, k, lambda, len(got), len(want))
		}
		for i := range got {
			if got[i].id != want[i].id {
				t.Fatalf("iter %d n=%d k=%d lambda=%v: order[%d] = item %d, naive picked item %d", iter, n, k, lambda, i, got[i].id, want[i].id)
			}
		}
		if gotAvg != wantAvg {
			t.Fatalf("iter %d n=%d k=%d lambda=%v: avgRedundancy = %v, naive = %v", iter, n, k, lambda, gotAvg, wantAvg)
		}
	}
}
