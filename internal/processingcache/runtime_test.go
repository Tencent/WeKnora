package processingcache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memory struct {
	sync.Mutex
	rows   map[string]*Record
	fail   bool
	winner []byte
}

func (m *memory) Get(_ context.Context, t uint64, s, g, k string) (*Record, error) {
	m.Lock()
	defer m.Unlock()
	if m.fail {
		return nil, errors.New("offline")
	}
	r := m.rows[identity(t, s, g, k)]
	if r == nil {
		return nil, ErrMiss
	}
	v := *r
	v.Payload = append([]byte(nil), r.Payload...)
	return &v, nil
}

func (m *memory) PutIfAbsent(_ context.Context, r *Record) (*Record, error) {
	m.Lock()
	defer m.Unlock()
	if m.fail {
		return nil, errors.New("offline")
	}
	k := identity(r.TenantID, r.ScopeID, r.Stage, r.Key)
	if v := m.rows[k]; v != nil {
		return v, nil
	}
	v := *r
	v.Payload = append([]byte(nil), r.Payload...)
	if m.winner != nil {
		v.Payload = m.winner
		v.Checksum = digest(v.Payload)
	}
	m.rows[k] = &v
	return &v, nil
}

func (m *memory) Delete(_ context.Context, r *Record) error {
	m.Lock()
	defer m.Unlock()
	k := identity(r.TenantID, r.ScopeID, r.Stage, r.Key)
	if v := m.rows[k]; v != nil && v.Checksum == r.Checksum && v.ExpiresAt.Equal(r.ExpiresAt) {
		delete(m.rows, k)
	}
	return nil
}
func valid(b []byte) error { var s string; return json.Unmarshal(b, &s) }
func TestBatchPersistenceOrderAndIsolation(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	o := Options{ReadEnabled: true, WriteEnabled: true, TTL: time.Hour, MaxBytes: 100, Epoch: "1"}
	calls := 0
	compute := func(_ context.Context, idx []int) ([][]byte, error) {
		calls++
		out := make([][]byte, len(idx))
		for i, n := range idx {
			out[i], _ = json.Marshal([]string{"a", "b", "a"}[n])
		}
		return out, nil
	}
	r, s, e := New(m, o).Batch(context.Background(), 1, "scope", "embed", []any{"a", "b", "a"}, valid, compute)
	if e != nil || calls != 1 || s.Deduplicated != 1 || string(r[1]) != `"b"` {
		t.Fatalf("%q %+v %v", r, s, e)
	}
	r[0][0] = 'x'
	if r[2][0] == 'x' {
		t.Fatal("aliased")
	}
	_, s, e = New(m, o).Batch(context.Background(), 1, "scope", "embed", []any{"a", "b", "a"}, valid, compute)
	if e != nil || s.Hits != 3 || calls != 1 {
		t.Fatalf("restart %+v %v calls=%d", s, e, calls)
	}
	for _, q := range []struct {
		tenant       uint64
		scope, epoch string
	}{{2, "scope", "1"}, {1, "other", "1"}, {1, "scope", "2"}} {
		o.Epoch = q.epoch
		_, s, e = New(m, o).Batch(context.Background(), q.tenant, q.scope, "embed", []any{"a"}, valid, compute)
		if e != nil || s.Misses != 1 {
			t.Fatal("isolation", s, e)
		}
	}
}

func TestFailuresCorruptionWinnerAndValidation(t *testing.T) {
	for _, mode := range []string{"offline", "expired", "checksum", "semantic", "winner", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			m := &memory{rows: map[string]*Record{}}
			o := Options{ReadEnabled: true, WriteEnabled: true, TTL: time.Hour, MaxBytes: 20}
			rt := New(m, o)
			compute := func(context.Context, []int) ([][]byte, error) { return [][]byte{[]byte(`"fresh"`)}, nil }
			if _, _, err := rt.Batch(context.Background(), 1, "s", "g", []any{1}, valid, compute); err != nil {
				t.Fatal(err)
			}
			for _, r := range m.rows {
				switch mode {
				case "expired":
					r.ExpiresAt = time.Now().Add(-time.Second)
				case "checksum":
					r.Payload = []byte(`"bad"`)
				case "semantic":
					r.Payload = []byte(`42`)
					r.Checksum = digest(r.Payload)
				}
			}
			if mode == "offline" {
				m.fail = true
			}
			if mode == "winner" {
				m.rows = map[string]*Record{}
				m.winner = []byte(`"winner"`)
			}
			if mode == "oversize" {
				o.MaxBytes = 2
				rt = New(m, o)
			}
			out, _, err := rt.Batch(context.Background(), 1, "s", "g", []any{1}, valid, compute)
			want := `"fresh"`
			if mode == "winner" {
				want = `"winner"`
			}
			if err != nil || string(out[0]) != want {
				t.Fatalf("%q %v", out, err)
			}
		})
	}
	m := &memory{rows: map[string]*Record{}}
	_, _, err := New(
		m,
		Options{WriteEnabled: true},
	).Batch(context.Background(), 1, "s", "g", []any{1, 2}, valid, func(context.Context, []int) ([][]byte, error) {
		return [][]byte{[]byte(`"ok"`), []byte(`42`)}, nil
	})
	if err == nil || len(m.rows) != 0 {
		t.Fatal("partially persisted invalid batch")
	}
}

