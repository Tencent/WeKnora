package ima

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchIncremental_RefreshesMutableNotes(t *testing.T) {
	for _, body := range []string{"updated body", ""} {
		t.Run(body, func(t *testing.T) {
			fake := newFakeIMA(t)
			note := fakeFile{
				MediaID: "note_1", Title: "Runbook", MediaType: mediaTypeNote,
				NotebookID: "777", NoteBody: "old body",
			}
			fake.setKB("kb1", []fakeFile{note})
			connector := NewConnector()
			ctx := context.Background()
			cfg := fake.config("kb1")
			initial, cursor, err := connector.FetchIncremental(ctx, cfg, nil)
			require.NoError(t, err)
			require.Len(t, initial, 1)

			unchanged, cursor, err := connector.FetchIncremental(ctx, cfg, cursor)
			require.NoError(t, err)
			require.Empty(t, unchanged)

			// An edit, unlike replacing an uploaded file, keeps the note's media ID.
			note.NoteBody = body
			fake.setKB("kb1", []fakeFile{note})
			items, cursor, err := connector.FetchIncremental(ctx, cfg, cursor)
			require.NoError(t, err)
			require.Len(t, items, 1, "edited notes must not be acknowledged by media ID alone")
			require.Equal(t, initial[0].ExternalID, items[0].ExternalID)
			expected := body
			if expected == "" {
				expected = "# Runbook\n"
			}
			require.Equal(t, expected, string(items[0].Content))
			require.False(t, items[0].IsDeleted)

			unchanged, cursor, err = connector.FetchIncremental(ctx, cfg, cursor)
			require.NoError(t, err)
			require.Empty(t, unchanged, "unchanged note bodies must not be re-ingested")

			fake.setKB("kb1", nil)
			deleted, _, err := connector.FetchIncremental(ctx, cfg, cursor)
			require.NoError(t, err)
			require.Len(t, deleted, 1)
			require.True(t, deleted[0].IsDeleted)
			require.Equal(t, initial[0].ExternalID, deleted[0].ExternalID)
		})
	}
}

func TestFetchIncremental_RetriesNoteEditsAfterReadFailure(t *testing.T) {
	fake := newFakeIMA(t)
	note := fakeFile{
		MediaID: "note_1", Title: "Runbook", MediaType: mediaTypeNote,
		NotebookID: "777", NoteBody: "old body",
	}
	fake.setKB("kb1", []fakeFile{note})
	connector := NewConnector()
	ctx := context.Background()
	cfg := fake.config("kb1")
	initial, cursor, err := connector.FetchIncremental(ctx, cfg, nil)
	require.NoError(t, err)
	require.Len(t, initial, 1)

	note.NoteBody = "edited body"
	note.NoteFails = true
	fake.setKB("kb1", []fakeFile{note})
	failed, cursor, err := connector.FetchIncremental(ctx, cfg, cursor)
	require.NoError(t, err)
	require.Empty(t, failed, "a failed read must not emit a deletion or an update")

	note.NoteFails = false
	fake.setKB("kb1", []fakeFile{note})
	retried, _, err := connector.FetchIncremental(ctx, cfg, cursor)
	require.NoError(t, err)
	require.Len(t, retried, 1)
	require.Equal(t, "edited body", string(retried[0].Content))
	require.Equal(t, initial[0].ExternalID, retried[0].ExternalID)
}

func TestFetchIncremental_LegacyNoteCursorAndUnchangedRetry(t *testing.T) {
	fake := newFakeIMA(t)
	note := fakeFile{
		MediaID: "note_1", Title: "Runbook", MediaType: mediaTypeNote,
		NotebookID: "777", NoteBody: "old body",
	}
	fake.setKB("kb1", []fakeFile{note})
	connector := NewConnector()
	ctx := context.Background()
	cfg := fake.config("kb1")
	_, cursor, err := connector.FetchIncremental(ctx, cfg, nil)
	require.NoError(t, err)
	// Persisted cursors from earlier versions have no note-body hashes.
	delete(cursor.ConnectorCursor, "kb_note_hashes")
	note.NoteBody = "current body"
	fake.setKB("kb1", []fakeFile{note})
	items, cursor, err := connector.FetchIncremental(ctx, cfg, cursor)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "current body", string(items[0].Content))

	// A failed read must retain the last hash, so recovering without an edit
	// does not redundantly rebuild the indexed document.
	note.NoteFails = true
	fake.setKB("kb1", []fakeFile{note})
	items, cursor, err = connector.FetchIncremental(ctx, cfg, cursor)
	require.NoError(t, err)
	require.Empty(t, items)
	note.NoteFails = false
	fake.setKB("kb1", []fakeFile{note})
	items, cursor, err = connector.FetchIncremental(ctx, cfg, cursor)
	require.NoError(t, err)
	require.Empty(t, items)

	// Replacing a note still emits the new media identity even with equal text.
	note.MediaID = "note_2"
	fake.setKB("kb1", []fakeFile{note})
	items, _, err = connector.FetchIncremental(ctx, cfg, cursor)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.False(t, items[0].IsDeleted)
}
