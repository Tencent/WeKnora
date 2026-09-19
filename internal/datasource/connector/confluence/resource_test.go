package confluence

import "testing"

func TestParseResourceID(t *testing.T) {
	space, err := parseResourceID("86802433")
	if err != nil || space.Kind != resourceSpace || space.SpaceID != "86802433" {
		t.Fatalf("space = %#v, %v", space, err)
	}
	page, err := parseResourceID("page:86802433:123456789")
	if err != nil || page.Kind != resourcePage || page.SpaceID != "86802433" || page.PageID != "123456789" {
		t.Fatalf("page = %#v, %v", page, err)
	}
	for _, id := range []string{"", "page:", "page:100", "page::200", "page:100:", "page:100:200:300", "node:86802433:folder-1", "space:100"} {
		if _, err := parseResourceID(id); err == nil {
			t.Errorf("parseResourceID(%q) succeeded", id)
		}
	}
}
