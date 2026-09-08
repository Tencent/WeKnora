package learning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Pin the raw bytes, including labels, before measuring any model outcomes.
// Changing this fixture requires reviewing its provenance and reported results.
const offlineEvalDataSHA256 = "8bc5d4d5fe393466ba5abd27f74f4c47301a887f88e8d6170d376b6cf3f43237"

type offlineEvalData struct {
	SchemaVersion    int                     `json:"schema_version"`
	ID               string                  `json:"id"`
	AlgorithmVersion string                  `json:"algorithm_version"`
	Provenance       string                  `json:"provenance"`
	LabelPolicy      string                  `json:"label_policy"`
	PredictionPolicy string                  `json:"prediction_policy"`
	Now              time.Time               `json:"now"`
	Topics           []offlineEvalTopic      `json:"topics"`
	States           []offlineEvalState      `json:"states"`
	RankingCases     []offlineEvalRanking    `json:"ranking_cases"`
	PredictionCases  []offlineEvalPrediction `json:"prediction_cases"`
}

type offlineEvalTopic struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Aliases  []string `json:"aliases"`
	PageType string   `json:"page_type"`
	Summary  string   `json:"summary"`
	Chunks   int      `json:"chunks"`
	Degree   int      `json:"degree"`
}

type offlineEvalMastery struct {
	PMastery           float64    `json:"p_mastery"`
	Attempts           int        `json:"attempts"`
	Correct            int        `json:"correct"`
	ConsecutiveCorrect int        `json:"consecutive_correct"`
	LastAssessedAt     *time.Time `json:"last_assessed_at"`
	NextReviewAt       *time.Time `json:"next_review_at"`
	ViewedAt           *time.Time `json:"viewed_at"`
	SourceStamp        string     `json:"source_stamp"`
}

type offlineEvalState struct {
	ID                 string                    `json:"id"`
	Why                string                    `json:"why"`
	CurrentSourceStamp string                    `json:"current_source_stamp"`
	Mastery            *offlineEvalMastery       `json:"mastery"`
	Familiar           bool                      `json:"familiar"`
	Expected           types.LearningMasteryView `json:"expected"`
}

type offlineEvalCandidate struct {
	Topic     string `json:"topic"`
	State     string `json:"state"`
	Related   bool   `json:"related"`
	Relevance int    `json:"relevance"`
	Why       string `json:"why"`
}

type offlineEvalRanking struct {
	ID         string                 `json:"id"`
	Goal       string                 `json:"goal"`
	Interests  []string               `json:"interests"`
	Candidates []offlineEvalCandidate `json:"candidates"`
}

type offlineEvalObservation struct {
	QuestionID string    `json:"question_id"`
	At         time.Time `json:"at"`
	Correct    *bool     `json:"correct"`
}

type offlineEvalPrediction struct {
	ID          string                   `json:"id"`
	Why         string                   `json:"why"`
	SourceStamp string                   `json:"source_stamp"`
	Train       []offlineEvalObservation `json:"train"`
	Heldout     []offlineEvalObservation `json:"heldout"`
}

