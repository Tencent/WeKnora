package types

import (
	"strings"
	"unicode/utf8"
)

func LearningClip(s string, bytes int) string {
	if len(s) <= bytes {
		return s
	}
	s = s[:bytes]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

func LearningValidateQuestions(source *LearningSource, questions []LearningQuestion) error {
	if source == nil || len(questions) != 3 {
		return ErrLearningEvidence
	}
	chunks := map[string]*Chunk{}
	for _, c := range source.Chunks {
		chunks[c.ID] = c
	}
	fingerprints := map[string]bool{}
	for _, q := range questions {
		fp := LearningFingerprint(q.Prompt)
		if strings.TrimSpace(q.Prompt) == "" || len(q.Prompt) > 1000 || fingerprints[fp] || len(q.Options) != 4 ||
			strings.TrimSpace(q.Explanation) == "" || len(q.Explanation) > 2000 || len(q.Evidence) == 0 || len(q.Evidence) > 3 {
			return ErrLearningEvidence
		}
		fingerprints[fp] = true
		ids, texts := map[string]bool{}, map[string]bool{}
		for _, o := range q.Options {
			text := strings.ToLower(LearningNormalize(o.Text))
			if o.ID == "" || len(o.ID) > 36 || ids[o.ID] || text == "" || len(o.Text) > 500 || texts[text] {
				return ErrLearningEvidence
			}
			ids[o.ID], texts[text] = true, true
		}
		if !ids[q.CorrectOption] {
			return ErrLearningEvidence
		}
		for _, e := range q.Evidence {
			c := chunks[e.ChunkID]
			quote := LearningNormalize(e.Quote)
			if c == nil || c.KnowledgeID != e.KnowledgeID || len(e.Quote) > 1000 || utf8.RuneCountInString(quote) < 10 ||
				!strings.Contains(LearningNormalize(LearningClip(c.Content, LearningChunkBytes)), quote) {
				return ErrLearningEvidence
			}
		}
	}
	return nil
}
