package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// MemoryScope is the resolved (workspace, principal) pair a memory operation
// runs under. It is always derived from the request context so no caller can
// address another user's memory by passing an id.
type MemoryScope struct {
	TenantID  uint64
	SubjectID string
}

func (s MemoryScope) Valid() bool {
	return s.TenantID > 0 && s.SubjectID != ""
}

// MemoryVectorQuery is one semantic lookup over a subject's stored vectors.
type MemoryVectorQuery struct {
	// ModelID pins the vector space. Vectors from another model are skipped
	// rather than scored, because the distance between them is meaningless.
	ModelID string
	// Vector is the embedded query.
	Vector []float32
	// MinScore is the cosine floor below which a match is not a match.
	MinScore float64
	// Limit is how many hits to return, best first.
	Limit int
}

// MemoryRepository is the storage contract for long-term memory. Every method
// takes an explicit scope rather than reading it from ctx, so a background
// worker cannot accidentally operate on whatever the ambient context happens
// to hold.
type MemoryRepository interface {
	// GetSubject returns the memory space, or (nil, nil) when it does not exist.
	GetSubject(ctx context.Context, scope MemoryScope) (*types.MemorySubject, error)
	// EnsureSubject returns the memory space, creating it on first use.
	EnsureSubject(ctx context.Context, scope MemoryScope) (*types.MemorySubject, error)
	// UpdateSubjectEnabled flips the per-user opt out.
	UpdateSubjectEnabled(ctx context.Context, scope MemoryScope, enabled bool) error
	// EnqueuePendingSession records that a session has turns past the cursor
	// and claims the in-flight slot when no run is already scheduled. It
	// returns the subject as it was before the update plus whether this call
	// is the one responsible for enqueuing the task, so a burst of turns
	// produces exactly one task and never a dropped turn.
	EnqueuePendingSession(
		ctx context.Context, scope MemoryScope, sessionID string, inFlightTimeout time.Duration,
	) (*types.MemorySubject, bool, error)
	// ClaimPendingSessions leases a snapshot without removing durable work.
	// A nil batch means no work remains; RetryAt defers a busy lease.
	ClaimPendingSessions(ctx context.Context, scope MemoryScope, fallbackSession, leaseID string, ttl time.Duration) (*types.MemoryExtractionBatch, error)
	// CheckpointExtraction acknowledges a processed segment (or a recorded
	// skip). A concurrent enqueue changes Revision and keeps the session pending.
	CheckpointExtraction(ctx context.Context, scope MemoryScope, leaseID string, session types.MemoryExtractionSession, cursor types.MemoryMessageCursor, drained bool) error
	HasPendingExtraction(ctx context.Context, scope MemoryScope) (bool, error)
	// RecordExtractionFailure returns true after the bounded invalid-output
	// retry budget. It preserves a failure range without storing transcript text.
	RecordExtractionFailure(
		ctx context.Context, scope MemoryScope, leaseID string, session MemoryExtractionFailure,
	) (bool, error)
	FinishExtraction(ctx context.Context, scope MemoryScope, leaseID string) error
	// Empty leaseID only releases a queued task, never a running worker.
	ReleaseExtractionSlot(ctx context.Context, scope MemoryScope, leaseID string) error
	// MarkForcedConsolidated records that this subject asked for a review
	// themselves. Kept on its own clock so the background pass and the button
	// rate limit each other independently.
	MarkForcedConsolidated(ctx context.Context, scope MemoryScope) error

	// --- Documents: episodes, the consolidated digest, verbatim notes ------
	//
	// Memory is stored as documents in two layers. An episode is one account
	// of a stretch of conversation, kept whole and read on demand; the digest
	// is the consolidated profile rewritten from those accounts and injected
	// into a turn. Notes are what the user asked to remember, verbatim.

	// SaveEpisode files one account, replacing any account already under the
	// same slug while preserving its usage counters.
	SaveEpisode(ctx context.Context, scope MemoryScope, episode *types.MemoryEpisode) error
	// GetEpisode returns one account, or (nil, nil) when absent.
	GetEpisode(ctx context.Context, scope MemoryScope, id string) (*types.MemoryEpisode, error)
	// EpisodeBySession returns the account of one conversation, or (nil, nil)
	// before it has been distilled.
	EpisodeBySession(ctx context.Context, scope MemoryScope, sessionID string) (*types.MemoryEpisode, error)
	// EpisodeBySlug resolves a pointer from the digest's index.
	EpisodeBySlug(ctx context.Context, scope MemoryScope, slug string) (*types.MemoryEpisode, error)
	// ListEpisodes pages the memory manager, newest account first.
	ListEpisodes(ctx context.Context, scope MemoryScope, limit, offset int) ([]*types.MemoryEpisode, int64, error)
	// EpisodeKeywordCounts reports how many of a person's accounts mention
	// each retrieval handle, most frequent first. This is the recurrence
	// signal: a keyword several unrelated conversations reached for is
	// something the person keeps coming back to.
	EpisodeKeywordCounts(ctx context.Context, scope MemoryScope, limit int) ([]MemoryKeywordCount, error)
	// SelectEpisodesForDigest returns the accounts one consolidation reads:
	// most used first, dropping any nothing has needed in unusedDays.
	SelectEpisodesForDigest(ctx context.Context, scope MemoryScope, limit, unusedDays int) ([]*types.MemoryEpisode, error)
	// CountEpisodesAwaitingDigest reports how many accounts the live digest
	// has never seen, which is the check that decides whether a consolidation
	// call has anything to do.
	CountEpisodesAwaitingDigest(ctx context.Context, scope MemoryScope) (int64, error)
	// MarkEpisodesConsolidated records which accounts a rewrite read.
	MarkEpisodesConsolidated(ctx context.Context, scope MemoryScope, ids []string, revision int64) error
	// TouchEpisodes records that these accounts were asked for, by the search
	// tool or by someone opening one. use_count is what the digest index and
	// the cap rank by, so only a deliberate read belongs here.
	TouchEpisodes(ctx context.Context, scope MemoryScope, ids []string) error
	// MarkEpisodesRecalled records that these accounts were relevant to a
	// question, moving last_used_at without claiming a read. Recall is this
	// system guessing; counting the guess would rank the store by how often it
	// guessed rather than by what anything needed.
	MarkEpisodesRecalled(ctx context.Context, scope MemoryScope, ids []string) error
	// DeleteEpisode forgets one account, vector included.
	DeleteEpisode(ctx context.Context, scope MemoryScope, id string) error
	// DeleteAllEpisodes clears the scope and reports how many went.
	DeleteAllEpisodes(ctx context.Context, scope MemoryScope) (int64, error)
	// PruneEpisodes enforces the per-subject cap, least-read first. An account
	// no profile has read yet is exempt, since it sorts to the front of a
	// least-read queue and would otherwise be dropped before it could be
	// consolidated. Only the table ceiling overrides that.
	PruneEpisodes(ctx context.Context, scope MemoryScope, keep int) (int64, error)

	// UpsertEpisodeEmbedding stores or replaces the vector for one account.
	UpsertEpisodeEmbedding(ctx context.Context, scope MemoryScope, embedding *types.MemoryEpisodeEmbedding) error
	// EpisodesMissingEmbeddings returns accounts with no usable vector yet.
	EpisodesMissingEmbeddings(ctx context.Context, scope MemoryScope, modelID string, limit int) ([]*types.MemoryEpisode, error)
	// SearchEpisodesByVector ranks every one of this subject's accounts
	// against one query.
	SearchEpisodesByVector(ctx context.Context, scope MemoryScope, query MemoryVectorQuery) ([]MemoryEpisodeHit, error)
	// SyncEpisodeVectorColumn moves stored episode vectors into the
	// database's own vector type. Returns how many it moved.
	SyncEpisodeVectorColumn(ctx context.Context, scope MemoryScope, limit int) (int, error)

	// GetDigest returns the consolidated profile, or (nil, nil) before the
	// first consolidation.
	GetDigest(ctx context.Context, scope MemoryScope) (*types.MemoryDigest, error)
	// AcquireDigestLease claims the right to rewrite the profile, returning
	// an empty id when another run holds it.
	AcquireDigestLease(ctx context.Context, scope MemoryScope, ttl time.Duration) (string, error)
	// ReleaseDigestLease drops the claim without changing the profile.
	ReleaseDigestLease(ctx context.Context, scope MemoryScope, lease string) error
	// SaveDigest installs a rewritten profile under the lease and returns its
	// revision. Fails with types.ErrMemoryDigestLeaseLost if the lease moved.
	SaveDigest(ctx context.Context, scope MemoryScope, lease, body string) (int64, error)
	// SetDigestEpisodeCount records how many accounts the profile came from.
	SetDigestEpisodeCount(ctx context.Context, scope MemoryScope, count int) error
	// SaveUserDigest installs a profile the person edited themselves and
	// marks it, so the next rewrite respects the edit instead of undoing it.
	SaveUserDigest(ctx context.Context, scope MemoryScope, body string) (int64, error)
	// DeleteDigest clears the profile, leaving the episodes to rebuild from.
	DeleteDigest(ctx context.Context, scope MemoryScope) error

	// AddNote records something the user asked to remember, deduplicated on
	// the exact text.
	AddNote(ctx context.Context, scope MemoryScope, note *types.MemoryNote) error
	// ListNotes returns the user's own words, newest first.
	ListNotes(ctx context.Context, scope MemoryScope, limit int) ([]*types.MemoryNote, error)
	// GetNote returns one note, or (nil, nil) when the id is not this
	// subject's.
	GetNote(ctx context.Context, scope MemoryScope, id string) (*types.MemoryNote, error)
	// CountNotes reports how many notes the subject holds. Counted rather than
	// derived from a bounded list, because the cap is enforced against it and a
	// store that already holds more notes than the cap has to read as full
	// rather than as exactly at the limit.
	CountNotes(ctx context.Context, scope MemoryScope) (int64, error)
	// DeleteNote forgets one explicit instruction.
	DeleteNote(ctx context.Context, scope MemoryScope, id string) error
	// DeleteAllNotes clears the scope and reports how many went.
	DeleteAllNotes(ctx context.Context, scope MemoryScope) (int64, error)
}

