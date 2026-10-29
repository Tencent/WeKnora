package processingcache

import (
	"context"
	"errors"
	"testing"
	"time"
)

type sharedMemory struct {
	*memory
	shared    *Record
	sharedErr error
}

func (m *sharedMemory) GetShared(context.Context, uint64, string, string) (*Record, error) {
	return m.shared, m.sharedErr
}

func TestSharedReuseValidatesSourceAndPreservesWinner(t *testing.T) {
	for _, mode := range []string{"valid", "tenant", "stage", "key", "expired", "checksum", "semantic", "oversize", "failure", "winner", "read-only"} {
		t.Run(mode, func(t *testing.T) {
			m := &sharedMemory{memory: &memory{rows: map[string]*Record{}}}
			opts := Options{ReadEnabled: true, WriteEnabled: true, TTL: time.Hour, MaxBytes: 20}
			rt := New(m, opts)
			compute := func(context.Context, []int) ([][]byte, error) { return [][]byte{[]byte(`"fresh"`)}, nil }
			if _, _, err := rt.Batch(context.Background(), 1, "source", "embed", []any{"text"}, valid, compute); err != nil {
				t.Fatal(err)
			}
			for _, r := range m.rows {
				copy := *r
				m.shared = &copy
			}
			switch mode {
			case "tenant":
				m.shared.TenantID = 2
			case "stage":
				m.shared.Stage = "different"
			case "key":
				m.shared.Key = "different"
			case "expired":
				m.shared.ExpiresAt = time.Now().Add(-time.Second)
			case "checksum":
				m.shared.Checksum = "broken"
			case "semantic":
				m.shared.Payload = []byte(`nullBROKEN`)
				m.shared.Checksum = digest(m.shared.Payload)
			case "oversize":
				m.shared.Payload = []byte(`"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"`)
				m.shared.Checksum = digest(m.shared.Payload)
			case "failure":
				m.sharedErr = errors.New("offline")
			case "winner":
				m.winner = []byte(`"winner"`)
			case "read-only":
				opts.WriteEnabled = false
				rt = New(m, opts)
			}
			calls := 0
			out, stats, err := rt.BatchShared(context.Background(), 1, "target", "embed", []any{"text"}, valid, func(ctx context.Context, idx []int) ([][]byte, error) { calls++; return compute(ctx, idx) })
			if err != nil {
				t.Fatal(err)
			}
			hit := mode == "valid" || mode == "winner" || mode == "read-only"
			if hit && (calls != 0 || stats.Hits != 1) || !hit && calls != 1 {
				t.Fatalf("calls=%d stats=%+v", calls, stats)
			}
			if mode == "winner" && string(out[0]) != `"winner"` {
				t.Fatal("ignored persistent winner")
			}
			if mode == "read-only" && len(m.rows) != 1 {
				t.Fatal("read-only wrote artifact")
			}
			if mode == "valid" {
				delete(m.rows, identity(1, "source", "embed", m.shared.Key))
				m.sharedErr = ErrMiss
				_, _, err = rt.BatchShared(context.Background(), 1, "target", "embed", []any{"text"}, valid, func(context.Context, []int) ([][]byte, error) { t.Fatal("lost copy after deletion"); return nil, nil })
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