// This entry point runs only deterministic, in-memory production functions.
// It does not instantiate repositories, queues, model clients, or credentials.
func TestLearningOfflineEvaluation(t *testing.T) {
	raw := offlineEvalRead(t, "testdata/offline_eval.json")
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	if hash != offlineEvalDataSHA256 {
		t.Fatalf("fixture changed: sha256=%s; review labels and update the evaluation report before repinning", hash)
	}
	var data offlineEvalData
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("expected one JSON document, got %v", err)
	}
	topics, states := offlineEvalValidateData(t, data)
	t.Logf("dataset=%s sha256=%s algorithm=%s go=%s model=none prompt=none",
		data.ID, hash, types.LearningAlgorithmVersion, runtime.Version())
	t.Logf("rank=rankRecommendations weights=0.45/0.25/0.20/0.10 kind_diversity=true reserved_exploration=true rank_sha256=%x bkt_sha256=%x",
		sha256.Sum256(offlineEvalRead(t, "recommend.go")),
		sha256.Sum256(offlineEvalRead(t, "../../../types/learning_algorithm.go")))
	t.Logf("synthetic_only=true ranking_cases=%d topics=%d state_cases=%d prediction_sequences=%d now=%s",
		len(data.RankingCases), len(data.Topics), len(data.States), len(data.PredictionCases), data.Now.Format(time.RFC3339))

	if !t.Run("golden_arithmetic", offlineEvalGoldenArithmetic) ||
		!t.Run("states_and_freshness", func(t *testing.T) { offlineEvalStates(t, data, states) }) ||
		!t.Run("fixture_guards", func(t *testing.T) { offlineEvalTemporalGuards(t, data) }) {
		return
	}
	t.Run("ranking", func(t *testing.T) {
		names := []string{"production", "degree_only", "cold_start"}
		totals := make([]offlineEvalRankingMetrics, len(names))
		candidateCount := 0
		for _, c := range data.RankingCases {
			t.Run(c.ID, func(t *testing.T) {
				var nodes []*types.LearningNode
				var views []*types.LearningNodeView
				labels := map[string]int{}
				for _, candidate := range c.Candidates {
					n := offlineEvalNode(topics[candidate.Topic], states[candidate.State])
					n.Related = candidate.Related
					nodes = append(nodes, n)
					views = append(views, types.LearningNodePublic(n, data.Now))
					labels[candidate.Topic] = candidate.Relevance
				}
				candidateCount += len(nodes)
				got := rankRecommendations(nodes, views, c.Interests, 5)
				if len(got) != 5 {
					t.Fatalf("got %d recommendations, want 5", len(got))
				}
				ids := make([]string, len(got))
				for i, r := range got {
					ids[i] = r.PageID
					if len(c.Interests) == 0 && r.Components.InterestMatch != 0 {
						t.Fatal("interest credit without an interest signal")
					}
				}
				for _, v := range views {
					if v.Mastery.State == "unseen" && got[4].Mastery.State != "unseen" {
						t.Fatal("the reserved exploration position is missing")
					}
				}
				slices.Reverse(nodes)
				slices.Reverse(views)
				if !reflect.DeepEqual(got, rankRecommendations(nodes, views, c.Interests, 5)) {
					t.Fatal("ranking changed after reversing input order")
				}
				rankings := [][]string{ids, offlineEvalBaseline(c, topics, true), offlineEvalBaseline(c, topics, false)}
				for i, ranked := range rankings {
					metrics := offlineEvalMeasureRanking(t, ranked, labels, 5)
					totals[i].NDCG += metrics.NDCG
					totals[i].Recall += metrics.Recall
					t.Logf("case=%s model=%s top5=%s NDCG@5=%.6f Recall@5=%.6f",
						c.ID, names[i], strings.Join(ranked, ","), metrics.NDCG, metrics.Recall)
				}
			})
		}
		if t.Failed() {
			t.Fatal("ranking checks failed; aggregate metrics withheld")
		}
		for i, total := range totals {
			n := float64(len(data.RankingCases))
			t.Logf("ranking_macro model=%s cases=%d candidate_occurrences=%d NDCG@5=%.6f Recall@5=%.6f",
				names[i], len(data.RankingCases), candidateCount, total.NDCG/n, total.Recall/n)
		}
	})
	t.Run("heldout_prediction", func(t *testing.T) {
		names := []string{"bkt_response", "fixed_chance_0.25", "fixed_prior_response_0.38"}
		totals := make([]offlineEvalLoss, len(names))
		trainCount, correctCount := 0, 0
		for _, c := range data.PredictionCases {
			t.Run(c.ID, func(t *testing.T) {
				// The predictor receives no heldout labels, timestamps, or outcomes.
				q := offlineEvalTrain(c.Train, c.SourceStamp)
				probabilities := []float64{q, 0.25, 0.38}
				trainCount += len(c.Train)
				for _, o := range c.Heldout {
					if *o.Correct {
						correctCount++
					}
				}
				for i, probability := range probabilities {
					var loss offlineEvalLoss
					for _, o := range c.Heldout {
						loss.add(t, probability, *o.Correct)
						totals[i].add(t, probability, *o.Correct)
					}
					brier, logloss := loss.mean(t)
					t.Logf("case=%s model=%s train=%d heldout=%d frozen_response_p=%.9f Brier=%.6f LogLoss=%.6f",
						c.ID, names[i], len(c.Train), len(c.Heldout), probability, brier, logloss)
				}
			})
		}
		if t.Failed() {
			t.Fatal("prediction checks failed; aggregate metrics withheld")
		}
		for i, total := range totals {
			brier, logloss := total.mean(t)
			t.Logf("prediction_micro model=%s sequences=%d train=%d heldout=%d correct=%d Brier=%.6f LogLoss=%.6f",
				names[i], len(data.PredictionCases), trainCount, total.Count, correctCount, brier, logloss)
		}
	})
}

func offlineEvalRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func offlineEvalValidateData(t *testing.T, d offlineEvalData) (map[string]offlineEvalTopic, map[string]offlineEvalState) {
	t.Helper()
	if d.SchemaVersion != 1 || d.ID == "" || d.AlgorithmVersion != types.LearningAlgorithmVersion ||
		d.Provenance == "" || d.LabelPolicy == "" || d.PredictionPolicy == "" || d.Now.IsZero() ||
		len(d.RankingCases) == 0 || len(d.PredictionCases) == 0 {
		t.Fatal("incomplete or incompatible evaluation metadata")
	}
	topics := map[string]offlineEvalTopic{}
	for _, topic := range d.Topics {
		if _, exists := topics[topic.ID]; exists || topic.ID == "" || topic.Title == "" || topic.Chunks < 0 || topic.Degree < 0 {
			t.Fatalf("invalid or duplicate topic: %+v", topic)
		}
		if !types.LearningEligiblePage(&types.WikiPage{PageType: topic.PageType, Status: types.WikiPageStatusPublished}) {
			t.Fatalf("ineligible topic kind: %s", topic.ID)
		}
		topics[topic.ID] = topic
	}
	states := map[string]offlineEvalState{}
	for _, state := range d.States {
		if _, exists := states[state.ID]; exists || state.ID == "" || state.Why == "" || state.Expected.State == "" {
			t.Fatalf("invalid or duplicate state: %s", state.ID)
		}
		states[state.ID] = state
	}
	seenCases := map[string]bool{}
	for _, c := range d.RankingCases {
		if c.ID == "" || seenCases[c.ID] || c.Goal == "" || len(c.Candidates) <= 5 {
			t.Fatalf("ranking case must have an ID, goal, and more than five candidates: %s", c.ID)
		}
		seenCases[c.ID] = true
		seenTopics := map[string]bool{}
		for _, candidate := range c.Candidates {
			_, topicExists := topics[candidate.Topic]
			_, stateExists := states[candidate.State]
			if !topicExists || !stateExists || seenTopics[candidate.Topic] ||
				candidate.Relevance < 0 || candidate.Relevance > 3 || candidate.Why == "" {
				t.Fatalf("invalid candidate in %s: %+v", c.ID, candidate)
			}
			seenTopics[candidate.Topic] = true
		}
	}
	seenCases = map[string]bool{}
	for _, c := range d.PredictionCases {
		if seenCases[c.ID] {
			t.Fatalf("duplicate prediction case %s", c.ID)
		}
		seenCases[c.ID] = true
		if err := offlineEvalValidateSequence(c, d.Now); err != nil {
			t.Fatal(err)
		}
	}
	return topics, states
}

func offlineEvalNode(topic offlineEvalTopic, state offlineEvalState) *types.LearningNode {
	page := &types.WikiPage{
		ID: topic.ID, KnowledgeBaseID: "synthetic-kb", Slug: topic.PageType + "/" + topic.ID,
		Title: topic.Title, Aliases: slices.Clone(topic.Aliases), PageType: topic.PageType,
		Status: types.WikiPageStatusPublished, Summary: topic.Summary,
		SourceRefs: types.StringArray{"synthetic-source|Hand-authored fixture"},
	}
	for i := 0; i < topic.Chunks; i++ {
		page.ChunkRefs = append(page.ChunkRefs, fmt.Sprintf("%s-chunk-%d", topic.ID, i))
	}
	node := &types.LearningNode{Page: page, SourceStamp: state.CurrentSourceStamp}
	if m := state.Mastery; m != nil {
		node.Mastery = &types.LearningMastery{
			PMastery: m.PMastery, Attempts: m.Attempts, Correct: m.Correct, ConsecutiveCorrect: m.ConsecutiveCorrect,
			LastAssessedAt: m.LastAssessedAt, NextReviewAt: m.NextReviewAt, ViewedAt: m.ViewedAt, SourceStamp: m.SourceStamp,
		}
	}
	return node
}

