package learning

import (
	"math"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

func rankRecommendations(
	nodes []*types.LearningNode,
	views []*types.LearningNodeView,
	interests []string,
	limit int,
) []*types.LearningRecommendation {
	if limit <= 0 {
		return []*types.LearningRecommendation{}
	}
	pool := make([]*types.LearningRecommendation, 0, len(nodes))
	for i, node := range nodes {
		v := views[i]
		if v.Mastery.State == "mastered" && !v.Mastery.SourceStale {
			continue
		}
		c := types.LearningScoreComponents{}
		reasons := []string{}
		switch v.Mastery.State {
		case "review_due":
			c.ReviewNeed = 1
			reasons = append(reasons, "review_due")
		case "learning":
			c.ReviewNeed = 1 - v.Mastery.PMastery
			reasons = append(reasons, "practice")
		}
		if v.Mastery.SourceStale {
			c.ReviewNeed = 1
			reasons = append(reasons, "source_changed")
		}
		if node.Related {
			c.GraphFrontier = 1
			reasons = append(reasons, "related_topic")
		}
		labels := append([]string{node.Page.Title}, node.Page.Aliases...)
		for _, label := range labels {
			label = strings.ToLower(types.LearningNormalize(label))
			for _, interest := range interests[:min(len(interests), 8)] {
				interest = strings.ToLower(types.LearningNormalize(types.LearningClip(interest, 128)))
				if interest != "" && strings.Contains(label, interest) {
					c.InterestMatch = 1
				}
			}
		}
		if c.InterestMatch > 0 {
			reasons = append(reasons, "interest_match")
		}
		c.ContentQuality = 0.5 * math.Min(1, float64(len(node.Page.ChunkRefs))/3)
		if strings.TrimSpace(node.Page.Summary) != "" {
			c.ContentQuality += 0.5
		}
		if len(node.Page.ChunkRefs) > 0 && len(node.Page.SourceRefs) > 0 {
			reasons = append(reasons, "source_backed")
		}
		if v.Mastery.State == "unseen" {
			reasons = append(reasons, "explore")
		}
		score := 0.45*c.ReviewNeed + 0.25*c.GraphFrontier + 0.20*c.InterestMatch + 0.10*c.ContentQuality
		pool = append(
			pool,
			&types.LearningRecommendation{LearningNodeView: *v, Score: score, Components: c, ReasonCodes: reasons},
		)
	}
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].Score != pool[j].Score {
			return pool[i].Score > pool[j].Score
		}
		return pool[i].PageID < pool[j].PageID
	})
	limit = min(limit, len(pool))
	result := make([]*types.LearningRecommendation, 0, limit)
	used := map[string]bool{}
	var exploration *types.LearningRecommendation
	if limit >= 2 {
		for _, r := range pool {
			if r.Mastery.State == "unseen" {
				exploration = r
				break
			}
		}
	}
	regular := limit
	if exploration != nil {
		regular--
		used[exploration.PageID] = true
	}
	// Due review takes precedence over page-kind diversity. Diversity only
	// arranges the remaining slots; it cannot postpone overdue assessments.
	kinds := map[string]bool{}
	for _, r := range pool {
		if len(result) >= regular {
			break
		}
		if !used[r.PageID] && (r.Mastery.State == "review_due" || r.Mastery.SourceStale) {
			result = append(result, r)
			used[r.PageID], kinds[r.PageType] = true, true
		}
	}
	for pass := 0; pass < 2; pass++ {
		for _, r := range pool {
			if len(result) >= regular {
				break
			}
			if used[r.PageID] || (pass == 0 && kinds[r.PageType]) {
				continue
			}
			result = append(result, r)
			used[r.PageID], kinds[r.PageType] = true, true
		}
	}
	if exploration != nil {
		result = append(result, exploration)
	}
	return result
}
