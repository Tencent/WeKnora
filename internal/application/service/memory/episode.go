package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// storeEpisode turns one phase-one response into stored state.
//
// The work is idempotent by construction. A conversation has exactly one
// account, and a rerun rewrites it rather than adding a second — so a retried
// task, a debounced double-fire, or a worker that died after the model call all
// converge on the same state instead of leaving the person with three
// overlapping records of the same afternoon.
func (s *Service) storeEpisode(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	segment transcriptSegment,
	previous *types.MemoryEpisode,
	response episodeResponse,
) error {
	// Notes are stored first and independently. They are the user's own
	// explicit request, and an account the model botched — or a summary it
	// decided was empty — must not be able to swallow "记住：我只用中文".
	s.storeEpisodeNotes(ctx, scope, segment, response.Notes)

	summary := types.SanitizeMemoryDocument(response.Summary, types.MemoryEpisodeMaxRunes)
	if summary == "" {
		logger.Infof(ctx, "memory: conversation %s held nothing worth an account", segment.sessionID)
		return nil
	}
	if redacted, changed := types.RedactSensitive(summary); changed {
		if types.IsMostlyRedacted(redacted) {
			logger.Infof(ctx, "memory: account for %s was mostly sensitive material, dropped",
				segment.sessionID)
			return nil
		}
		logger.Infof(ctx, "memory: redacted sensitive material from an account")
		summary = types.SanitizeMemoryDocument(redacted, types.MemoryEpisodeMaxRunes)
	}

	episode := &types.MemoryEpisode{
		SessionID: segment.sessionID,
		Slug:      episodeSlug(response, previous, segment.sessionID),
		Title:     types.SanitizeMemoryEpisodeTitle(response.Title),
		Outcome:   types.NormalizeMemoryOutcome(response.Outcome),
		Summary:   summary,
		Keywords:  types.SanitizeMemoryEpisodeKeywords(response.Keywords),
		ToAt:      segment.end,
	}
	if len(segment.lines) > 0 {
		episode.FromAt = segment.lines[0].at
	}
	if err := s.repo.SaveEpisode(ctx, scope, episode); err != nil {
		return fmt.Errorf("save episode: %w", err)
	}
	logger.Infof(ctx, "memory: filed account %s (%s) for subject %s",
		episode.Slug, episode.Outcome, scope.SubjectID)

	s.storeEpisodeEmbedding(ctx, scope, cfg, episode)
	// Pruned against the workspace's cap rather than the table ceiling, so a
	// workspace that chose to keep less history actually keeps less. The
	// account just filed is the newest, so it is never the one dropped.
	if removed, err := s.repo.PruneEpisodes(ctx, scope, cfg.EffectiveMaxEpisodes()); err != nil {
		logger.Warnf(ctx, "memory: prune episodes failed: %v", err)
	} else if removed > 0 {
		logger.Infof(ctx, "memory: pruned %d least-used accounts", removed)
	}
	return nil
}

// episodeSlug settles on the permanent handle for an account.
//
// An existing account keeps its slug regardless of what the model proposed:
// the digest points at slugs, and a pointer that changes whenever the
// conversation continues is a pointer that is usually stale. Only a new
// account gets to be named, and a model that returned nothing usable is named
// after its conversation rather than rejected — an account nothing can point
// at is still worth having, because search can reach it.
func episodeSlug(
	response episodeResponse, previous *types.MemoryEpisode, sessionID string,
) string {
	if previous != nil && previous.Slug != "" {
		return previous.Slug
	}
	if slug := types.SanitizeMemoryEpisodeSlug(response.Slug); slug != "" {
		return slug
	}
	if slug := types.SanitizeMemoryEpisodeSlug(response.Title); slug != "" {
		return slug
	}
	suffix := sessionID
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return "conversation-" + suffix
}

// storeEpisodeNotes records what the user explicitly asked to be remembered.
//
// Every note has to be something a [user] line actually says. The model is
// asked for verbatim quotes, but "verbatim" is an instruction and not a
// guarantee, and a note is the one thing in this system that gets injected
// into prompts as the user's own words — so a line that does not appear in
// what the user typed is dropped rather than trusted.
func (s *Service) storeEpisodeNotes(
	ctx context.Context, scope interfaces.MemoryScope, segment transcriptSegment, notes []string,
) {
	if len(notes) == 0 {
		return
	}
	for _, note := range notes {
		note = types.SanitizeMemoryNote(note)
		if note == "" {
			continue
		}
		line, ok := findUserLine(segment, note)
		if !ok {
			logger.Infof(ctx, "memory: dropped a note the user never typed")
			continue
		}
		if redacted, changed := types.RedactSensitive(note); changed {
			if types.IsMostlyRedacted(redacted) {
				continue
			}
			note = types.SanitizeMemoryNote(redacted)
		}
		err := s.repo.AddNote(ctx, scope, &types.MemoryNote{
			Content:         note,
			SourceSessionID: line.sessionID,
			SourceMessageID: line.messageID,
		})
		if err != nil {
			logger.Warnf(ctx, "memory: store note failed: %v", err)
			continue
		}
		logger.Infof(ctx, "memory: recorded a verbatim note for subject %s", scope.SubjectID)
	}
}

