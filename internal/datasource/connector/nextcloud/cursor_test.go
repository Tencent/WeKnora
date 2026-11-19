package nextcloud

import (
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestCursorBindingAndSchema(t *testing.T) {
	f := newFakeNextcloud(t)
	f.add("/file.pdf", "pdf")
	cfg := f.config("/")
	_, cur, err := NewConnector().FetchIncremental(t.Context(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Credentials["username"] = "bob"
	_, _, err = NewConnector().FetchIncremental(t.Context(), cfg, cur)
	if !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("account switch = %v", err)
	}
	for _, fields := range []map[string]any{
		{"schema_version": 99},
		{"schema_version": 1, "files": "wrong"},
		{"schema_version": 1, "files": map[string]string{"/../outside": "v1"}},
	} {
		_, err := decodeCursor(&types.SyncCursor{ConnectorCursor: fields})
		if !errors.Is(err, datasource.ErrInvalidConfig) {
			t.Fatalf("invalid cursor accepted: %v", err)
		}
	}
}
