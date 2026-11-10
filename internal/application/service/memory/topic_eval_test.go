package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Whether two labels name the same subject is the one decision in this feature
// that cannot be reviewed by reading the code. Both failure directions are
// silent and both make the feature useless:
//
//	too strict — one subject spreads across rows, none reaches the threshold
//	too loose  — every specific question in a domain collapses into one bucket
//
// This file is how that gets measured instead of argued about. Offline it
// checks the deterministic tiers and that the prompt still carries the rules
// the boundary cases depend on. Given a real model it scores the model tier:
//
//	WEKNORA_MEMORY_EVAL_MODEL=<model id> \
//	WEKNORA_MEMORY_EVAL_BASE_URL=... WEKNORA_MEMORY_EVAL_API_KEY=... \
//	go test ./internal/application/service/memory/ -run TestTopicMergeEval -v

type topicEvalCase struct {
	Name     string `json:"name"`
	Tracked  string `json:"tracked"`
	Incoming string `json:"incoming"`
	Same     bool   `json:"same"`
	Why      string `json:"why"`
}

type topicEvalSet struct {
	Description string          `json:"description"`
	Cases       []topicEvalCase `json:"cases"`
}

func loadTopicEvalSet(t *testing.T) topicEvalSet {
	t.Helper()
	raw, err := os.ReadFile("topic_evalset.json")
	require.NoError(t, err)
	var set topicEvalSet
	require.NoError(t, json.Unmarshal(raw, &set))
	require.NotEmpty(t, set.Cases)
	for _, c := range set.Cases {
		require.NotEmpty(t, c.Tracked, "case %q has no tracked label", c.Name)
		require.NotEmpty(t, c.Incoming, "case %q has no incoming label", c.Name)
		require.NotEmpty(t, c.Why, "case %q does not say why", c.Name)
	}
	return set
}

// The cheap tiers must never merge a pair the set says is different. They are
// allowed to miss a merge — that is what the model tier is for — but a wrong
// merge here happens with no model in the loop and no way to notice.
func TestCheapTiersNeverMergeDifferentSubjects(t *testing.T) {
	set := loadTopicEvalSet(t)
	for _, c := range set.Cases {
		existing := []*types.MemoryTopicStat{{
			Topic:         c.Tracked,
			NormalizedKey: types.NormalizeTopicKey(c.Tracked),
		}}
		merged := matchTopicExactly(c.Incoming, existing) != nil ||
			matchTopicLoosely(c.Incoming, existing) != nil
		if !c.Same {
			require.False(t, merged,
				"case %q: %q must not be folded into %q without a model saying so — %s",
				c.Name, c.Incoming, c.Tracked, c.Why)
		}
	}
}

// Report which cases each tier settles, so a threshold change shows up as a
// shift in this table rather than as a surprise in production.
func TestTopicTierCoverageIsVisible(t *testing.T) {
	set := loadTopicEvalSet(t)
	var needModel []string
	for _, c := range set.Cases {
		existing := []*types.MemoryTopicStat{{
			Topic:         c.Tracked,
			NormalizedKey: types.NormalizeTopicKey(c.Tracked),
		}}
		tier := "model"
		switch {
		case matchTopicExactly(c.Incoming, existing) != nil:
			tier = "exact"
		case matchTopicLoosely(c.Incoming, existing) != nil:
			tier = "fuzzy"
		}
		if tier == "model" {
			needModel = append(needModel, c.Name)
		}
		t.Logf("%-40s same=%-5v tier=%-5s dice=%.2f",
			c.Name, c.Same, tier, types.TopicSimilarity(c.Tracked, c.Incoming))
	}
	// If the cheap tiers ever settle everything, the thresholds have been
	// loosened past the point where they can only be right.
	require.NotEmpty(t, needModel,
		"no case reaches the model tier, which means the cheap tiers are deciding things they cannot know")
}

