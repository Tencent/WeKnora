package meetingorchestration

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

var sessionContinuationPattern = regexp.MustCompile(`(?i)(继续|接着|刚才|上一部分|上半部分|前面提到|承接|then|continue|following up)`)

// transcriptWindowText extracts the content evidence used for sessionization.
// Titles and external grouping metadata are intentionally excluded.
func transcriptWindowText(chunks []transcript.Chunk) (opening, closing, content string) {
	if len(chunks) == 0 {
		return "", "", ""
	}
	parts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if text := strings.TrimSpace(chunk.Content); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", "", ""
	}
	window := 3
	if len(parts) < window {
		window = len(parts)
	}
	opening = strings.Join(parts[:window], " ")
	closing = strings.Join(parts[len(parts)-window:], " ")
	content = strings.Join(parts, " ")
	return opening, closing, content
}

func transcriptBoundaryEvidenceIDs(chunks []transcript.Chunk) []string {
	if len(chunks) == 0 {
		return []string{}
	}
	ids := []string{strings.TrimSpace(chunks[0].EvidenceSentenceID)}
	if last := strings.TrimSpace(chunks[len(chunks)-1].EvidenceSentenceID); last != "" && last != ids[0] {
		ids = append(ids, last)
	}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			result = append(result, id)
		}
	}
	return result
}

func groupCandidateScopes(input []candidateScope, evidence map[string]evidenceRecord) ([]candidateScope, []MeetingSession, int) {
	if len(input) == 0 {
		return nil, nil, 0
	}
	sorted := append([]candidateScope(nil), input...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].createdAt.IsZero() || sorted[j].createdAt.IsZero() {
			return sorted[i].videoID < sorted[j].videoID
		}
		if sorted[i].createdAt.Equal(sorted[j].createdAt) {
			return sorted[i].videoID < sorted[j].videoID
		}
		return sorted[i].createdAt.Before(sorted[j].createdAt)
	})

	groups := make([][]candidateScope, 0, len(sorted))
	possiblePairs := 0
	for _, scope := range sorted {
		if len(groups) == 0 || !sameSessionByContent(groups[len(groups)-1][len(groups[len(groups)-1])-1], scope) {
			if len(groups) > 0 && hasSessionEvidenceGap(groups[len(groups)-1][len(groups[len(groups)-1])-1], scope) {
				possiblePairs++
			}
			groups = append(groups, []candidateScope{scope})
			continue
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], scope)
	}

	result := make([]candidateScope, 0, len(groups))
	sessions := make([]MeetingSession, 0, len(groups))
	for _, group := range groups {
		merged := mergeCandidateScopes(group)
		result = append(result, merged)
		refs := make([]EvidenceRef, 0)
		for _, id := range merged.boundaryEvidenceIDs {
			if record, ok := evidence[id]; ok && record.EndMs > record.StartMs {
				refs = append(refs, EvidenceRef{VideoID: record.VideoID, TranscriptGeneration: record.Generation, EvidenceID: id, StartMs: record.StartMs, EndMs: record.EndMs})
			}
		}
		videoIDs := make([]string, 0, len(group))
		for _, scope := range group {
			videoIDs = append(videoIDs, scope.videoID)
		}
		basis := "content_evidence"
		if len(group) == 1 {
			basis = "single_fragment_content_evidence"
		}
		sessions = append(sessions, MeetingSession{MeetingSessionID: sessionIDFromScope(merged), FragmentVideoIDs: videoIDs, OrderingBasis: basis, GroupingEvidenceRefs: refs})
	}
	return result, sessions, possiblePairs
}