// Both baselines see the exact same candidates. Neither uses relevance labels,
// personal state, relatedness, or interests. Integer sixths avoid sharing the
// production scorer when implementing the source-metadata-only baseline.
func offlineEvalBaseline(c offlineEvalRanking, topics map[string]offlineEvalTopic, degreeOnly bool) []string {
	ids := make([]string, 0, len(c.Candidates))
	for _, candidate := range c.Candidates {
		ids = append(ids, candidate.Topic)
	}
	key := func(id string) int {
		topic := topics[id]
		if degreeOnly {
			return topic.Degree
		}
		quality := min(3, topic.Chunks)
		if strings.TrimSpace(topic.Summary) != "" {
			quality += 3
		}
		return quality
	}
	sort.Slice(ids, func(i, j int) bool {
		if key(ids[i]) != key(ids[j]) {
			return key(ids[i]) > key(ids[j])
		}
		return ids[i] < ids[j]
	})
	return ids[:min(5, len(ids))]
}

type offlineEvalRankingMetrics struct{ NDCG, Recall float64 }

func offlineEvalMeasureRanking(t *testing.T, ids []string, labels map[string]int, k int) offlineEvalRankingMetrics {
	t.Helper()
	var dcg, ideal float64
	hits, relevant := 0, 0
	grades := make([]int, 0, len(labels))
	for _, grade := range labels {
		grades = append(grades, grade)
		if grade > 0 {
			relevant++
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))
	for i, grade := range grades[:min(k, len(grades))] {
		ideal += (math.Exp2(float64(grade)) - 1) / math.Log2(float64(i+2))
	}
	seen := map[string]bool{}
	for i, id := range ids {
		grade, exists := labels[id]
		if !exists || seen[id] {
			t.Fatalf("unknown or repeated recommendation %q", id)
		}
		seen[id] = true
		if i >= k {
			continue
		}
		dcg += (math.Exp2(float64(grade)) - 1) / math.Log2(float64(i+2))
		if grade > 0 {
			hits++
		}
	}
	var result offlineEvalRankingMetrics
	if ideal > 0 {
		result.NDCG = dcg / ideal
	}
	if relevant > 0 {
		result.Recall = float64(hits) / float64(relevant)
	}
	return result
}

func offlineEvalValidateSequence(c offlineEvalPrediction, now time.Time) error {
	if c.ID == "" || c.Why == "" || c.SourceStamp == "" || len(c.Heldout) == 0 {
		return fmt.Errorf("incomplete prediction case %q", c.ID)
	}
	seen := map[string]bool{}
	var previous time.Time
	for _, partition := range [][]offlineEvalObservation{c.Train, c.Heldout} {
		for _, o := range partition {
			if o.QuestionID == "" || seen[o.QuestionID] || o.Correct == nil ||
				o.At.IsZero() || !o.At.After(previous) || o.At.After(now) {
				return fmt.Errorf("%s: require distinct questions and strictly increasing train-then-heldout timestamps: %s", c.ID, o.QuestionID)
			}
			seen[o.QuestionID], previous = true, o.At
		}
	}
	return nil
}

func offlineEvalTrain(train []offlineEvalObservation, stamp string) float64 {
	m := &types.LearningMastery{PMastery: types.LearningInitialMastery}
	for _, o := range train {
		types.LearningAssess(m, stamp, *o.Correct, o.At)
	}
	return offlineEvalResponseChance(m.PMastery)
}

func offlineEvalResponseChance(p float64) float64 { return 0.90*p + 0.25*(1-p) }

type offlineEvalLoss struct {
	Count          int
	Brier, LogLoss float64
}

