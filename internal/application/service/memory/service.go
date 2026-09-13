package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ErrMemoryDisabled is returned by write operations when memory is off at the
// workspace or user level.
var ErrMemoryDisabled = errors.New("memory: disabled for this scope")

// ErrNotFound is returned when a record id does not exist in the caller's own
// memory space. Scope mismatch and genuine absence deliberately produce the
// same error so an id cannot be probed for existence across users.
var ErrNotFound = errors.New("memory: record not found")

// ErrSensitiveContent means the statement was almost entirely credentials or
// identity numbers, so redacting it left nothing worth remembering.
var ErrSensitiveContent = errors.New("memory: statement was sensitive material")

// ErrContentTooLong means the text exceeded the store's rune budget. Refused
// rather than trimmed: a note or a profile cut off mid-sentence changes what
// it instructs the model to do.
var ErrContentTooLong = errors.New("memory: content too long")

// ErrEmptyContent means nothing survived sanitization.
var ErrEmptyContent = errors.New("memory: empty content")

// ErrNotesFull means the subject already holds MemoryNotesMaxItems notes.
//
// The cap exists because every note rides in every turn, so the twenty-first
// one would push the store past what a system prompt can carry. Adding is
// refused rather than served by dropping the oldest, because the oldest note
// is an instruction the user gave and never withdrew.
var ErrNotesFull = errors.New("memory: note store is full")

// interestCandidateLimit bounds how many of the most-repeated keywords the
// interest query considers. Generous relative to the handful that can survive
// the threshold, because keywords below the cut are still the cheapest way to
// see why something did not qualify.
const interestCandidateLimit = 40

// Service implements interfaces.MemoryService.
type Service struct {
	repo         interfaces.MemoryRepository
	tenantRepo   interfaces.TenantRepository
	messageRepo  interfaces.MessageRepository
	modelService interfaces.ModelService
	enqueuer     interfaces.TaskEnqueuer
	config       *config.Config
}

// NewMemoryService builds the long-term memory service.
func NewMemoryService(
	repo interfaces.MemoryRepository,
	tenantRepo interfaces.TenantRepository,
	messageRepo interfaces.MessageRepository,
	modelService interfaces.ModelService,
	enqueuer interfaces.TaskEnqueuer,
	cfg *config.Config,
) interfaces.MemoryService {
	return &Service{
		repo:         repo,
		tenantRepo:   tenantRepo,
		messageRepo:  messageRepo,
		modelService: modelService,
		enqueuer:     enqueuer,
		config:       cfg,
	}
}

// workspaceConfig loads the workspace memory switch. A missing tenant or an
// unset column yields a zero-value config, which is disabled.
func (s *Service) workspaceConfig(ctx context.Context, tenantID uint64) *types.MemoryConfig {
	tenant, err := s.tenantRepo.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil || tenant.MemoryConfig == nil {
		return &types.MemoryConfig{}
	}
	cfg := *tenant.MemoryConfig
	cfg.Normalize()
	return &cfg
}

// enabledScope resolves the scope and checks every level of the switch. The
// second return value is false whenever memory must not be used, and callers
// on the read path treat that as "no memory" rather than as a failure.
func (s *Service) enabledScope(ctx context.Context) (interfaces.MemoryScope, *types.MemoryConfig, bool) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return scope, nil, false
	}
	cfg := s.workspaceConfig(ctx, scope.TenantID)
	if !cfg.MemoryEnabled() {
		return scope, cfg, false
	}
	if !types.MemoryAllowedForAgent(ctx) {
		return scope, cfg, false
	}
	subject, err := s.repo.GetSubject(ctx, scope)
	if err != nil {
		logger.Warnf(ctx, "memory: load subject failed: %v", err)
		return scope, cfg, false
	}
	// A subject row is created on first write. Its absence means the user has
	// nothing stored yet, which is still "enabled" for the write path.
	if subject != nil && !subject.Enabled {
		return scope, cfg, false
	}
	return scope, cfg, true
}