// findUserLine locates the user message a quote came from.
//
// Containment rather than equality, because a user line holding several
// sentences legitimately yields one of them, and because the sanitizers on
// both sides collapse whitespace differently than the model does. The
// comparison is on the collapsed forms so that a difference in spacing is not
// mistaken for a fabrication.
func findUserLine(segment transcriptSegment, note string) (transcriptLine, bool) {
	needle := strings.ToLower(collapseSpaces(note))
	if needle == "" {
		return transcriptLine{}, false
	}
	for _, line := range segment.lines {
		if line.tier != tierUser {
			continue
		}
		if strings.Contains(strings.ToLower(collapseSpaces(line.content)), needle) {
			return line, true
		}
	}
	return transcriptLine{}, false
}

func collapseSpaces(text string) string {
	return strings.Join(strings.Fields(text), "")
}

// callEpisodeModel makes one phase-one call.
func (s *Service) callEpisodeModel(
	ctx context.Context,
	cfg *types.MemoryConfig,
	payload types.MemoryExtractPayload,
	segment transcriptSegment,
	previous *types.MemoryEpisode,
) (episodeResponse, error) {
	modelID := s.extractionModelID(ctx, cfg, payload)
	if modelID == "" {
		// An error rather than a skip, so the watermark stays put. Skipping
		// here would consume every message the run was given: memory would
		// report success, advance past them, and no model would ever have seen
		// them — which is how an enabled memory feature ends up having learned
		// nothing at all.
		return episodeResponse{}, fmt.Errorf(
			"no chat model available for memory extraction; " +
				"configure one under workspace memory settings")
	}
	chatModel, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil {
		return episodeResponse{}, fmt.Errorf("get extraction model: %w", err)
	}

	instructions := ""
	if cfg != nil {
		instructions = cfg.ExtractInstructions
	}
	userPrompt := buildEpisodePrompt(
		segment, previous, instructions,
		episodeEvidenceBudget(s.modelContextWindow(ctx, modelID)),
	)

	response, err := s.completeEpisode(ctx, chatModel, userPrompt, episodeBudgetTokens)
	if err != nil {
		return episodeResponse{}, err
	}
	if isTruncated(response) {
		logger.Warnf(ctx,
			"memory: account writing hit the token ceiling with %d chars, retrying with %d tokens",
			len(strings.TrimSpace(contentOf(response))), episodeBudgetRetryTokens)
		response, err = s.completeEpisode(ctx, chatModel, userPrompt, episodeBudgetRetryTokens)
		if err != nil {
			return episodeResponse{}, err
		}
		if isTruncated(response) {
			return episodeResponse{}, fmt.Errorf(
				"%w: no usable output within %d tokens; "+
					"if this is a reasoning model, its thinking is consuming the budget",
				errInvalidExtractionOutput, episodeBudgetRetryTokens)
		}
	}

	parsed, err := parseEpisodeResponse(response.Content)
	if err != nil {
		return episodeResponse{}, fmt.Errorf("%w: %v", errInvalidExtractionOutput, err)
	}
	return parsed, nil
}

// modelContextWindow reports what the extraction model declares, or 0 when it
// declares nothing. Best effort: an unknown window means the transcript falls
// back to a conservative budget, which is a smaller account rather than a
// failed one.
func (s *Service) modelContextWindow(ctx context.Context, modelID string) int {
	if modelID == "" || s.modelService == nil {
		return 0
	}
	model, err := s.modelService.GetModelByID(ctx, modelID)
	if err != nil || model == nil {
		return 0
	}
	return model.Parameters.ContextWindow
}

func contentOf(response *types.ChatResponse) string {
	if response == nil {
		return ""
	}
	return response.Content
}

