package utils

import (
	"path/filepath"
	"testing"
)

func TestSafeJoinUnderBase(t *testing.T) {
	base := t.TempDir()
	absBase, err := filepath.Abs(base)
	if err != nil {
		t.Fatal(err)
	}

	got, err := SafeJoinUnderBase(base, "a/b")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(absBase, "a", "b"); got != want {
		t.Fatalf("nested prefix: got %q want %q", got, want)
	}

	got, err = SafeJoinUnderBase(base, "foo/bar/..")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(absBase, "foo"); got != want {
		t.Fatalf("cleaned suffix: got %q want %q", got, want)
	}

	got, err = SafeJoinUnderBase(base, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != absBase {
		t.Fatalf("empty suffix: got %q want %q", got, absBase)
	}

	got, err = SafeJoinUnderBase(base, "/abs")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(absBase, "abs"); got != want {
		t.Fatalf("leading slash stripped: got %q want %q", got, want)
	}

	for _, rel := range []string{"../outside", "a/../../outside", ".."} {
		if _, err := SafeJoinUnderBase(base, rel); err == nil {
			t.Fatalf("expected traversal error for %q", rel)
		}
	}
	if _, err := SafeJoinUnderBase("  ", "a"); err == nil {
		t.Fatal("expected error for empty baseDir")
	}
}