// Recall assembles the memory to inject for one turn.
//
// Three things go in, and they are three different kinds of claim. The
// consolidated profile is what memory believes about this person, and it rides
// in every turn because that is the layer it was written to be. The user's
// verbatim notes are standing instructions they typed themselves. And when the
// question matches a past conversation, an excerpt of that account comes along
// — enough to know the conversation happened and roughly how it went, with the
// rest reachable through the search tool.
//
// Never calls a chat model and never returns an error: memory is an
// enhancement, so any failure has to degrade into an ordinary answer rather
// than into a failed request.
func (s *Service) Recall(ctx context.Context, query string) interfaces.MemoryRecall {
	recallCtx, recallSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "memory.recall",
		Input: map[string]interface{}{
			"query": langfuse.TruncateRunes(query, recallQueryPreviewRunes),
		},
	})

	scope, cfg, ok := s.enabledScope(recallCtx)
	if !ok {
		reason := s.scopeDisableReason(recallCtx)
		logger.Infof(recallCtx, "memory: recall skipped (%s)", reason)
		recallSpan.Finish(langfuse.SummarizeMemoryRecallOutput(map[string]interface{}{
			"outcome": "disabled",
			"reason":  reason,
		}), nil, nil)
		return interfaces.MemoryRecall{}
	}

	digest, err := s.repo.GetDigest(recallCtx, scope)
	if err != nil {
		logger.Warnf(recallCtx, "memory: load profile for recall failed: %v", err)
	}
	notes, err := s.repo.ListNotes(recallCtx, scope, types.MemoryNotesMaxItems)
	if err != nil {
		logger.Warnf(recallCtx, "memory: load notes for recall failed: %v", err)
	}
	matched, rankTrace := s.matchEpisodes(recallCtx, scope, cfg, query)

	body := ""
	if digest != nil {
		body = digest.Body
	}
	excerpts := make([]string, 0, len(matched))
	for _, episode := range matched {
		excerpts = append(excerpts, renderEpisodeExcerpt(episode))
	}
	prompt := types.WrapMemoryDocumentForPrompt(body, types.MemoryNoteTexts(notes), excerpts)
	if prompt == "" {
		logger.Infof(recallCtx, "memory: recall empty subject=%s", scope.SubjectID)
		recallSpan.Finish(langfuse.SummarizeMemoryRecallOutput(map[string]interface{}{
			"outcome":    "empty",
			"subject_id": scope.SubjectID,
		}), map[string]interface{}{
			"tenant_id": scope.TenantID,
		}, nil)
		return interfaces.MemoryRecall{}
	}

	// An excerpt that rode into a turn was relevant to the question, which is
	// worth recording and is not a read: nothing asked for it, and nothing has
	// said it helped. It moves last_used_at so the account stays inside the
	// digest selection's window, and leaves use_count to the search tool. Off
	// the request path: the answer does not wait on a counter.
	s.markRecalledAsync(recallCtx, scope, matched)

	used := types.UsedMemoriesFromDocuments(digest, notes, matched)
	logger.Infof(recallCtx,
		"memory: recall done subject=%s profile_runes=%d notes=%d episodes=%d mode=%s prompt_runes=%d",
		scope.SubjectID, len([]rune(body)), len(notes), len(matched),
		rankTrace.Mode, len([]rune(prompt)))
	recallSpan.Finish(langfuse.SummarizeMemoryRecallOutput(map[string]interface{}{
		"outcome":       "ok",
		"subject_id":    scope.SubjectID,
		"profile_runes": len([]rune(body)),
		"note_count":    len(notes),
		"episode_count": len(matched),
		"vector_hits":   rankTrace.VectorHits,
		"vector_skip":   rankTrace.VectorSkipReason,
		"ranking_mode":  rankTrace.Mode,
		"prompt_runes":  len([]rune(prompt)),
	}), map[string]interface{}{
		"tenant_id": scope.TenantID,
	}, nil)

	return interfaces.MemoryRecall{Prompt: prompt, Used: used, Episodes: matched, Notes: notes}
}

