package chunker

import "testing"

func TestHeadingHierarchy_LinearNesting(t *testing.T) {
	h := NewHeadingHierarchy()
	h.Observe("# Chapter 1")
	h.Observe("## Section 1.1")
	h.Observe("### Subsection 1.1.1")
	if got := h.Breadcrumb(); got != "Chapter 1 > Section 1.1 > Subsection 1.1.1" {
		t.Errorf("breadcrumb mismatch: %q", got)
	}
}

func TestHeadingHierarchy_PopsDeeperOnSiblingHeading(t *testing.T) {
	h := NewHeadingHierarchy()
	h.Observe("# Chapter 1")
	h.Observe("## Section 1.1")
	h.Observe("### Subsection 1.1.1")
	h.Observe("## Section 1.2") // pops the H3
	if got := h.Breadcrumb(); got != "Chapter 1 > Section 1.2" {
		t.Errorf("breadcrumb after sibling: %q", got)
	}
}

func TestHeadingHierarchy_PopsAllOnNewTopLevel(t *testing.T) {
	h := NewHeadingHierarchy()
	h.Observe("# Chapter 1")
	h.Observe("## Section A")
	h.Observe("### Sub")
	h.Observe("# Chapter 2")
	if got := h.Breadcrumb(); got != "Chapter 2" {
		t.Errorf("breadcrumb after new H1: %q", got)
	}
	if h.Depth() != 1 {
		t.Errorf("depth should be 1, got %d", h.Depth())
	}
}

func TestHeadingHierarchy_NonHeadingIgnored(t *testing.T) {
	h := NewHeadingHierarchy()
	h.Observe("# Title")
	level, txt := h.Observe("just a paragraph")
	if level != 0 || txt != "" {
		t.Errorf("non-heading should not register: %d %q", level, txt)
	}
	if got := h.Breadcrumb(); got != "Title" {
		t.Errorf("breadcrumb after non-heading: %q", got)
	}
}

func TestHeadingHierarchy_BreadcrumbWithHashes(t *testing.T) {
	h := NewHeadingHierarchy()
	h.Observe("# A")
	h.Observe("## B")
	got := h.BreadcrumbWithHashes()
	want := "# A\n## B"
	if got != want {
		t.Errorf("hashes breadcrumb:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestHeadingHierarchy_EmptyState(t *testing.T) {
	h := NewHeadingHierarchy()
	if h.Breadcrumb() != "" || h.BreadcrumbWithHashes() != "" || h.Depth() != 0 {
		t.Error("empty hierarchy should have no breadcrumb / depth=0")
	}
}

func TestHeadingHierarchy_Reset(t *testing.T) {
	h := NewHeadingHierarchy()
	h.Observe("# A")
	h.Observe("## B")
	h.Reset()
	if h.Breadcrumb() != "" || h.Depth() != 0 {
		t.Error("Reset did not clear state")
	}
}

func TestHeadingHierarchy_SkipLevels(t *testing.T) {
	h := NewHeadingHierarchy()
	// Document jumps from H1 directly to H3, skipping H2.
	h.Observe("# Top")
	h.Observe("### Deep")
	if got := h.Breadcrumb(); got != "Top > Deep" {
		t.Errorf("skipped-level breadcrumb: %q", got)
	}
}
