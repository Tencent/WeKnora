package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistryDistributionBoundary(t *testing.T) {
	builtins, external, community := 0, 0, 0
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
		if entry.Distribution != "builtin" {
			switch entry.Distribution {
			case "external_link":
				external++
			case "community":
				community++
				u, err := url.Parse(entry.InstallSource)
				if err != nil || u.Host != "github.com" || len(u.Path) < 50 {
					t.Fatalf("community source is not pinned: %s", entry.ID)
				}
			default:
				t.Fatalf("unknown distribution: %s", entry.Distribution)
			}
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
	if builtins != 7 || external != 1 || community != 2 {
		t.Fatalf("unexpected catalog: %d builtins, %d external", builtins, external)
	}
	for _, id := range []string{"../pdf", "/pdf", "anthropic-pdf", "tencent-browser-skill"} {
		if _, err := Files(id); err == nil {
			t.Fatalf("accepted non-builtin path %q", id)
		}
	}
}

func TestResourceAccessRejectsStaleDigests(t *testing.T) {
	for _, entry := range List() {
		if entry.Distribution != "builtin" {
			continue
		}
		t.Run(entry.ID, func(t *testing.T) {
			files, err := FilesForEntry(entry)
			require.NoError(t, err)
			archive, err := Archive(entry.ID)
			require.NoError(t, err)
			digest := sha256.Sum256(archive)
			require.True(t, MatchesArchiveDigest(entry.ID, hex.EncodeToString(digest[:])))
			files["SKILL.md"] = []byte("retired development instructions")
			entry.Digest = DigestFiles(files)
			_, err = FilesForEntry(entry)
			require.ErrorContains(t, err, "unsupported skill digest")
			archive, err = archiveFiles(files)
			require.NoError(t, err)
			digest = sha256.Sum256(archive)
			require.False(t, MatchesArchiveDigest(entry.ID, hex.EncodeToString(digest[:])))
		})
	}
}
