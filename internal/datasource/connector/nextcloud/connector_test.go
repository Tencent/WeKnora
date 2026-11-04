package nextcloud

import (
	"errors"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
)

func TestConnectorValidationResourcesAncestorsAndBatch(t *testing.T) {
	f := newFakeNextcloud(t)
	f.add("/docs/说明.pdf", "pdf")
	f.add("/docs/archive.zip", "zip")
	c := NewConnector()
	cfg := f.config("/docs")
	if c.Type() != "nextcloud" {
		t.Fatal("incorrect connector type")
	}
	if err := c.Validate(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	resources, err := c.ListResources(t.Context(), cfg, "")
	if err != nil || len(resources) != 1 || resources[0].ExternalID != "/" || !resources[0].HasChildren {
		t.Fatalf("root = %+v, %v", resources, err)
	}
	resources, err = c.ListResources(t.Context(), cfg, "/docs")
	if err != nil || len(resources) != 1 || resources[0].ExternalID != "/docs/说明.pdf" ||
		resources[0].ParentID != "/docs" {
		t.Fatalf("children = %+v, %v", resources, err)
	}
	ancestors, err := c.ResolveResourceAncestors(t.Context(), cfg, []string{"/docs/说明.pdf", "/docs/sub/file.pdf", "/"})
	if err != nil || !slices.Equal(ancestors, []string{"/", "/docs", "/docs/sub"}) {
		t.Fatalf("ancestors = %v, %v", ancestors, err)
	}
	items, err := c.FetchAll(t.Context(), cfg, []string{"/docs/说明.pdf"})
	if err != nil || len(items) != 1 || string(items[0].Content) != "pdf" {
		t.Fatalf("batch = %+v, %v", items, err)
	}
	if _, err := c.FetchAll(t.Context(), nil, nil); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatal(err)
	}
	f.status["PROPFIND:/"] = 403
	if err := c.Validate(t.Context(), cfg); !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Fatal(err)
	}
}

func TestResourceBrowsingDoesNotDependOnSavedSyncFilters(t *testing.T) {
	f := newFakeNextcloud(t)
	f.add("/docs/notes.md", "notes")
	cfg := f.config("/docs")
	cfg.Settings = map[string]any{"include_extensions": ".pdf", "exclude_extensions": ".md"}
	resources, err := NewConnector().ListResources(t.Context(), cfg, "/docs")
	if err != nil || len(resources) != 1 || resources[0].Name != "notes.md" {
		t.Fatalf("picker must still allow selecting files when editing filters: %+v, %v", resources, err)
	}
	items, _, err := NewConnector().FetchIncremental(t.Context(), cfg, nil)
	if err != nil || len(items) != 0 {
		t.Fatalf("sync must still apply filters: %+v, %v", items, err)
	}
}

func TestListingLimitsDuplicateAndDefaultNamespace(t *testing.T) {
	_, c := fakeClient(t)
	root := `<response><href>` + testDAVPrefix + `/</href><propstat><prop><resourcetype><collection/>` +
		`</resourcetype></prop><status>HTTP/1.1 200 OK</status></propstat></response>`
	wrap := func(s string) []byte { return []byte(`<multistatus xmlns="DAV:">` + s + `</multistatus>`) }
	if _, err := c.parseListing(wrap(root), "/", 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.parseListing(wrap(root+root), "/", 1, 3); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := c.parseListing(wrap(root+root), "/", 1, 1); err == nil {
		t.Fatal("listing limit ignored")
	}
	if _, err := c.parseListing(append(wrap(root), []byte("<extra/>")...), "/", 0, 1); err == nil {
		t.Fatal("trailing XML accepted")
	}
}
