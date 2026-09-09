package skills

import (
	"bytes"
	"net/url"
	"testing"
)

func TestRegistryDistributionBoundary(t *testing.T) {
	builtins, external := 0, 0
	seen := map[string]bool{}
	for _, entry := range List() {
		if seen[entry.ID] {
			t.Fatalf("duplicate id: %s", entry.ID)
		}
		seen[entry.ID] = true
		for _, link := range []string{entry.SourceURL, entry.LicenseURL} {
			u, err := url.Parse(link)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				t.Fatalf("invalid source link: %s", link)
			}
		}
		archive, err := Archive(entry.ID)
		if entry.Distribution == "external_link" {
			external++
			if err == nil || archive != nil {
				t.Fatalf("external entry %s returned executable resources", entry.ID)
			}
			continue
		}
		builtins++
		if err != nil {
			t.Fatal(err)
		}
		files, err := Files(entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{
			"SKILL.md", "LICENSE", "UPSTREAM.md", "requirements.txt", "scripts/weknora_smoke.py",
		} {
			if len(files[required]) == 0 {
				t.Fatalf("%s has no %s", entry.ID, required)
			}
		}
		again, _ := Archive(entry.ID)
		if !bytes.Equal(archive, again) {
			t.Fatalf("non-deterministic archive: %s", entry.ID)
		}
		if !Matches(entry.Name, files) {
			t.Fatal("package fails its own digest")
		}
		files["SKILL.md"] = []byte("modified")
		if Matches(entry.Name, files) {
			t.Fatal("modified instructions accepted as builtin")
		}
	}
	if builtins != 8 || external != 5 {
		t.Fatalf("unexpected catalog: %d builtins, %d external", builtins, external)
	}
	for _, id := range []string{"../pdf", "/pdf", "anthropic-pdf", "tencent-browser-skill"} {
		if _, err := Files(id); err == nil {
			t.Fatalf("accepted non-builtin path %q", id)
		}
	}
}
