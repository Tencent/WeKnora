package hostreview

import "github.com/Tencent/WeKnora/internal/types"

const (
	transcriptFetch    = 20
	maxEarlierMessages = 4
	maxMessageRunes    = 2000
	// maxCurrentTurnBytes is the most of this turn's user text auto review
	// will read. Above it the call refuses, the same way an oversized action
	// does: truncating would drop the part that authorizes the action.
	maxCurrentTurnBytes = 64 << 10
)

// splitTranscript separates this turn's user messages (the one that started
// the turn and any added mid-run, all stored under its RequestID) from
// earlier ones. Fork copies carry new RequestIDs, so they are always earlier.
// This turn is kept whole. Only earlier messages are shortened, and they
// cannot authorize on their own.
func splitTranscript(msgs []*types.Message, requestID string) (current, earlier []string) {
	for _, m := range msgs {
		if requestID != "" && m.RequestID == requestID {
			current = append(current, m.Content)
			continue
		}
		earlier = append(earlier, truncateRunes(m.Content, maxMessageRunes))
	}
	if len(earlier) > maxEarlierMessages {
		earlier = earlier[len(earlier)-maxEarlierMessages:]
	}
	return current, earlier
}

func currentTurnTooLong(current []string) bool {
	n := 0
	for _, text := range current {
		n += len(text)
		if n > maxCurrentTurnBytes {
			return true
		}
	}
	return false
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
