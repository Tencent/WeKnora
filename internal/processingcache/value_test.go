package processingcache

import (
	"context"
	"errors"
	"testing"
)

func TestValueContract(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
	calls := 0
	validate := func(v []string) error {
		if v == nil {
			return errors.New("null")
		}
		return nil
	}
	compute := func(context.Context) ([]string, error) { calls++; return []string{"ok"}, nil }
	a, e := Value(context.Background(), r, 1, "doc", "stage", "input", validate, compute)
	if e != nil {
		t.Fatal(e)
	}
	a[0] = "mutated"
	b, e := Value(context.Background(), r, 1, "doc", "stage", "input", validate, compute)
	if e != nil || b[0] != "ok" || calls != 1 {
		t.Fatalf("%v %v calls=%d", b, e, calls)
	}
	for _, tenant := range []uint64{0, 0} {
		_, e = Value(context.Background(), r, tenant, "doc", "stage", "input", validate, compute)
		if e != nil {
			t.Fatal(e)
		}
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	_, e = Value(
		context.Background(),
		r,
		1,
		"doc",
		"invalid",
		"input",
		validate,
		func(context.Context) ([]string, error) { return nil, nil },
	)
	if e == nil {
		t.Fatal("accepted null")
	}
	if len(m.rows) != 1 {
		t.Fatal("persisted invalid output")
	}
}

func TestValueSerializationFaultFailsOpen(t *testing.T) {
	r := New(&memory{rows: map[string]*Record{}}, Options{ReadEnabled: true, WriteEnabled: true})
	value, err := Value(
		context.Background(),
		r,
		1,
		"doc",
		"stage",
		"input",
		nil,
		func(context.Context) (chan int, error) { return make(chan int), nil },
	)
	if err != nil || value == nil {
		t.Fatalf("cache serialization changed successful compute: %v", err)
	}
}

func TestValueErrorsAndCancellationDoNotPersist(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
	failure := errors.New("compute")
	_, err := Value(
		context.Background(),
		r,
		1,
		"doc",
		"stage",
		"input",
		nil,
		func(context.Context) (string, error) { return "", failure },
	)
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = Value(
		ctx,
		r,
		1,
		"doc",
		"stage",
		"input",
		nil,
		func(context.Context) (string, error) { cancel(); return "partial", nil },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(m.rows) != 0 {
		t.Fatal("persisted failed/canceled result")
	}
}

func TestValueCacheStoreFaultFailsOpen(t *testing.T) {
	m := &memory{rows: map[string]*Record{}, fail: true}
	r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
	v, err := Value(
		context.Background(),
		r,
		1,
		"doc",
		"stage",
		"input",
		nil,
		func(context.Context) (string, error) { return "ok", nil },
	)
	if err != nil || v != "ok" {
		t.Fatalf("%s %v", v, err)
	}
}

func TestValueDisabledPreservesOriginalValue(t *testing.T) {
	original := []string{"ok"}
	r := New(nil, Options{})
	got, err := Value(
		context.Background(),
		r,
		1,
		"doc",
		"stage",
		"input",
		nil,
		func(context.Context) ([]string, error) { return original, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	got[0] = "same"
	if original[0] != "same" {
		t.Fatal("disabled cache changed original path")
	}
}

type nonRoundTripValue struct{ Value string }

func (v nonRoundTripValue) MarshalJSON() ([]byte, error) { return []byte(`"ok"`), nil }

type changedRoundTripValue string

func (v changedRoundTripValue) MarshalJSON() ([]byte, error) { return []byte(`"changed"`), nil }
func TestValueRoundTripFaultFailsOpen(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
	original := nonRoundTripValue{Value: "original"}
	value, err := Value(
		context.Background(),
		r,
		1,
		"doc",
		"roundtrip",
		"input",
		nil,
		func(context.Context) (nonRoundTripValue, error) { return original, nil },
	)
	if err != nil || value != original {
		t.Fatalf("representation fault changed successful compute: %+v %v", value, err)
	}
	if len(m.rows) != 0 {
		t.Fatal("persisted unusable payload")
	}
}

func TestValueRoundTripValidatorErrorPropagates(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
	rejected := errors.New("business validation")
	validate := func(v changedRoundTripValue) error {
		if v != "original" {
			return rejected
		}
		return nil
	}
	_, err := Value(
		context.Background(),
		r,
		1,
		"doc",
		"roundtrip",
		"input",
		validate,
		func(context.Context) (changedRoundTripValue, error) { return "original", nil },
	)
	if !errors.Is(err, rejected) {
		t.Fatalf("business validator error swallowed: %v", err)
	}
	if len(m.rows) != 0 {
		t.Fatal("persisted invalid payload")
	}
}

func TestValueRoundTripCancellationPropagates(t *testing.T) {
	m := &memory{rows: map[string]*Record{}}
	r := New(m, Options{ReadEnabled: true, WriteEnabled: true})
	ctx, cancel := context.WithCancel(context.Background())
	_, err := Value(
		ctx,
		r,
		1,
		"doc",
		"roundtrip",
		"input",
		nil,
		func(context.Context) (nonRoundTripValue, error) { cancel(); return nonRoundTripValue{}, nil },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation swallowed: %v", err)
	}
	if len(m.rows) != 0 {
		t.Fatal("persisted canceled payload")
	}
}
