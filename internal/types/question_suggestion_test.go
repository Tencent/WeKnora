package types

import "testing"

func TestQuestionSuggestionConfigValidate(t *testing.T) {
	config := &QuestionSuggestionConfig{
		Starters: StarterSuggestionConfig{Mode: SuggestionModeHybrid, Count: 6},
		FollowUps: FollowUpSuggestionConfig{
			Mode:            SuggestionModeHybrid,
			Count:           3,
			MaxContextTurns: 2,
			Categories:      []string{SuggestionCategoryClarify, SuggestionCategoryDeepen},
		},
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	config.FollowUps.Count = 6
	if err := config.Validate(); err == nil {
		t.Fatal("out-of-range follow-up count was accepted")
	}
}