// renderEpisodeExcerpt is how one matched account appears inside a turn.
func renderEpisodeExcerpt(episode *types.MemoryEpisode) string {
	if episode == nil {
		return ""
	}
	date := ""
	if !episode.ToAt.IsZero() {
		date = episode.ToAt.Format("2006-01-02")
	}
	return fmt.Sprintf("### %s（%s，%s，slug=%s）\n%s",
		episode.Title, date, episode.Outcome, episode.Slug,
		types.MemoryEpisodeExcerpt(episode))
}

// RememberVerbatim records something the user explicitly asked to remember.
//
// Separate from everything else in this file, and stored as the user's own
// words rather than as anything a model produced. Two reasons. It has to take
// effect on the very next turn — a person who types "记住：我只用中文" and is
// then answered in English has been told the feature does not work, whatever
// the background pipeline does twenty minutes later. And it is the one memory
// nobody should paraphrase: the distillation that writes accounts and the
// consolidation that rewrites the profile are both allowed to reword what they
// read, which is exactly wrong for an instruction.
func (s *Service) RememberVerbatim(
	ctx context.Context, content, sessionID, messageID string,
) error {
	scope, _, ok := s.enabledScope(ctx)
	if !ok {
		return ErrMemoryDisabled
	}
	content = types.SanitizeMemoryNote(content)
	if content == "" {
		return nil
	}
	// Redact before storing. A note is injected into the system prompt of
	// every later turn, so a credential that reaches storage is not merely
	// retained, it is re-sent to a model repeatedly.
	if redacted, changed := types.RedactSensitive(content); changed {
		if types.IsMostlyRedacted(redacted) {
			return ErrSensitiveContent
		}
		content = types.SanitizeMemoryNote(redacted)
	}
	if _, err := s.repo.EnsureSubject(ctx, scope); err != nil {
		return fmt.Errorf("ensure memory subject: %w", err)
	}
	return s.repo.AddNote(ctx, scope, &types.MemoryNote{
		Content:         content,
		SourceSessionID: sessionID,
		SourceMessageID: messageID,
	})
}

// ---------------------------------------------------------------------------
// Memory manager
//
// Everything below backs the screen where a person reads and edits what is
// stored about them. Each store is exposed as what it is — the profile that
// rides in every turn, the accounts of past conversations, the instructions
// they typed — so that deleting something in the manager changes the next
// answer. A manager over rows that no turn reads is worse than no manager,
// because it tells the user they are in control when they are not.
// ---------------------------------------------------------------------------

// Profile returns the consolidated profile, or (nil, nil) before the first
// consolidation has written one.
func (s *Service) Profile(ctx context.Context) (*types.MemoryDigest, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.GetDigest(ctx, scope)
}

// SaveProfile installs a profile the person wrote themselves.
//
// The body is not sanitized down to a single line the way a note is: the
// profile is a sectioned document, and its headings are what the injector and
// the retrieval conditioner read sections out of. Over-length is refused
// rather than trimmed, because a document cut mid-section loses the heading
// that gives the rest of it meaning.
func (s *Service) SaveProfile(ctx context.Context, body string) (int64, error) {
	scope, _, ok := s.enabledScope(ctx)
	if !ok {
		return 0, ErrMemoryDisabled
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return 0, ErrEmptyContent
	}
	if len([]rune(body)) > types.MemoryDigestMaxRunes {
		return 0, ErrContentTooLong
	}
	if redacted, changed := types.RedactSensitive(body); changed {
		if types.IsMostlyRedacted(redacted) {
			return 0, ErrSensitiveContent
		}
		body = redacted
	}
	if _, err := s.repo.EnsureSubject(ctx, scope); err != nil {
		return 0, fmt.Errorf("ensure memory subject: %w", err)
	}
	return s.repo.SaveUserDigest(ctx, scope, body)
}

