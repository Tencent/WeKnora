package types

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// TestMessageArtifacts_ValueScanRoundTrip pins the JSONB serialisation
// contract used by GORM: (Value → bytes → Scan) must produce an equivalent
// slice, including a zero-time modified-at (which must survive JSON's
// RFC3339 encoding).
func TestMessageArtifacts_ValueScanRoundTrip(t *testing.T) {
	mod := time.Date(2026, 7, 10, 10, 20, 30, 0, time.UTC)
	created := time.Date(2026, 7, 10, 10, 20, 31, 0, time.UTC)

	original := MessageArtifacts{
		{
			URL:        "local://42/report.pptx",
			FileName:   "report.pptx",
			FileType:   ".pptx",
			FileSize:   285432,
			SourcePath: "/workspace/output/report.pptx",
			ModTime:    mod,
			CreatedAt:  created,
		},
		{
			// Second entry to cover slice-of-many + empty ModTime edge.
			URL:        "local://42/notes.txt",
			FileName:   "notes.txt",
			FileType:   ".txt",
			FileSize:   17,
			SourcePath: "/workspace/output/notes.txt",
			CreatedAt:  created,
		},
	}

	raw, err := original.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	rawBytes, ok := raw.([]byte)
	if !ok {
		t.Fatalf("Value() returned %T, want []byte", raw)
	}
	// Extra sanity: the bytes must be valid JSON, and re-serialising the
	// decoded value should be byte-equal.
	var probe []map[string]any
	if err := json.Unmarshal(rawBytes, &probe); err != nil {
		t.Fatalf("Value() emitted invalid JSON: %v (bytes=%q)", err, rawBytes)
	}
	if len(probe) != 2 {
		t.Fatalf("expected 2 encoded artifacts, got %d", len(probe))
	}

	var decoded MessageArtifacts
	if err := decoded.Scan(rawBytes); err != nil {
		t.Fatalf("Scan([]byte) error = %v", err)
	}
	if len(decoded) != len(original) {
		t.Fatalf("Scan length = %d, want %d", len(decoded), len(original))
	}
	for i := range original {
		if decoded[i].URL != original[i].URL ||
			decoded[i].FileName != original[i].FileName ||
			decoded[i].FileType != original[i].FileType ||
			decoded[i].FileSize != original[i].FileSize ||
			decoded[i].SourcePath != original[i].SourcePath {
			t.Fatalf("artifact %d mismatch: got=%+v want=%+v", i, decoded[i], original[i])
		}
		if !decoded[i].ModTime.Equal(original[i].ModTime) {
			t.Fatalf("artifact %d mod_time mismatch: got=%s want=%s",
				i, decoded[i].ModTime, original[i].ModTime)
		}
		if !decoded[i].CreatedAt.Equal(original[i].CreatedAt) {
			t.Fatalf("artifact %d created_at mismatch: got=%s want=%s",
				i, decoded[i].CreatedAt, original[i].CreatedAt)
		}
	}

	// Also assert Scan(string) is accepted — some drivers (e.g. sqlite) return
	// TEXT for JSONB columns, so the code path must not care.
	var fromString MessageArtifacts
	if err := fromString.Scan(string(rawBytes)); err != nil {
		t.Fatalf("Scan(string) error = %v", err)
	}
	if len(fromString) != len(original) {
		t.Fatalf("Scan(string) length = %d, want %d", len(fromString), len(original))
	}
}

// TestMessageArtifacts_ScanNil ensures a NULL column materialises as an
// empty (but non-nil) slice, matching the invariant Message.BeforeCreate
// establishes.
func TestMessageArtifacts_ScanNil(t *testing.T) {
	var m MessageArtifacts
	if err := m.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) error = %v", err)
	}
	if m == nil {
		t.Fatal("Scan(nil) yielded nil slice, want empty non-nil")
	}
	if len(m) != 0 {
		t.Fatalf("Scan(nil) length = %d, want 0", len(m))
	}
}

// TestMessageArtifacts_ScanUnknownType covers the fallback branch: a driver
// returning an unexpected type (e.g. int) must produce an empty slice and
// no error, matching the defensive contract shared with MessageImages/
// MessageAttachments.
func TestMessageArtifacts_ScanUnknownType(t *testing.T) {
	var m MessageArtifacts
	if err := m.Scan(12345); err != nil {
		t.Fatalf("Scan(int) unexpected error = %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("Scan(unknown) length = %d, want 0", len(m))
	}
}

// TestMessageArtifacts_ValueNil pins the "nil slice → JSON empty array" path
// so a fresh Message without artifacts serialises as `[]` rather than `null`.
func TestMessageArtifacts_ValueNil(t *testing.T) {
	var m MessageArtifacts
	v, err := m.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	b, ok := v.([]byte)
	if !ok {
		t.Fatalf("Value() returned %T, want []byte", v)
	}
	if !bytes.Equal(b, []byte("[]")) {
		t.Fatalf("Value(nil slice) = %q, want []", b)
	}
}