func (l *offlineEvalLoss) add(t *testing.T, q float64, correct bool) {
	t.Helper()
	if math.IsNaN(q) || q <= 0 || q >= 1 {
		t.Fatalf("invalid response probability %v", q)
	}
	y := 0.0
	if correct {
		y = 1
		l.LogLoss -= math.Log(q)
	} else {
		l.LogLoss -= math.Log1p(-q)
	}
	l.Brier += (q - y) * (q - y)
	l.Count++
}

func (l offlineEvalLoss) mean(t *testing.T) (float64, float64) {
	t.Helper()
	if l.Count == 0 {
		t.Fatal("cannot report prediction metrics with no heldout observations")
	}
	return l.Brier / float64(l.Count), l.LogLoss / float64(l.Count)
}

func offlineEvalClose(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > 1e-12 {
		t.Fatalf("%s: got %.16g, want %.16g", name, got, want)
	}
}

func offlineEvalGoldenArithmetic(t *testing.T) {
	offlineEvalClose(t, "initial mastery", types.LearningInitialMastery, 1.0/5)
	// Independently derived rational goldens: start hidden mass (1/5, 4/5),
	// multiply known/unknown mass by (9/10, 1/4) for C or (1/10, 3/4) for F,
	// normalize, then move 3/20 of the unknown mass to known.
	// Example C: known posterior=9/19; transitioned mastery=21/38;
	// the NEXT response chance is 463/760, not 21/38.
	goldens := []struct {
		Answers string
		P, Q    float64
	}{
		{"", 1.0 / 5, 19.0 / 50},
		{"C", 21.0 / 38, 463.0 / 760},
		{"F", 11.0 / 62, 453.0 / 1240},
		{"CC", 1563.0 / 1852, 29579.0 / 37040},
		{"CF", 107.0 / 396, 3371.0 / 7920},
		{"FC", 315.0 / 604, 1423.0 / 2416},
		{"FF", 547.0 / 3148, 22851.0 / 62960},
		{"CCC", 113403.0 / 118316, 2065819.0 / 2366320},
		{"CFC", 8571.0 / 13484, 178843.0 / 269680},
		{"FCF", 1707.0 / 6620, 55291.0 / 132400},
		{"FFF", 27785.0 / 160436, 232677.0 / 641744},
		{"FFC", 5243.0 / 10156, 118939.0 / 203120},
	}
	for _, golden := range goldens {
		p := 0.2
		var train []offlineEvalObservation
		for i, answer := range golden.Answers {
			correct := answer == 'C'
			p = types.LearningBKT(p, correct)
			train = append(train, offlineEvalObservation{At: time.Date(2026, 9, i+1, 12, 0, 0, 0, time.UTC), Correct: &correct})
		}
		offlineEvalClose(t, golden.Answers+" BKT", p, golden.P)
		offlineEvalClose(t, golden.Answers+" response mapping", offlineEvalResponseChance(p), golden.Q)
		offlineEvalClose(t, golden.Answers+" assessed training", offlineEvalTrain(train, "v1"), golden.Q)
	}
	// Retrieved grades [3,0,1,0,2]; ideal [3,2,2,1,0], four relevant
	// candidates total. DCG=7+1/2+3/log2(6), IDCG=7+3/log2(3)+3/2+1/log2(5).
	metrics := offlineEvalMeasureRanking(t, []string{"a", "b", "c", "d", "e"},
		map[string]int{"a": 3, "b": 0, "c": 1, "d": 0, "e": 2, "f": 2}, 5)
	offlineEvalClose(t, "NDCG arithmetic", metrics.NDCG, 0.8001649902816075)
	offlineEvalClose(t, "Recall denominator includes unretrieved relevant candidates", metrics.Recall, 3.0/4)
	zero := offlineEvalMeasureRanking(t, []string{"a"}, map[string]int{"a": 0}, 5)
	offlineEvalClose(t, "no relevant NDCG", zero.NDCG, 0)
	offlineEvalClose(t, "no relevant Recall", zero.Recall, 0)
	short := offlineEvalMeasureRanking(t, []string{"a"}, map[string]int{"a": 1, "b": 1}, 5)
	offlineEvalClose(t, "short ranking Recall", short.Recall, 0.5)
	var loss offlineEvalLoss
	loss.add(t, 0.25, true)
	loss.add(t, 0.75, false)
	brier, logloss := loss.mean(t)
	offlineEvalClose(t, "Brier arithmetic", brier, 9.0/16)
	offlineEvalClose(t, "natural-log loss arithmetic", logloss, 1.3862943611198906)

	topics := map[string]offlineEvalTopic{
		"a": {Degree: 2, Chunks: 2, Summary: "summary"},
		"b": {Degree: 7, Chunks: 1, Summary: "summary"},
		"c": {Degree: 7, Chunks: 3, Summary: " \t"},
		"d": {Degree: 0, Chunks: 6, Summary: "summary"},
		"e": {Degree: 1, Chunks: 3, Summary: "summary"},
		"f": {Degree: 5, Chunks: 0},
	}
	var baselineCase offlineEvalRanking
	for _, id := range []string{"f", "e", "d", "c", "b", "a"} {
		baselineCase.Candidates = append(baselineCase.Candidates, offlineEvalCandidate{Topic: id})
	}
	if got := offlineEvalBaseline(baselineCase, topics, true); !slices.Equal(got, []string{"b", "c", "f", "a", "e"}) {
		t.Fatalf("degree baseline order/tie breaking: %v", got)
	}
	if got := offlineEvalBaseline(baselineCase, topics, false); !slices.Equal(got, []string{"d", "e", "a", "b", "c"}) {
		t.Fatalf("cold-start baseline order/saturation/tie breaking: %v", got)
	}

	node := offlineEvalNode(offlineEvalTopic{ID: "golden", Title: "Bayesian model", Aliases: []string{"Knowledge tracing"},
		PageType: "concept", Summary: "Summary", Chunks: 2}, offlineEvalState{})
	node.Related = true
	view := types.LearningNodePublic(node, time.Time{})
	view.Mastery = types.LearningMasteryView{State: "learning", PMastery: 0.4}
	r := rankRecommendations([]*types.LearningNode{node}, []*types.LearningNodeView{view}, []string{" KNOWLEDGE   TRACING "}, 1)[0]
	offlineEvalClose(t, "review component", r.Components.ReviewNeed, 3.0/5)
	offlineEvalClose(t, "related component", r.Components.GraphFrontier, 1)
	offlineEvalClose(t, "alias interest component", r.Components.InterestMatch, 1)
	offlineEvalClose(t, "content component", r.Components.ContentQuality, 5.0/6)
	offlineEvalClose(t, "weighted score", r.Score, 241.0/300)
	if !reflect.DeepEqual(r.ReasonCodes, []string{"practice", "related_topic", "interest_match", "source_backed"}) {
		t.Fatalf("unexpected golden reasons: %v", r.ReasonCodes)
	}
}