// DeleteProfile clears the profile and leaves the accounts alone, so the next
// consolidation rebuilds it from what the person has actually discussed. This
// is the "that description of me is wrong, start over" action, as distinct
// from Clear.
func (s *Service) DeleteProfile(ctx context.Context) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	return s.repo.DeleteDigest(ctx, scope)
}

// ListEpisodes pages the accounts of past conversations, newest first.
func (s *Service) ListEpisodes(
	ctx context.Context, limit, offset int,
) ([]*types.MemoryEpisode, int64, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListEpisodes(ctx, scope, limit, offset)
}

// GetEpisode returns one account. An id belonging to another subject is
// reported as missing, so the manager cannot be used to discover that somebody
// else's account exists.
func (s *Service) GetEpisode(ctx context.Context, id string) (*types.MemoryEpisode, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	episode, err := s.repo.GetEpisode(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if episode == nil {
		return nil, ErrNotFound
	}
	return episode, nil
}

// DeleteEpisode forgets one account. The profile already built from it is left
// standing: it is a summary of many conversations, and silently rewriting it
// here would take longer than a request may last.
func (s *Service) DeleteEpisode(ctx context.Context, id string) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	episode, err := s.repo.GetEpisode(ctx, scope, id)
	if err != nil {
		return err
	}
	if episode == nil {
		return ErrNotFound
	}
	return s.repo.DeleteEpisode(ctx, scope, id)
}

// ListNotes returns what the user asked to remember, in their own words.
func (s *Service) ListNotes(ctx context.Context, limit int) ([]*types.MemoryNote, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListNotes(ctx, scope, limit)
}

// AddNote records something the person typed in the manager.
//
// Deliberately the same store and the same sanitization as an in-chat "记住：
// ...", so a memory added by hand reaches the next turn's prompt exactly as
// one asked for in conversation does.
func (s *Service) AddNote(ctx context.Context, content string) (*types.MemoryNote, error) {
	scope, _, ok := s.enabledScope(ctx)
	if !ok {
		return nil, ErrMemoryDisabled
	}
	// Length is judged before sanitization, which collapses whitespace and
	// would otherwise let a long paste through as a shorter single line.
	if len([]rune(strings.TrimSpace(content))) > types.MemoryNoteMaxRunes {
		return nil, ErrContentTooLong
	}
	content = types.SanitizeMemoryNote(content)
	if content == "" {
		return nil, ErrEmptyContent
	}
	// Redact before storing. A note is injected into the system prompt of
	// every later turn, so a credential that reaches storage is not merely
	// retained, it is re-sent to a model repeatedly.
	if redacted, changed := types.RedactSensitive(content); changed {
		if types.IsMostlyRedacted(redacted) {
			return nil, ErrSensitiveContent
		}
		content = types.SanitizeMemoryNote(redacted)
	}
	if err := s.checkNoteCapacity(ctx, scope, content); err != nil {
		return nil, err
	}
	if _, err := s.repo.EnsureSubject(ctx, scope); err != nil {
		return nil, fmt.Errorf("ensure memory subject: %w", err)
	}
	note := &types.MemoryNote{Content: content}
	if err := s.repo.AddNote(ctx, scope, note); err != nil {
		return nil, err
	}
	// Re-read rather than return what was sent: the store deduplicates on the
	// text, so the caller has to be told the id and timestamps of the note it
	// actually holds, which may be one written months ago.
	stored, err := s.repo.GetNote(ctx, scope, note.ID)
	if err != nil || stored == nil {
		return note, err
	}
	return stored, nil
}