// The rules the boundary cases depend on have to actually be in the prompts.
// This is a weak check, but it is the one that catches a rule being edited away
// — which is exactly how the "specific lookup" case started merging.
func TestTopicPromptsCarryTheRulesTheEvalSetDependsOn(t *testing.T) {
	require.Contains(t, topicAdjudicationPrompt, "具体查询",
		"the adjudication prompt must still separate a specific lookup from a standing interest")
	require.Contains(t, topicAdjudicationPrompt, "拿不准就判不同",
		"a merge is unrecoverable in a way a miss is not, so ties have to break apart")
	require.NotContains(t, topicAdjudicationPrompt, "算同一件事，归到已有主题",
		"folding subtopics into their parent is what collapsed a domain into one bucket")

	segment := transcriptSegment{lines: []transcriptLine{{content: "问题"}}}
	prompt := buildExtractionPrompt(segment, nil, nil,
		[]*types.MemoryTopicStat{{Topic: "门店排班管理"}}, "")
	require.Contains(t, prompt, "SAME subject",
		"reuse has to be an identity test; 'is about' is a relatedness test and merges everything adjacent")
	require.Contains(t, prompt, "Do not force a fit")
}

// Long lists invite picking something off them, so the extraction call sees a
// bounded slice even when the resolver considers more.
func TestExtractionPromptDoesNotDumpEveryTrackedTopic(t *testing.T) {
	var tracked []*types.MemoryTopicStat
	for i := 0; i < extractShownTopics*3; i++ {
		tracked = append(tracked, &types.MemoryTopicStat{Topic: fmt.Sprintf("主题%d", i)})
	}
	prompt := buildExtractionPrompt(
		transcriptSegment{lines: []transcriptLine{{content: "问题"}}}, nil, nil, tracked, "")
	require.Contains(t, prompt, "主题0")
	require.NotContains(t, prompt, fmt.Sprintf("主题%d", extractShownTopics))
}

// TestTopicMergeEval scores the model tier against the golden set.
// Skipped unless WEKNORA_MEMORY_EVAL_MODEL is set.
func TestTopicMergeEval(t *testing.T) {
	modelID := strings.TrimSpace(os.Getenv("WEKNORA_MEMORY_EVAL_MODEL"))
	if modelID == "" {
		t.Skip("set WEKNORA_MEMORY_EVAL_MODEL (plus base URL / API key) to score topic merging")
	}
	set := loadTopicEvalSet(t)
	chatModel, err := newEvalChatModel(modelID)
	require.NoError(t, err)

	correct := 0
	for _, c := range set.Cases {
		user := fmt.Sprintf("已有主题：\n[0] %s\n\n新出现的说法：\n[0] %s\n", c.Tracked, c.Incoming)
		decided, err := askTopicMerge(chatModel, user)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if decided == c.Same {
			correct++
			t.Logf("PASS %-40s same=%v", c.Name, c.Same)
			continue
		}
		t.Logf("FAIL %-40s expected same=%v, got %v — %s", c.Name, c.Same, decided, c.Why)
	}
	t.Logf("topic merge: %d/%d", correct, len(set.Cases))
}

// askTopicMerge runs the real adjudication prompt for one pair and reports
// whether the model merged them.
func askTopicMerge(chatModel chat.Chat, user string) (bool, error) {
	thinking := false
	response, err := chatModel.Chat(context.Background(), []chat.Message{
		{Role: "system", Content: topicAdjudicationPrompt},
		{Role: "user", Content: user},
	}, &chat.ChatOptions{
		Temperature:         0,
		MaxCompletionTokens: 800,
		Thinking:            &thinking,
		Format:              topicAdjudicationSchema,
	})
	if err != nil {
		return false, err
	}
	if response == nil {
		return false, fmt.Errorf("no response")
	}
	content := strings.TrimSpace(response.Content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return false, fmt.Errorf("no JSON object in %q", content)
	}
	var parsed struct {
		Resolutions []struct {
			SameAs *int `json:"same_as"`
		} `json:"resolutions"`
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &parsed); err != nil {
		return false, err
	}
	return len(parsed.Resolutions) > 0 && parsed.Resolutions[0].SameAs != nil, nil
}