type offlineEvalMemory struct {
	interfaces.MemoryService
	available bool
	reads     int
}

func (m *offlineEvalMemory) MemoryAvailable(context.Context) bool { return m.available }
func (m *offlineEvalMemory) DocumentAffinity(context.Context, []string) map[string]int {
	m.reads++
	return map[string]int{"synthetic-source": 2}
}

func offlineEvalStates(t *testing.T, d offlineEvalData, states map[string]offlineEvalState) {
	for _, state := range d.States {
		t.Run(state.ID, func(t *testing.T) {
			node := offlineEvalNode(d.Topics[0], state)
			var before types.LearningMastery
			if node.Mastery != nil {
				before = *node.Mastery
			}
			view := types.LearningNodePublic(node, d.Now)
			if view.Familiar != state.Familiar || !reflect.DeepEqual(view.Mastery, state.Expected) {
				t.Fatalf("got familiar=%v mastery=%+v; want familiar=%v mastery=%+v", view.Familiar, view.Mastery, state.Familiar, state.Expected)
			}
			if node.Mastery != nil && !reflect.DeepEqual(*node.Mastery, before) {
				t.Fatal("public state projection mutated stored evidence")
			}
			for _, available := range []bool{false, true} {
				memory := &offlineEvalMemory{available: available}
				svc := &service{memory: memory}
				familiarView := types.LearningNodePublic(node, d.Now)
				svc.familiar(context.Background(), []*types.LearningNode{node}, []*types.LearningNodeView{familiarView})
				wantReads := 0
				if available {
					wantReads = 1
				}
				if memory.reads != wantReads || familiarView.Familiar != (state.Familiar || available) ||
					!reflect.DeepEqual(familiarView.Mastery, state.Expected) {
					t.Fatal("memory availability/familiarity changed mastery or read disabled memory")
				}
			}
			if node.Mastery == nil {
				node.Mastery = &types.LearningMastery{}
			}
			node.Mastery.ViewedAt = &d.Now
			if got := types.LearningNodePublic(node, d.Now); !got.Familiar || !reflect.DeepEqual(got.Mastery, state.Expected) {
				t.Fatal("a stored read marker changed mastery")
			}
		})
	}
	node := offlineEvalNode(d.Topics[0], states["stale"])
	before, after := types.LearningAssess(node.Mastery, "v2", false, d.Now)
	offlineEvalClose(t, "fresh source starts from prior", before, 1.0/5)
	offlineEvalClose(t, "first wrong answer on new source", after, 11.0/62)
	if node.Mastery.Attempts != 1 || node.Mastery.Correct != 0 || node.Mastery.ConsecutiveCorrect != 0 ||
		node.Mastery.ViewedAt == nil || node.Mastery.SourceStamp != "v2" ||
		!node.Mastery.NextReviewAt.Equal(d.Now.Add(24*time.Hour)) {
		t.Fatal("new source retained old assessment credit or lost familiarity")
	}
	due := offlineEvalNode(d.Topics[0], states["due"])
	if types.LearningNodePublic(due, d.Now.Add(-time.Nanosecond)).Mastery.State != "mastered" {
		t.Fatal("review became due before its deadline")
	}
	m := &types.LearningMastery{SourceStamp: "v1", Attempts: 3, PMastery: 0.85}
	if types.LearningMasteryState(m, "v1", d.Now).State != "mastered" {
		t.Fatal("exact mastery threshold was not inclusive")
	}
	m.PMastery = math.Nextafter(0.85, 0)
	if types.LearningMasteryState(m, "v1", d.Now).State != "learning" {
		t.Fatal("mastered below probability threshold")
	}
	m = &types.LearningMastery{}
	for i, days := range []int{1, 3, 7, 14, 30, 30, 1} {
		types.LearningAssess(m, "v1", i < 6, d.Now)
		if !m.NextReviewAt.Equal(d.Now.Add(time.Duration(days) * 24 * time.Hour)) {
			t.Fatalf("wrong review interval at answer %d", i+1)
		}
	}
}