// checkNoteCapacity refuses a note that would take the subject over the
// injection cap. Re-adding text already stored is allowed through: it
// refreshes a note rather than growing the store.
func (s *Service) checkNoteCapacity(
	ctx context.Context, scope interfaces.MemoryScope, content string,
) error {
	count, err := s.repo.CountNotes(ctx, scope)
	if err != nil {
		return fmt.Errorf("count notes: %w", err)
	}
	if count < int64(types.MemoryNotesMaxItems) {
		return nil
	}
	held, err := s.repo.ListNotes(ctx, scope, types.MemoryNotesMaxItems)
	if err != nil {
		return fmt.Errorf("load notes: %w", err)
	}
	for _, note := range held {
		if note != nil && note.Content == content {
			return nil
		}
	}
	return ErrNotesFull
}

// DeleteNote forgets one explicit instruction.
func (s *Service) DeleteNote(ctx context.Context, id string) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	note, err := s.repo.GetNote(ctx, scope, id)
	if err != nil {
		return err
	}
	if note == nil {
		return ErrNotFound
	}
	return s.repo.DeleteNote(ctx, scope, id)
}

// Clear forgets everything in the caller's memory space.
//
// All three stores go, and the count returned is every row dropped, because
// the person asking for this is asking for the product to know nothing about
// them — a profile left behind would keep describing them in every turn.
func (s *Service) Clear(ctx context.Context) (int64, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return 0, err
	}
	var removed int64
	digest, err := s.repo.GetDigest(ctx, scope)
	if err != nil {
		return 0, err
	}
	if digest != nil {
		if err := s.repo.DeleteDigest(ctx, scope); err != nil {
			return 0, err
		}
		removed++
	}
	episodes, err := s.repo.DeleteAllEpisodes(ctx, scope)
	if err != nil {
		return 0, err
	}
	removed += episodes
	notes, err := s.repo.DeleteAllNotes(ctx, scope)
	if err != nil {
		return 0, err
	}
	removed += notes
	return removed, nil
}

// GetSettings returns the merged view the settings UI renders.
func (s *Service) GetSettings(ctx context.Context) (*types.MemorySettings, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	cfg := s.workspaceConfig(ctx, scope.TenantID)
	settings := &types.MemorySettings{
		WorkspaceEnabled: cfg.MemoryEnabled(),
		UserEnabled:      true,
		WriteMode:        cfg.WriteMode,
		MaxEpisodes:      cfg.EffectiveMaxEpisodes(),
	}
	if settings.WriteMode == "" {
		settings.WriteMode = types.MemoryWriteExplicitOnly
	}
	subject, err := s.repo.GetSubject(ctx, scope)
	if err != nil {
		return nil, err
	}
	if subject != nil {
		settings.UserEnabled = subject.Enabled
	}
	// Counted from the store rather than from a column on the subject, so the
	// figure cannot drift from what the accounts page lists.
	if _, total, err := s.repo.ListEpisodes(ctx, scope, 1, 0); err == nil {
		settings.EpisodeCount = int(total)
	}
	settings.Effective = settings.WorkspaceEnabled && settings.UserEnabled
	return settings, nil
}

// SetEnabled flips the caller's own opt out.
func (s *Service) SetEnabled(ctx context.Context, enabled bool) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	return s.repo.UpdateSubjectEnabled(ctx, scope, enabled)
}

// ---------------------------------------------------------------------------
// Retrieval conditioning
// ---------------------------------------------------------------------------

// retrievalBackgroundRuneBudget bounds what reaches the rewriter. The rewrite
// prompt is small and latency-sensitive; a paragraph of background would both
// slow it down and drown the actual question.
const retrievalBackgroundRuneBudget = 240