func TestWriteOnlyReturnsFreshResultWithoutReplacingStoredWinner(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	ctx := context.Background()
	writer := New(m, Options{WriteEnabled: true})
	calls := 0
	for _, payload := range []string{`"original"`, `"fresh"`} {
		out, stats, err := writer.Batch(ctx, 1, "scope", "stage", []any{"input"}, valid,
			func(context.Context, []int) ([][]byte, error) {
				calls++
				return [][]byte{[]byte(payload)}, nil
			})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || string(out[0]) != payload {
			t.Fatalf("write-only result=%q, want %s", out, payload)
		}
		if stats.Hits != 0 || stats.Misses != 1 || stats.Bypass != 0 {
			t.Fatalf("write-only stats: %+v", stats)
		}
	}
	if calls != 2 {
		t.Fatalf("compute calls=%d, want 2", calls)
	}
	out, stats, err := New(m, Options{ReadEnabled: true, WriteEnabled: true}).Batch(
		ctx, 1, "scope", "stage", []any{"input"}, valid,
		func(context.Context, []int) ([][]byte, error) {
			t.Fatal("read-enabled runtime must reuse the stored winner")
			return nil, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || string(out[0]) != `"original"` || stats.Hits != 1 {
		t.Fatalf("stored result=%q stats=%+v", out, stats)
	}
}

func TestDisabledCancellationAndConcurrent(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	r := New(m, Options{})
	_, s, e := r.Batch(
		context.Background(),
		1,
		"s",
		"g",
		[]any{1, 1},
		valid,
		func(_ context.Context, idx []int) ([][]byte, error) {
			if len(idx) != 2 {
				t.Fatal(idx)
			}
			return [][]byte{[]byte(`"a"`), []byte(`"b"`)}, nil
		},
	)
	if e != nil || s.Bypass != 2 {
		t.Fatal(s, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, e = r.Batch(
		ctx,
		1,
		"s",
		"g",
		[]any{1},
		valid,
		func(context.Context, []int) ([][]byte, error) { t.Fatal("computed after cancel"); return nil, nil },
	)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	r = New(m, Options{ReadEnabled: true, WriteEnabled: true})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, _, err := r.Batch(
				context.Background(),
				1,
				"s",
				"g",
				[]any{1, 1},
				valid,
				func(context.Context, []int) ([][]byte, error) { return [][]byte{[]byte(`"ok"`)}, nil },
			)
			if err != nil || string(out[1]) != `"ok"` {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentBatchesShareComputeAndWaiterCancellation(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	compute := func(context.Context, []int) ([][]byte, error) {
		close(entered)
		<-release
		return [][]byte{[]byte(`"ok"`)}, nil
	}
	go func() { _, _, err := r.Batch(context.Background(), 1, "s", "g", []any{1}, valid, compute); done <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	waitDone := make(chan error, 1)
	go func() {
		_, _, err := r.Batch(ctx, 1, "s", "g", []any{1}, valid, func(context.Context, []int) ([][]byte, error) {
			t.Error("duplicate computation")
			return nil, errors.New("unexpected")
		})
		waitDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-waitDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEmptyPayloadIsAHit(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
	compute := func(context.Context, []int) ([][]byte, error) { return [][]byte{{}}, nil }
	if _, _, err := r.Batch(context.Background(), 1, "s", "g", []any{1}, nil, compute); err != nil {
		t.Fatal(err)
	}
	_, s, e := r.Batch(context.Background(), 1, "s", "g", []any{1}, nil, compute)
	if e != nil || s.Hits != 1 {
		t.Fatal(s, e)
	}
}

func TestKeyPreservesSecretsAndMapOrder(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
	calls := 0
	compute := func(context.Context, []int) ([][]byte, error) { calls++; return [][]byte{[]byte(`"ok"`)}, nil }
	for _, input := range []any{
		map[string]any{"secret": "one", "a": 1},
		map[string]any{"a": 1, "secret": "one"},
		map[string]any{"a": 1, "secret": "two"},
	} {
		if _, _, e := r.Batch(context.Background(), 1, "s", "g", []any{input}, valid, compute); e != nil {
			t.Fatal(e)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	for _, rec := range m.rows {
		if len(rec.Key) != 64 {
			t.Fatal(rec.Key)
		}
	}
	boom := errors.New("compute failed")
	before := len(m.rows)
	_, _, e := r.Batch(
		context.Background(),
		1,
		"s",
		"g",
		[]any{99},
		valid,
		func(context.Context, []int) ([][]byte, error) { return nil, boom },
	)
	if !errors.Is(e, boom) || len(m.rows) != before {
		t.Fatal(e)
	}
}

func TestConcurrentWaiterGetsIndependentSharedResult(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
	entered := make(chan struct{})
	release := make(chan struct{})
	results := make(chan [][]byte, 2)
	var calls atomic.Int32
	compute := func(context.Context, []int) ([][]byte, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return [][]byte{[]byte(`"ok"`)}, nil
	}
	run := func() {
		out, _, err := r.Batch(context.Background(), 1, "s", "g", []any{1}, valid, compute)
		if err != nil {
			t.Error(err)
		}
		results <- out
	}
	go run()
	<-entered
	go run()
	time.Sleep(20 * time.Millisecond)
	close(release)
	a, b := <-results, <-results
	if calls.Load() != 1 {
		t.Fatal("compute calls", calls.Load())
	}
	a[0][0] = 'x'
	if string(b[0]) != `"ok"` {
		t.Fatal("shared caller slice")
	}
}

func TestComputeCancellationAndWrongCardinalityDoNotPersist(t *testing.T) {
	for _, mode := range []string{"cancel", "cardinality"} {
		t.Run(mode, func(t *testing.T) {
			m := &memory{rows: map[string]*Record{}}
			r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, _, err := r.Batch(ctx, 1, "s", "g", []any{1}, valid, func(context.Context, []int) ([][]byte, error) {
				if mode == "cancel" {
					cancel()
					return [][]byte{[]byte(`"ok"`)}, nil
				}
				return nil, nil
			})
			if err == nil || len(m.rows) != 0 {
				t.Fatal("persisted failed computation", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestReadOnlyDoesNotDeleteCorruption(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	compute := func(context.Context, []int) ([][]byte, error) { return [][]byte{[]byte(`"ok"`)}, nil }
	if _, _, err := New(
		m,
		Options{ReadEnabled: true, WriteEnabled: true},
	).Batch(context.Background(), 1, "s", "g", []any{1}, valid, compute); err != nil {
		t.Fatal(err)
	}
	for _, r := range m.rows {
		r.Payload = []byte("broken")
	}
	out, _, err := New(m, Options{ReadEnabled: true}).Batch(context.Background(), 1, "s", "g", []any{1}, valid, compute)
	if err != nil || string(out[0]) != `"ok"` || len(m.rows) != 1 {
		t.Fatal("read-only mutated store", err)
	}
}

func TestBypassCountsOriginalPositionsForCacheFailures(t *testing.T) {
	for _, mode := range []string{"read-error", "write-error", "oversize", "invalid-winner", "miss"} {
		t.Run(mode, func(t *testing.T) {
			m := &memory{rows: map[string]*Record{}}
			opts := Options{ReadEnabled: true, WriteEnabled: true}
			if mode == "read-error" {
				m.fail = true
			}
			if mode == "write-error" {
				m.fail = true
				opts.ReadEnabled = false
			}
			if mode == "oversize" {
				opts.MaxBytes = 2
			}
			if mode == "invalid-winner" {
				m.winner = []byte(`42`)
			}
			_, stats, err := New(
				m,
				opts,
			).Batch(context.Background(), 1, "s", "g", []any{1, 1}, valid, func(
				context.Context, []int,
			) ([][]byte, error) {
				return [][]byte{[]byte(`"ok"`)}, nil
			})
			want := 2
			if mode == "miss" {
				want = 0
			}
			if err != nil || stats.Bypass != want || stats.Misses != 2 || stats.Hits != 0 {
				t.Fatal(stats, err)
			}
		})
	}
}