// MemoryKeywordCount is one retrieval handle and how many of a person's
// accounts mention it.
type MemoryKeywordCount struct {
	Keyword string
	// Episodes counts accounts, not mentions. A conversation that says the
	// same word ten times is one conversation.
	Episodes int
}

// MemoryEpisodeHit is one matched account and how close it was.
type MemoryEpisodeHit struct {
	Episode *types.MemoryEpisode
	Score   float64
}

// MemoryExtractionFailure identifies an invalid-output range without retaining message text.
type MemoryExtractionFailure struct {
	Session types.MemoryExtractionSession
	End     types.MemoryMessageCursor
	Code    string
}

// MemoryRecall is what one turn pulls in: the consolidated profile, the user's
// standing notes, and any past conversation the question matched.
type MemoryRecall struct {
	// Prompt is the ready-to-append envelope, empty when nothing was recalled.
	Prompt string
	// Used is what produced Prompt, flattened for the chat UI and for the
	// per-message record of what memory contributed to an answer.
	Used types.UsedMemories
	// Episodes and Notes are the same contributions unflattened, for callers
	// that need to act on them — recording a read, linking to an account —
	// rather than only display them.
	Episodes []*types.MemoryEpisode
	Notes    []*types.MemoryNote
}

// MemorySearchResult is what an on-demand lookup into the memory store
// returns.
//
// It is a separate type from MemoryRecall because the two have different
// consumers. Recall produces a prompt envelope for a turn; a search produces
// items for a tool, and that tool has to tell the model *why* it got nothing.
// "This user has memory switched off" and "nothing stored matches" call for
// different answers, and collapsing both into an empty slice would have the
// agent report a blank memory store to someone who simply disabled it.
type MemorySearchResult struct {
	// Episodes are the matched accounts, most relevant first.
	Episodes []*types.MemoryEpisode
	// Available is false when memory is off at any level — workspace, user or
	// the agent handling this request.
	Available bool
}