// RetrievalContextFor returns what memory contributes to retrieval.
//
// Like Recall this makes no model call: it is two indexed reads plus string
// assembly, because it runs before the first token of every retrieval turn.
func (s *Service) RetrievalContextFor(ctx context.Context) interfaces.RetrievalContext {
	condCtx, condSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "memory.retrieval_context",
	})
	scope, cfg, ok := s.enabledScope(condCtx)
	if !ok || !cfg.RetrievalConditioningEnabled() {
		reason := "disabled"
		if !ok {
			reason = s.scopeDisableReason(condCtx)
		} else if !cfg.RetrievalConditioningEnabled() {
			reason = "retrieval_conditioning_disabled"
		}
		condSpan.Finish(map[string]interface{}{
			"outcome": "skipped",
			"reason":  reason,
		}, nil, nil)
		return interfaces.RetrievalContext{}
	}

	// The profile's 用户画像 section is the background, and the person's
	// promoted interests are the interests. Both come from the document layer
	// now: the profile is where a consolidation put what it concluded about
	// who this person is, and it is derived from every account rather than
	// from whichever statements happened to be stored as profile kind.
	digest, err := s.repo.GetDigest(condCtx, scope)
	if err != nil {
		logger.Warnf(condCtx, "memory: load profile for retrieval context failed: %v", err)
	}
	var (
		background []string
		budget     int
	)
	if digest != nil {
		// Only 用户画像, not the whole profile. What the rewriter needs is the
		// vocabulary of this person's domain; feeding it their preferences
		// about answer style would put "回答简短" into a search query.
		for _, line := range types.MemoryDigestBullets(
			types.MemoryDigestSection(digest.Body, types.MemoryDigestSectionProfile),
		) {
			cost := len([]rune(line)) + 2
			if budget+cost > retrievalBackgroundRuneBudget {
				break
			}
			budget += cost
			background = append(background, line)
		}
	}

	interests := s.recurringInterests(condCtx, scope, cfg)

	// Who is asking, and nothing about which documents they usually land on.
	// That list belonged to a counter that measured which documents the
	// retriever kept picking rather than which ones the person found useful,
	// and spending it here was the worst of the available places: it edits the
	// question towards those documents before anything has been matched, so a
	// question they cannot answer comes back with them anyway.
	retrievalCtx := interfaces.RetrievalContext{
		Background: strings.Join(background, "；"),
		Interests:  interests,
	}
	logger.Infof(condCtx,
		"memory: retrieval context subject=%s background=%d interests=%d",
		scope.SubjectID, len(background), len(interests))
	condSpan.Finish(langfuse.SummarizeRetrievalContextOutput(
		retrievalCtx.Background, retrievalCtx.Interests,
	), map[string]interface{}{
		"tenant_id": scope.TenantID,
	}, nil)
	return retrievalCtx
}

// recurringInterests lists what this person keeps coming back to.
//
// Counted from the keywords the accounts already carry, and a keyword has to
// appear in several separate conversations before it qualifies. One
// conversation about invoicing is a question; four are an interest.
//
// This replaced a subject tracker that resolved model-named topics against
// each other through character deletion, bigram overlap and an adjudicating
// model call. Three layers of guessing about whether two strings mean the same
// thing, to produce the same list of noun phrases this query produces from
// data already on disk. The keywords are exact forms lifted from the accounts,
// so equality here means equality; varied wording undercounts, which delays a
// promotion rather than corrupting a count.
func (s *Service) recurringInterests(
	ctx context.Context, scope interfaces.MemoryScope, cfg *types.MemoryConfig,
) []string {
	counts, err := s.repo.EpisodeKeywordCounts(ctx, scope, interestCandidateLimit)
	if err != nil {
		logger.Warnf(ctx, "memory: load recurring keywords failed: %v", err)
		return nil
	}
	threshold := cfg.EffectiveInterestThreshold()
	interests := make([]string, 0, types.MemoryInterestMaxItems)
	for _, row := range counts {
		if row.Episodes < threshold {
			// Ordered by frequency, so nothing below this can qualify either.
			break
		}
		if keyword := strings.TrimSpace(row.Keyword); keyword != "" {
			interests = append(interests, keyword)
		}
		if len(interests) >= types.MemoryInterestMaxItems {
			break
		}
	}
	return interests
}
