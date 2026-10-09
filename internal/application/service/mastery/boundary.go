package mastery

import (
	"math"
	"sort"
)

// FamiliarLevel is the water-level threshold at/above which a node counts as
// "familiar" for boundary seeding. It matches the familiar tier floor (level 40)
// in the ten-tier water-level mapping.
const FamiliarLevel = 40

// DefaultBoundaryTopK is the number of boundary candidates the guidance view
// highlights by default.
const DefaultBoundaryTopK = 12

// BoundaryNode is one knowledge-frontier candidate with its PPR score. The
// score lets the recommendation layer rank one-hop neighbors precisely instead
// of treating the boundary as an undifferentiated set.
type BoundaryNode struct {
	Slug  string
	Score float64
}

// ComputeBoundary identifies the knowledge frontier: nodes that sit adjacent to
// the user's familiar/mastered region but are not themselves familiar. It runs
// a personalized PageRank seeded from familiar nodes and returns the top-K
// unfamiliar nodes by PPR score, which the frontend renders as the boundary
// ripple. It is a pure, deterministic function so it can be unit-tested and
// replayed offline.
//
// Parameters:
//   - levels: slug → water level (0..100). Nodes with level >= familiarScore
//     are the seeds (the user's known region).
//   - adjacency: slug → neighbor slugs (undirected).
//   - familiarScore: the level threshold at/above which a node is "familiar".
//   - topK: max boundary nodes to return (capped defensively).
func ComputeBoundary(
	levels map[string]int,
	adjacency map[string][]string,
	familiarScore int,
	topK int,
) []BoundaryNode {
	if topK <= 0 {
		topK = DefaultBoundaryTopK
	}
	if familiarScore <= 0 {
		familiarScore = FamiliarLevel
	}

	seeds := make([]string, 0)
	for slug, lv := range levels {
		if lv >= familiarScore {
			seeds = append(seeds, slug)
		}
	}
	// Cold start: no familiar region, so there is no boundary to highlight.
	if len(seeds) == 0 {
		return nil
	}

	const (
		alpha     = 0.15
		tolerance = 1e-8
		maxIter   = 100
	)

	outDeg := make(map[string]int, len(adjacency))
	for slug, nbrs := range adjacency {
		for _, n := range nbrs {
			if n != slug {
				outDeg[slug]++
			}
		}
	}

	// Personalized restart distribution over the familiar seeds.
	p := make(map[string]float64, len(seeds))
	for _, s := range seeds {
		p[s] = 1.0 / float64(len(seeds))
	}

	// r = alpha*p + (1-alpha) * A^T r, where A is the row-stochastic adjacency.
	r := make(map[string]float64, len(p))
	for k, v := range p {
		r[k] = v
	}

	for iter := 0; iter < maxIter; iter++ {
		next := make(map[string]float64, len(adjacency))
		for k, v := range p {
			next[k] = alpha * v
		}
		for u, ru := range r {
			d := outDeg[u]
			if d == 0 {
				continue
			}
			share := ru / float64(d)
			for _, v := range adjacency[u] {
				if v == u {
					continue
				}
				next[v] += (1 - alpha) * share
			}
		}
		var diff float64
		for k, v := range next {
			diff += math.Abs(v - r[k])
		}
		r = next
		if diff < tolerance {
			break
		}
	}

	cands := make([]BoundaryNode, 0, len(r))
	for slug, score := range r {
		if levels[slug] >= familiarScore {
			continue // already familiar — not a boundary candidate
		}
		if score <= 0 {
			continue
		}
		cands = append(cands, BoundaryNode{Slug: slug, Score: score})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Score != cands[j].Score {
			return cands[i].Score > cands[j].Score
		}
		return cands[i].Slug < cands[j].Slug
	})

	out := make([]BoundaryNode, 0, topK)
	for i := 0; i < len(cands) && i < topK; i++ {
		out = append(out, cands[i])
	}
	return out
}
