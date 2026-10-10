package seafile

import (
	"errors"
	"net/url"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
)

func TestConnectorResources(t *testing.T) {
	noWait(t)
	f := newFakeSeafile(t)
	f.addRepo(testRepoID, "Zulu", "repo", false)
	f.addRepo(otherRepoID, "Alpha", "srepo", false)
	f.addRepo("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "Hidden", "repo", true)
	cfg := syncConfig(f)
	c := NewConnector()
	if c.Type() != "seafile" {
		t.Fatalf("Type = %q", c.Type())
	}
	got, err := c.ListResources(t.Context(), cfg, "")
	if err != nil || len(got) != 2 {
		t.Fatalf("libraries = %+v, %v", got, err)
	}
	for i, want := range []struct{ id, name, typ string }{
		{otherRepoID, "Alpha", "srepo"}, {testRepoID, "Zulu", "repo"},
	} {
		r := got[i]
		if r.ExternalID != want.id+":/" || r.Name != want.name ||
			r.Type != "library" || r.Description != want.typ || !r.HasChildren {
			t.Fatalf("library[%d] = %+v", i, r)
		}
	}
	p := "/产品/100% 文档"
	f.addDir(testRepoID, p+"/子目录")
	f.addFile(testRepoID, p+"/说明.pdf", []byte("pdf"), 1)
	f.addFile(testRepoID, p+"/archive.zip", []byte("zip"), 1)
	f.addFile(testRepoID, p+"/page.mdx", []byte("mdx"), 1)
	parent := testRepoID + ":" + p
	got, err = c.ListResources(t.Context(), cfg, parent)
	if err != nil || len(got) != 2 {
		t.Fatalf("children = %+v, %v", got, err)
	}
	wantTypes := map[string]string{"子目录": "directory", "说明.pdf": "file"}
	for _, r := range got {
		typ, ok := wantTypes[r.Name]
		if !ok || r.Type != typ || r.HasChildren != (typ == "directory") ||
			r.ParentID != parent || r.ExternalID != parent+"/"+r.Name {
			t.Fatalf("child = %+v", r)
		}
		delete(wantTypes, r.Name)
	}
	reqs := f.requests("dir:" + p)
	if len(reqs) != 1 || reqs[0].RawQuery != (url.Values{"p": {p}}).Encode() {
		t.Fatalf("directory query was not encoded exactly once: %+v", reqs)
	}
	_, err = c.ListResources(t.Context(), cfg, testRepoID+":/missing")
	if !errors.Is(err, datasource.ErrResourceNotFound) {
		t.Fatalf("missing directory = %v", err)
	}
	f.fail("repos", 403, -1)
	_, err = c.ListResources(t.Context(), cfg, "")
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Fatalf("forbidden libraries = %v", err)
	}
}

func TestConnectorAncestors(t *testing.T) {
	noWait(t)
	f := newFakeSeafile(t)
	c := NewConnector()
	cfg := syncConfig(f)
	got, err := c.ResolveResourceAncestors(t.Context(), cfg, []string{
		testRepoID + ":/a/b/c/d.pdf", testRepoID + ":/a/b/e.pdf",
		testRepoID + ":/a/b/c/d.pdf", testRepoID + ":/",
	})
	want := []string{testRepoID + ":/", testRepoID + ":/a", testRepoID + ":/a/b", testRepoID + ":/a/b/c"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("ancestors = %v, %v; want %v", got, err, want)
	}
	got, err = c.ResolveResourceAncestors(t.Context(), cfg, []string{testRepoID + ":/"})
	if err != nil || len(got) != 0 || len(f.counts()) != 0 {
		t.Fatalf("root ancestors = %v, %v; requests = %v", got, err, f.counts())
	}
}

func TestConnectorValidate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		roots     []string
		encrypted bool
		want      error
	}{
		{"credentials only", nil, false, nil},
		{"selected directory", []string{"/docs"}, false, nil},
		{"two repositories", []string{"/docs", "other"}, false, datasource.ErrInvalidConfig},
		{"encrypted", []string{"/docs"}, true, datasource.ErrInvalidConfig},
		{"unknown repository", []string{"other"}, false, datasource.ErrResourceNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noWait(t)
			f := newFakeSeafile(t)
			f.addRepo(testRepoID, "Library", "repo", tc.encrypted)
			f.addDir(testRepoID, "/docs")
			cfg := syncConfig(f, tc.roots...)
			for i, p := range tc.roots {
				if p == "other" {
					cfg.ResourceIDs[i] = otherRepoID + ":/"
				}
			}
			err := NewConnector().Validate(t.Context(), cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate = %v; want %v", err, tc.want)
			}
			if tc.want == nil && f.count("ping") != 1 {
				t.Fatalf("Validate did not ping: %v", f.counts())
			}
			if tc.encrypted && f.countPrefix("dir:") != 0 {
				t.Fatalf("encrypted repository was traversed: %v", f.counts())
			}
		})
	}
}

func TestConnectorEmptySelection(t *testing.T) {
	noWait(t)
	f := newFakeSeafile(t)
	h := &recorder{}
	_, err := run(t, NewConnector(), syncConfig(f), nil, h, false)
	if !errors.Is(err, datasource.ErrInvalidConfig) || len(f.counts()) != 0 ||
		len(h.items) != 0 || len(h.checkpoints) != 0 {
		t.Fatalf("empty selection = %v; requests = %v; handler = %+v", err, f.counts(), h)
	}
}

func TestConnectorBatchWrappers(t *testing.T) {
	noWait(t)
	f := newFakeSeafile(t)
	f.addRepo(testRepoID, "Library", "repo", false)
	f.addFile(testRepoID, "/docs/a.pdf", []byte("a"), 1)
	f.addFile(testRepoID, "/outside/b.pdf", []byte("b"), 1)
	c := NewConnector()
	cfg := syncConfig(f, "/outside")
	items, err := c.FetchAll(t.Context(), cfg, []string{testRepoID + ":/docs"})
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, items, "/docs/a.pdf")
	cfg = syncConfig(f, "/docs")
	items, cur, err := c.FetchIncremental(t.Context(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, items, "/docs/a.pdf")
	f.touch(testRepoID, "/docs/a.pdf")
	items, cur, err = c.FetchIncremental(t.Context(), cfg, cur)
	if err != nil || len(filesOf(cur)) != 1 {
		t.Fatalf("incremental wrapper = %v, cursor = %+v", err, cur)
	}
	assertPaths(t, items, "/docs/a.pdf")
}