func offlineEvalTemporalGuards(t *testing.T, d offlineEvalData) {
	original := d.PredictionCases[0]
	for _, mutation := range []string{"split_overlap", "duplicate_question", "out_of_order", "future", "missing_label", "empty_holdout"} {
		c := original
		c.Train, c.Heldout = slices.Clone(c.Train), slices.Clone(c.Heldout)
		switch mutation {
		case "split_overlap":
			c.Heldout[0].At = c.Train[len(c.Train)-1].At
		case "duplicate_question":
			c.Heldout[0].QuestionID = c.Train[0].QuestionID
		case "out_of_order":
			c.Train[0], c.Train[1] = c.Train[1], c.Train[0]
		case "future":
			c.Heldout[len(c.Heldout)-1].At = d.Now.Add(time.Hour)
		case "missing_label":
			c.Heldout[0].Correct = nil
		case "empty_holdout":
			c.Heldout = nil
		}
		if err := offlineEvalValidateSequence(c, d.Now); err == nil {
			t.Fatalf("accepted invalid temporal fixture: %s", mutation)
		}
	}
	on, off := d.RankingCases[2], d.RankingCases[3]
	if on.ID != "optional_interest_on" || off.ID != "optional_interest_off" ||
		len(on.Interests) == 0 || len(off.Interests) != 0 || !reflect.DeepEqual(on.Candidates, off.Candidates) {
		t.Fatal("interest ablation must preserve all candidates and relevance labels")
	}
}