// The fuzzy threshold is only defensible if there is daylight between the pairs
// that mean the same thing and the pairs that do not. This measures that gap
// and fails when an edit closes it — which is the only way to know a threshold
// change is safe without shipping it and waiting for someone to notice their
// interests turned into one bucket.
func TestFuzzyThresholdSitsInARealGap(t *testing.T) {
	set := loadTopicEvalSet(t)

	highestDifferent, lowestFuzzyMerged := 0.0, 1.0
	var highestName, lowestName string
	for _, c := range set.Cases {
		score := types.TopicSimilarity(c.Tracked, c.Incoming)
		if !c.Same {
			if score > highestDifferent {
				highestDifferent, highestName = score, c.Name
			}
			continue
		}
		if score >= topicFuzzyThreshold && score < lowestFuzzyMerged {
			lowestFuzzyMerged, lowestName = score, c.Name
		}
	}

	require.Greater(t, lowestFuzzyMerged, highestDifferent,
		"%q (%.2f, same) scores no higher than %q (%.2f, different): character overlap "+
			"cannot separate these, so the threshold is picking one at random",
		lowestName, lowestFuzzyMerged, highestName, highestDifferent)
	require.Greater(t, topicFuzzyThreshold, highestDifferent,
		"the threshold must sit above every pair that is not the same subject; %q scores %.2f",
		highestName, highestDifferent)
	require.LessOrEqual(t, topicFuzzyThreshold, lowestFuzzyMerged,
		"the threshold must not sit above a pair it is supposed to merge; %q scores %.2f",
		lowestName, lowestFuzzyMerged)

	t.Logf("fuzzy threshold %.2f sits between %q (%.2f, different) and %q (%.2f, same)",
		topicFuzzyThreshold, highestName, highestDifferent, lowestName, lowestFuzzyMerged)
}

type topicGranularityCase struct {
	Question string `json:"question"`
	Good     string `json:"good"`
	Bad      string `json:"bad"`
}

// A subject has to recur to be worth anything. A label carrying the parameters
// of one question — a name, an age group, a distance, an edition number — can
// only ever match itself, so it is counted once and sits at one hit forever
// while the feature looks like it is working.
//
// This is not hypothetical: the prompt used to offer "某选手的参赛项目" as the
// model of a good separate subject, and production filled up with labels like
// "v2.3版本orders接口分页参数默认值查询".
func TestGoodSubjectsRecurAndQueryShapedOnesDoNot(t *testing.T) {
	var set struct {
		Granularity struct {
			Cases []topicGranularityCase `json:"cases"`
		} `json:"granularity"`
	}
	raw, err := os.ReadFile("topic_evalset.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &set))
	require.NotEmpty(t, set.Granularity.Cases)

	for _, c := range set.Granularity.Cases {
		require.False(t, types.TopicLooksLikeOneQuestion(c.Good),
			"%q is the subject we want and must not be flagged as a one-off", c.Good)
		require.True(t, types.TopicLooksLikeOneQuestion(c.Bad) ||
			types.TopicSimilarity(c.Good, c.Bad) < 1.0,
			"%q names the question rather than the subject", c.Bad)
	}
}

// The granularity rule has to survive in the prompt, since nothing downstream
// can recover a subject from a label that already baked one question into it.
func TestExtractionPromptTeachesSubjectLevelNaming(t *testing.T) {
	require.Contains(t, extractionSystemPrompt, "RECURS",
		"the reason a subject must be nameable at a recurring level has to be stated")
	require.Contains(t, extractionSystemPrompt, "belong to the question, not to the subject name")
	require.Contains(t, extractionSystemPrompt, "are categories, not",
		"the opposite failure — naming a category — has to stay ruled out too")

	prompt := buildExtractionPrompt(
		transcriptSegment{lines: []transcriptLine{{content: "问题"}}}, nil, nil,
		[]*types.MemoryTopicStat{{Topic: "门店排班管理"}}, "")
	require.NotContains(t, prompt, "某选手的参赛项目",
		"that example taught the model to name queries, which is how the topic table filled "+
			"with labels that can never match anything again")
	require.Contains(t, prompt, "same level of",
		"a new label has to be written at the level of the ones already tracked")
}
