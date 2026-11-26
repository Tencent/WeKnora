package configcopy

import (
	"reflect"
	"testing"
)

type cloneInner struct {
	Name   string
	Tags   []string
	Scores map[string]int
	Next   *cloneInner
	Fn     func() string
}

type cloneOuter struct {
	Title  string
	Count  int
	Inner  *cloneInner
	Items  []cloneInner
	Lookup map[string]*cloneInner
	Any    any
}

func cloneFixture() cloneOuter {
	return cloneOuter{
		Title: "root",
		Count: 3,
		Inner: &cloneInner{
			Name:   "inner",
			Tags:   []string{"a", "b"},
			Scores: map[string]int{"x": 1},
			Next:   &cloneInner{Name: "leaf"},
			Fn:     func() string { return "kept" },
		},
		Items: []cloneInner{
			{Name: "first", Tags: []string{"t1"}},
			{Name: "second"},
		},
		Lookup: map[string]*cloneInner{
			"one": {Name: "one"},
		},
		Any: cloneInner{Name: "boxed"},
	}
}

func TestCloneDeepCopiesNestedData(t *testing.T) {
	orig := cloneFixture()
	got := Clone(orig)

	if got.Title != orig.Title || got.Count != orig.Count {
		t.Fatalf("Clone changed scalar fields: got %+v, want %+v", got, orig)
	}
	// Mutating the clone must not touch the original at any depth.
	got.Inner.Name = "changed"
	got.Inner.Tags[0] = "changed"
	got.Inner.Scores["x"] = 99
	got.Inner.Next.Name = "changed"
	got.Items[0].Name = "changed"
	got.Items[0].Tags[0] = "changed"
	got.Lookup["one"].Name = "changed"
	got.Any = cloneInner{Name: "changed"}

	if orig.Inner.Name != "inner" || orig.Inner.Tags[0] != "a" ||
		orig.Inner.Scores["x"] != 1 || orig.Inner.Next.Name != "leaf" ||
		orig.Items[0].Name != "first" || orig.Items[0].Tags[0] != "t1" ||
		orig.Lookup["one"].Name != "one" ||
		orig.Any.(cloneInner).Name != "boxed" {
		t.Fatalf("Clone shares storage with the original: %+v", orig)
	}
	if got.Inner == orig.Inner {
		t.Fatalf("Clone did not copy the nested pointer")
	}
	if &got.Items[0].Tags[0] == &orig.Items[0].Tags[0] {
		t.Fatalf("Clone shares the slice backing array with the original")
	}
	// The boxed value is a copy, not the original struct.
	if got.Any.(cloneInner).Name != "changed" {
		t.Fatalf("Clone did not copy the interface field: %+v", got.Any)
	}
}

func TestClonePreservesNil(t *testing.T) {
	orig := cloneOuter{}
	got := Clone(orig)
	if got.Inner != nil || got.Items != nil || got.Lookup != nil || got.Any != nil {
		t.Fatalf("Clone turned nil into non-nil: %+v", got)
	}

	var nilMap map[string]int
	if Clone(nilMap) != nil {
		t.Fatalf("Clone of a nil map is not nil")
	}
	var nilSlice []string
	if Clone(nilSlice) != nil {
		t.Fatalf("Clone of a nil slice is not nil")
	}
	var nilPtr *cloneInner
	if Clone(nilPtr) != nil {
		t.Fatalf("Clone of a nil pointer is not nil")
	}
}

func TestCloneKeepsFunctions(t *testing.T) {
	orig := cloneFixture()
	got := Clone(orig)
	if got.Inner.Fn == nil || got.Inner.Fn() != "kept" {
		t.Fatalf("Clone dropped the function value: %+v", got.Inner)
	}
}

func TestCloneScalars(t *testing.T) {
	if Clone(42) != 42 {
		t.Fatalf("Clone changed an int")
	}
	if Clone("hello") != "hello" {
		t.Fatalf("Clone changed a string")
	}
	if Clone(true) != true {
		t.Fatalf("Clone changed a bool")
	}
}

func TestCloneSlice(t *testing.T) {
	orig := []cloneInner{{Name: "a"}, {Name: "b", Tags: []string{"x"}}}
	got := Clone(orig)
	got[0].Name = "changed"
	got[1].Tags[0] = "changed"
	if orig[0].Name != "a" || orig[1].Tags[0] != "x" {
		t.Fatalf("Clone of a slice shares storage: %+v", orig)
	}
}

func TestValuePreservesTypeAndCopies(t *testing.T) {
	orig := map[string]int{"a": 1}
	v := Value(reflect.ValueOf(orig))
	if v.Type() != reflect.TypeOf(orig) {
		t.Fatalf("Value changed the type: got %v, want %v", v.Type(), reflect.TypeOf(orig))
	}
	cloned, ok := v.Interface().(map[string]int)
	if !ok {
		t.Fatalf("Value did not return a map: %v", v)
	}
	cloned["a"] = 99
	if orig["a"] != 1 {
		t.Fatalf("Value shares storage with the original: %+v", orig)
	}
}