// completeEpisode issues one account-writing call.
//
// Thinking stays off for the same reason the rest of the memory pipeline turns
// it off: the schema is fixed, and on a model that reasons by default the
// reasoning eats the completion budget and returns an empty string. The
// temperature is not zero, unlike the classification calls — this call writes
// prose, and a greedily decoded account of a long conversation degenerates into
// repeated clauses.
func (s *Service) completeEpisode(
	ctx context.Context, chatModel chat.Chat, userPrompt string, budget int,
) (*types.ChatResponse, error) {
	thinking := false
	// Labelled so the call is identifiable where it is observed. Every other
	// model call in the product says what it is for, and the two memory calls
	// said nothing: in a trace they appeared as bare chat.completion beside
	// the agent's own rounds, which is the only place the cost and the prompt
	// of a background rewrite can be inspected.
	ctx = types.WithLLMCallMetadata(ctx, "memory_episode", "")
	response, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "system", Content: episodeSystemPrompt},
		{Role: "user", Content: userPrompt},
	}, &chat.ChatOptions{
		Temperature:         0.2,
		MaxCompletionTokens: budget,
		Thinking:            &thinking,
		Format:              episodeSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("episode model call: %w", err)
	}
	return response, nil
}

// parseEpisodeResponse tolerates the usual model wrappers: fenced blocks and
// prose around the object.
func parseEpisodeResponse(content string) (episodeResponse, error) {
	trimmed := unwrapJSONObject(content)
	if trimmed == "" {
		if strings.TrimSpace(content) == "" {
			return episodeResponse{}, nil
		}
		return episodeResponse{}, fmt.Errorf("no JSON object in response")
	}
	var parsed episodeResponse
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return episodeResponse{}, err
	}
	return parsed, nil
}

// storeEpisodeEmbedding makes an account semantically reachable.
//
// The embedded text is the title, the keywords and the account itself, in that
// order. Leading with the handles matters because embedding models weight
// early tokens more heavily and an account is mostly narrative — a query like
// "上次那个导入失败" needs to match the keyword, not compete with three
// paragraphs of history.
func (s *Service) storeEpisodeEmbedding(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	episode *types.MemoryEpisode,
) {
	if episode == nil {
		return
	}
	modelID, ok := s.embedder(ctx, cfg)
	if !ok {
		return
	}
	vector := s.embedText(ctx, modelID, episodeEmbeddableText(episode), embedWriteTimeout)
	if len(vector) == 0 {
		return
	}
	err := s.repo.UpsertEpisodeEmbedding(ctx, scope, &types.MemoryEpisodeEmbedding{
		EpisodeID: episode.ID,
		ModelID:   modelID,
		Dims:      len(vector),
		Vector:    types.EncodeEmbedding(vector),
	})
	if err != nil {
		logger.Warnf(ctx, "memory: store episode embedding failed: %v", err)
	}
}

// episodeEmbeddableText is what an account is matched on.
func episodeEmbeddableText(episode *types.MemoryEpisode) string {
	var text strings.Builder
	if episode.Title != "" {
		text.WriteString(episode.Title)
		text.WriteString("\n")
	}
	if len(episode.Keywords) > 0 {
		text.WriteString(strings.Join(episode.Keywords, "、"))
		text.WriteString("\n")
	}
	text.WriteString(episode.Summary)
	return text.String()
}

// backfillEpisodeEmbeddings fills in accounts whose vector is missing.
//
// Two ways that happens: the embedding model was unset or failing when the
// account was written, and a rewrite dropped a vector that no longer described
// the text. Both leave an account reachable only by keyword, so this runs on
// the extraction path rather than waiting for the next rewrite of that same
// conversation, which may never come.
func (s *Service) backfillEpisodeEmbeddings(
	ctx context.Context, scope interfaces.MemoryScope, cfg *types.MemoryConfig,
) int {
	modelID, ok := s.embedder(ctx, cfg)
	if !ok {
		return 0
	}
	episodes, err := s.repo.EpisodesMissingEmbeddings(ctx, scope, modelID, backfillPerRun)
	if err != nil {
		logger.Warnf(ctx, "memory: find accounts missing embeddings failed: %v", err)
		return 0
	}
	filled := 0
	for _, episode := range episodes {
		before := time.Now()
		s.storeEpisodeEmbedding(ctx, scope, cfg, episode)
		filled++
		if time.Since(before) > embedWriteTimeout {
			// The model is slow enough that continuing would hold the
			// extraction task open; the rest are picked up next run.
			break
		}
	}
	if moved, err := s.repo.SyncEpisodeVectorColumn(ctx, scope, 0); err != nil {
		logger.Warnf(ctx, "memory: sync episode vector column failed: %v", err)
	} else if moved > 0 {
		logger.Infof(ctx, "memory: moved %d episode vectors into the database's vector type", moved)
	}
	return filled
}