// RetrievalContext is what memory contributes to retrieval rather than to the
// answer prompt: who this person is and what they keep asking about.
//
// This is the part of memory that earns its keep in a knowledge-base product.
// The same question means different things to different people — "how do I tune
// the segmentation" from someone working on medical imaging should not retrieve
// the same passages as it would from someone working on autonomous driving —
// and that difference has to be applied before retrieval, not after.
//
// Both fields describe the person, never the material. A list of the documents
// someone usually lands on was here once, and feeding it to query rewriting
// edited the question towards those documents before anything had been
// matched, so a question they could not answer came back with them anyway.
type RetrievalContext struct {
	// Background is a compact description of the person, for query rewriting.
	Background string
	// Interests are the subjects they keep returning to.
	Interests []string
}

func (c RetrievalContext) Empty() bool {
	return c.Background == "" && len(c.Interests) == 0
}

// MemoryService is the read/write API for long-term memory.
type MemoryService interface {
	// Recall assembles the memory to inject for one turn. It performs no LLM
	// calls and returns an empty recall (never an error) whenever memory is
	// disabled at any level, so callers can use it unconditionally.
	Recall(ctx context.Context, query string) MemoryRecall
	// SearchMemory ranks the user's stored memories against an arbitrary
	// query, for callers that need to reach past what Recall's per-turn budget
	// admitted. Like Recall it performs no LLM call and never errors.
	SearchMemory(ctx context.Context, query string, limit int) MemorySearchResult
	// MemoryAvailable reports whether this request may read memory at all:
	// the workspace switch, the user's own opt out and the agent's preference
	// combined.
	//
	// Read paths do not need this — Recall and SearchMemory already degrade on
	// their own. It exists for callers that must decide whether to *offer* a
	// memory-backed feature, where the difference between "off" and "empty"
	// has to be settled before anything is built rather than after it is
	// called.
	MemoryAvailable(ctx context.Context) bool
	// RetrievalContextFor returns what memory contributes to retrieval. Like
	// Recall it makes no model call and degrades to an empty value.
	RetrievalContextFor(ctx context.Context) RetrievalContext
	// RememberVerbatim records an explicit "remember this" in the user's own
	// words, effective on the next turn and never reworded by a model.
	RememberVerbatim(ctx context.Context, content, sessionID, messageID string) error
	// ScheduleExtraction debounces and enqueues background distillation for a
	// finished turn. Best effort: failures are logged, never returned.
	ScheduleExtraction(ctx context.Context, sessionID, messageID, chatModelID string)
	// Handle runs the background distillation task.
	Handle(ctx context.Context, task *asynq.Task) error

	// Profile returns the consolidated profile, or (nil, nil) before the first
	// consolidation has written one.
	Profile(ctx context.Context) (*types.MemoryDigest, error)
	// SaveProfile installs a profile the person wrote themselves and returns
	// its revision. The edit is marked, so the next rewrite is told to apply it
	// rather than restore what they removed.
	SaveProfile(ctx context.Context, body string) (int64, error)
	// DeleteProfile clears the profile. The accounts stay, so the next
	// consolidation rebuilds it from what is left.
	DeleteProfile(ctx context.Context) error

	// ListEpisodes pages the accounts of past conversations, newest first.
	ListEpisodes(ctx context.Context, limit, offset int) ([]*types.MemoryEpisode, int64, error)
	// GetEpisode returns one account, and ErrNotFound when the id belongs to
	// nobody or to somebody else.
	GetEpisode(ctx context.Context, id string) (*types.MemoryEpisode, error)
	// DeleteEpisode forgets one account.
	DeleteEpisode(ctx context.Context, id string) error

	// ListNotes returns what the user asked to remember, in their own words.
	ListNotes(ctx context.Context, limit int) ([]*types.MemoryNote, error)
	// AddNote records something the user typed in the memory manager. It takes
	// effect on the very next turn, which is what makes it the honest way to
	// add a memory by hand.
	AddNote(ctx context.Context, content string) (*types.MemoryNote, error)
	// DeleteNote forgets one explicit instruction.
	DeleteNote(ctx context.Context, id string) error

	// Clear forgets everything in the caller's memory space: the profile,
	// every account and every note. Returns how many rows went.
	Clear(ctx context.Context) (int64, error)
	// ConsolidateNow rewrites the caller's consolidated profile immediately,
	// overruling the schedule the background pass waits out. Rate limited on
	// its own clock, because each press is worth a whole-profile model call.
	ConsolidateNow(ctx context.Context) (*types.MemoryConsolidationResult, error)
	// GetSettings returns the effective per-user memory settings.
	GetSettings(ctx context.Context) (*types.MemorySettings, error)
	// SetEnabled flips the per-user opt out.
	SetEnabled(ctx context.Context, enabled bool) error
}
