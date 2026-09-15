package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Memory is stored in three layers, and every method here operates on the
// caller's own memory space. There is no subject id on the wire: the server
// derives identity from the credentials, so a scoped API key cannot inherit
// another person's memories. Full-access API keys or a Bearer session are
// required.
//
// The profile (MemoryDigest) is one document per person that rides in every
// turn. The accounts (MemoryEpisode) hold one narrative per conversation and
// are read on demand. The notes (MemoryNote) are what the person asked to be
// remembered, word for word.

// MemorySettings is the merged workspace + personal memory switch.
type MemorySettings struct {
	WorkspaceEnabled bool   `json:"workspace_enabled"`
	UserEnabled      bool   `json:"user_enabled"`
	Effective        bool   `json:"effective"`
	WriteMode        string `json:"write_mode"`
	// EpisodeCount is how many conversation accounts the caller has stored,
	// and MaxEpisodes the cap past which the least-read ones are dropped.
	EpisodeCount int `json:"episode_count"`
	MaxEpisodes  int `json:"max_episodes"`
}

// MemoryDigest is the consolidated profile injected into every turn.
type MemoryDigest struct {
	// Body is markdown with "## " headings, at most 2400 runes.
	Body     string `json:"body"`
	Revision int64  `json:"revision"`
	// EpisodeCount is how many accounts the current body was rewritten from.
	EpisodeCount int `json:"episode_count"`
	// UserEditedAt is set when the person wrote the body themselves. The next
	// automatic rewrite replaces such a body rather than merging into it, so a
	// caller showing the profile has to be able to say where the text came
	// from.
	UserEditedAt *time.Time `json:"user_edited_at,omitempty"`
	GeneratedAt  *time.Time `json:"generated_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// MemoryEpisode is one conversation's account, as written by the model once
// the conversation went quiet.
type MemoryEpisode struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
	// Slug is the stable handle the profile's index points at. It survives a
	// rewrite, so it stays valid while the account behind it improves.
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// Outcome is one of success / partial / fail / uncertain: what the
	// conversation achieved, judged from the transcript. A reader needs it to
	// know whether an approach recorded here is one to repeat.
	Outcome string `json:"outcome"`
	// Summary is the account itself, as markdown.
	Summary  string    `json:"summary"`
	Keywords []string  `json:"keywords,omitempty"`
	FromAt   time.Time `json:"from_at"`
	ToAt     time.Time `json:"to_at"`
	// UseCount and LastUsedAt are the only retention signal in the system, so
	// they also decide which accounts the per-person cap drops first.
	UseCount   int        `json:"use_count"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// MemoryNote is a sentence the user explicitly asked to remember, stored as
// typed and never rewritten by a model.
type MemoryNote struct {
	ID              string    `json:"id"`
	Content         string    `json:"content"`
	SourceSessionID string    `json:"source_session_id,omitempty"`
	SourceMessageID string    `json:"source_message_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// MemoryConsolidationResult is what POST /memory/consolidate returns.
//
// Only Reviewed and Skipped are ever populated: the endpoint rewrites the
// profile, and Merged / Demoted / Expired describe a per-statement merge that
// does not happen. They are kept so an older client still decodes.
type MemoryConsolidationResult struct {
	Merged  int `json:"merged"`
	Demoted int `json:"demoted"`
	Expired int `json:"expired"`
	// Reviewed is how many accounts the rewrite read.
	Reviewed   int `json:"reviewed"`
	Candidates int `json:"candidates"`
	// Skipped is why the profile was left alone: too_soon, too_few_items or
	// model_unavailable. Empty when the profile was rewritten.
	Skipped string `json:"skipped,omitempty"`
}

// MemoryExportData is the three layers as exported.
type MemoryExportData struct {
	// Profile is nil when no profile has been generated yet.
	Profile  *MemoryDigest    `json:"profile"`
	Episodes []*MemoryEpisode `json:"episodes"`
	Notes    []*MemoryNote    `json:"notes"`
}

// MemoryExport is the JSON snapshot from GET /memory/export.
type MemoryExport struct {
	Total     int64            `json:"total"`
	Truncated bool             `json:"truncated"`
	Data      MemoryExportData `json:"data"`
}

type memorySettingsResponse struct {
	Success bool            `json:"success"`
	Data    *MemorySettings `json:"data"`
}

type memoryProfileResponse struct {
	Success bool          `json:"success"`
	Data    *MemoryDigest `json:"data"`
}

type memoryProfileRevisionResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Revision int64 `json:"revision"`
	} `json:"data"`
}

type memoryEpisodeListResponse struct {
	Success bool             `json:"success"`
	Data    []*MemoryEpisode `json:"data"`
	Total   int64            `json:"total"`
}

type memoryEpisodeResponse struct {
	Success bool           `json:"success"`
	Data    *MemoryEpisode `json:"data"`
}

type memoryNoteListResponse struct {
	Success bool          `json:"success"`
	Data    []*MemoryNote `json:"data"`
}

type memoryNoteResponse struct {
	Success bool        `json:"success"`
	Data    *MemoryNote `json:"data"`
}

type memoryClearResponse struct {
	Success bool  `json:"success"`
	Removed int64 `json:"removed"`
}

type memoryConsolidateResponse struct {
	Success bool                       `json:"success"`
	Data    *MemoryConsolidationResult `json:"data"`
}

// memoryListQuery builds the paging query. Zero values are left off so the
// server applies its own defaults rather than the SDK pinning them.
func memoryListQuery(limit, offset int) url.Values {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	return q
}

// GetMemorySettings returns the merged workspace + personal memory switch.
func (c *Client) GetMemorySettings(ctx context.Context) (*MemorySettings, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/memory/settings", nil, nil)
	if err != nil {
		return nil, err
	}
	var out memorySettingsResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// UpdateMemorySettings turns the caller's own long-term memory on or off. It
// cannot reach the workspace switch, which an admin owns.
func (c *Client) UpdateMemorySettings(ctx context.Context, enabled bool) (*MemorySettings, error) {
	body := map[string]bool{"enabled": enabled}
	resp, err := c.doRequest(ctx, http.MethodPut, "/api/v1/memory/settings", body, nil)
	if err != nil {
		return nil, err
	}
	var out memorySettingsResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetMemoryProfile returns the profile injected into every turn. The result is
// nil, without an error, until enough conversations exist for a first rewrite.
func (c *Client) GetMemoryProfile(ctx context.Context) (*MemoryDigest, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/memory/profile", nil, nil)
	if err != nil {
		return nil, err
	}
	var out memoryProfileResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// SaveMemoryProfile replaces the profile body with the caller's own wording
// and returns the new revision. body is markdown, at most 2400 runes; empty or
// longer is rejected with 400. The edit takes effect on the next turn and is
// replaced whole by the next automatic rewrite.
func (c *Client) SaveMemoryProfile(ctx context.Context, body string) (int64, error) {
	payload := map[string]string{"body": body}
	resp, err := c.doRequest(ctx, http.MethodPut, "/api/v1/memory/profile", payload, nil)
	if err != nil {
		return 0, err
	}
	var out memoryProfileRevisionResponse
	if err := parseResponse(resp, &out); err != nil {
		return 0, err
	}
	return out.Data.Revision, nil
}

// DeleteMemoryProfile clears the profile. The accounts it was written from
// survive, so the next consolidation builds a new one.
func (c *Client) DeleteMemoryProfile(ctx context.Context) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, "/api/v1/memory/profile", nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// ListMemoryEpisodes pages through the caller's conversation accounts, newest
// first. limit defaults to 20 server-side and accepts 1–100; an out-of-range
// value falls back to 20 rather than erroring.
func (c *Client) ListMemoryEpisodes(ctx context.Context, limit, offset int) ([]*MemoryEpisode, int64, error) {
	resp, err := c.doRequest(
		ctx, http.MethodGet, "/api/v1/memory/episodes", nil, memoryListQuery(limit, offset),
	)
	if err != nil {
		return nil, 0, err
	}
	var out memoryEpisodeListResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, 0, err
	}
	return out.Data, out.Total, nil
}

// GetMemoryEpisode reads one conversation's full account.
func (c *Client) GetMemoryEpisode(ctx context.Context, id string) (*MemoryEpisode, error) {
	if id == "" {
		return nil, fmt.Errorf("episode id is required")
	}
	path := "/api/v1/memory/episodes/" + url.PathEscape(id)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var out memoryEpisodeResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// DeleteMemoryEpisode permanently removes one conversation's account. A
// profile that already cites it is not rewritten on the spot, since that costs
// a model call; the next consolidation drops the reference.
func (c *Client) DeleteMemoryEpisode(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("episode id is required")
	}
	path := "/api/v1/memory/episodes/" + url.PathEscape(id)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// ListMemoryNotes returns what the caller asked to remember, newest first.
// limit defaults to 20, which is also the per-person maximum.
func (c *Client) ListMemoryNotes(ctx context.Context, limit int) ([]*MemoryNote, error) {
	resp, err := c.doRequest(
		ctx, http.MethodGet, "/api/v1/memory/notes", nil, memoryListQuery(limit, 0),
	)
	if err != nil {
		return nil, err
	}
	var out memoryNoteListResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// CreateMemoryNote stores one sentence verbatim, in effect from the next turn.
// content is at most 300 runes, and a person may hold 20 notes: past that the
// call fails rather than the oldest note being dropped silently. Submitting a
// note that already exists returns the existing one.
func (c *Client) CreateMemoryNote(ctx context.Context, content string) (*MemoryNote, error) {
	body := map[string]string{"content": content}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/memory/notes", body, nil)
	if err != nil {
		return nil, err
	}
	var out memoryNoteResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// DeleteMemoryNote permanently removes one note.
func (c *Client) DeleteMemoryNote(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("note id is required")
	}
	path := "/api/v1/memory/notes/" + url.PathEscape(id)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// ClearMemory permanently deletes all three layers for the caller and returns
// how many rows went.
func (c *Client) ClearMemory(ctx context.Context) (int64, error) {
	resp, err := c.doRequest(ctx, http.MethodDelete, "/api/v1/memory/all", nil, nil)
	if err != nil {
		return 0, err
	}
	var out memoryClearResponse
	if err := parseResponse(resp, &out); err != nil {
		return 0, err
	}
	return out.Removed, nil
}

// ExportMemory downloads a JSON snapshot of the caller's profile, accounts and
// notes. Truncated is true only if the 20,000-account safety ceiling clipped
// the file, so a partial snapshot cannot be mistaken for a complete one.
func (c *Client) ExportMemory(ctx context.Context) (*MemoryExport, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/memory/export", nil, nil)
	if err != nil {
		return nil, err
	}
	var out MemoryExport
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ConsolidateMemory rewrites the caller's profile from their recent accounts
// immediately, instead of waiting for the background pass. A result with an
// empty Skipped means the profile changed; otherwise Skipped says why it did
// not, and the existing profile is left as it was.
func (c *Client) ConsolidateMemory(ctx context.Context) (*MemoryConsolidationResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/memory/consolidate", nil, nil)
	if err != nil {
		return nil, err
	}
	var out memoryConsolidateResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}