func mergeCandidateScopes(group []candidateScope) candidateScope {
	merged := candidateScope{
		videoID:             group[0].videoID,
		createdAt:           group[0].createdAt,
		allowedEvidence:     map[string]struct{}{},
		allowedSections:     map[string]struct{}{},
		allowedBlocks:       map[string]struct{}{},
		actionableSections:  map[string]struct{}{},
		videos:              make([]map[string]any, 0, len(group)),
		boundaryEvidenceIDs: []string{},
	}
	for _, scope := range group {
		merged.videos = append(merged.videos, scope.videos...)
		for id := range scope.allowedEvidence {
			merged.allowedEvidence[id] = struct{}{}
		}
		for id := range scope.allowedSections {
			merged.allowedSections[id] = struct{}{}
		}
		for id := range scope.allowedBlocks {
			merged.allowedBlocks[id] = struct{}{}
		}
		for id := range scope.actionableSections {
			merged.actionableSections[id] = struct{}{}
		}
		merged.boundaryEvidenceIDs = appendUniqueStrings(merged.boundaryEvidenceIDs, scope.boundaryEvidenceIDs...)
		merged.contentText += " " + scope.contentText
	}
	merged.video = map[string]any{"meeting_session_id": sessionIDFromScope(merged), "videos": merged.videos}
	return merged
}

func sessionIDFromScope(scope candidateScope) string {
	seed := strings.TrimSpace(scope.videoID)
	if seed == "" && len(scope.videos) > 0 {
		if id, ok := scope.videos[0]["video_id"].(string); ok {
			seed = id
		}
	}
	return stableSessionID(seed)
}

func stableSessionID(seed string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(seed)))
	return fmt.Sprintf("session-%x", hash[:8])
}

func stableClusterID(name, scope string) string {
	return stableSemanticID("topic", name, scope)
}

func stableWorkItemID(clusterID, title string) string {
	return stableSemanticID("item", clusterID, title)
}

func stableEventID(workItemID, changeType string, refs []EvidenceRef) string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.EvidenceID)
	}
	sort.Strings(ids)
	return stableSemanticID("event", workItemID, changeType, strings.Join(ids, ","))
}

func stableDecisionID(workItemID, content string, refs []EvidenceRef) string {
	return stableSemanticID("decision", workItemID, content, evidenceKey(refs))
}

func stableTodoID(workItemID, content string, refs []EvidenceRef) string {
	return stableSemanticID("todo", workItemID, content, evidenceKey(refs))
}

func evidenceKey(refs []EvidenceRef) string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.EvidenceID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func stableSemanticID(prefix string, values ...string) string {
	seed := make([]string, 0, len(values))
	for _, value := range values {
		seed = append(seed, strings.ToLower(strings.TrimSpace(value)))
	}
	hash := sha256.Sum256([]byte(strings.Join(seed, "\x1f")))
	return fmt.Sprintf("%s-%x", prefix, hash[:8])
}

func sameSessionByContent(previous, current candidateScope) bool {
	if strings.TrimSpace(previous.contentText) == "" || strings.TrimSpace(current.contentText) == "" {
		return false
	}
	if sessionContinuationPattern.MatchString(current.openingText) || sessionContinuationPattern.MatchString(previous.closingText) {
		return true
	}
	boundary := tokenOverlap(previous.closingText, current.openingText)
	whole := tokenOverlap(previous.contentText, current.contentText)
	return boundary >= 0.25 && whole >= 0.15
}

func hasSessionEvidenceGap(previous, current candidateScope) bool {
	return strings.TrimSpace(previous.contentText) != "" && strings.TrimSpace(current.contentText) != ""
}

func tokenOverlap(left, right string) float64 {
	a, b := contentTokens(left), contentTokens(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for token := range a {
		if _, ok := b[token]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(a)+len(b)-shared)
}

func contentTokens(value string) map[string]struct{} {
	value = strings.ToLower(strings.TrimSpace(value))
	result := map[string]struct{}{}
	word := strings.Builder{}
	flush := func() {
		if word.Len() >= 2 {
			result[word.String()] = struct{}{}
		}
		word.Reset()
	}
	runes := []rune(value)
	for i, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if r < 128 {
				word.WriteRune(r)
				continue
			}
			flush()
			if i+1 < len(runes) && unicode.IsLetter(runes[i+1]) {
				result[string([]rune{r, runes[i+1]})] = struct{}{}
			}
			continue
		}
		flush()
	}
	flush()
	return result
}

func appendUniqueStrings(existing []string, values ...string) []string {
	seen := map[string]struct{}{}
	for _, value := range existing {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		existing = append(existing, value)
	}
	return existing
}
