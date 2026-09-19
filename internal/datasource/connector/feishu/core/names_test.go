package core

import "testing"

func TestNestedFileName(t *testing.T) {
	got := NestedFileName("成本构成测算", "image-abc.png")
	want := "成本构成测算/image-abc.png"
	if got != want {
		t.Errorf("NestedFileName = %q, want %q", got, want)
	}
	if ImageRelName("tok", ".jpg") != "image-tok.jpg" {
		t.Errorf("ImageRelName unexpected: %s", ImageRelName("tok", ".jpg"))
	}
	if BoardRelName("wid", "") != "board-wid.png" {
		t.Errorf("BoardRelName default ext: %s", BoardRelName("wid", ""))
	}
}
