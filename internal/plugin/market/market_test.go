package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

const digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestParse(t *testing.T) {
	base, _ := url.Parse("https://market.example.com/v1/index.json")
	doc := `{"schemaVersion":1,"plugins":[
	  {"id":"acme.search","name":{"default":"Acme Search","zh-CN":"Acme 搜索"},"publisher":{"id":"acme"},
	   "icon":"javascript:alert(1)",
	   "versions":[
	     {"version":"1.0.0","url":"pkgs/acme-search-1.0.0.wkp","digest":"` + digestA + `"},
	     {"version":"2.0.0","url":"https://cdn.example.com/acme-search-2.0.0.wkp","digest":"` + digestA + `",
	      "engines":{"weknora":">=9.0.0"}},
	     {"version":"1.2.0","url":"/pkgs/acme-search-1.2.0.wkp","digest":"` + digestA + `"}]},
	  {"id":"acme.old","versions":[{"version":"1.0.0","url":"x.wkp","digest":"` + digestA + `",
	   "engines":{"weknora":"<0.1.0"}}]},
	  {"id":"Bad ID","versions":[{"version":"1.0.0","url":"x.wkp","digest":"` + digestA + `"}]},
	  {"id":"acme.nodigest","versions":[{"version":"1.0.0","url":"x.wkp","digest":"md5:1"}]},
	  {"id":"acme.ftp","versions":[{"version":"1.0.0","url":"ftp://x/y.wkp","digest":"` + digestA + `"}]},
	  {"id":"acme.search","versions":[{"version":"9.0.0","url":"x.wkp","digest":"` + digestA + `"}]}
	]}`
	listings, skipped, err := Parse([]byte(doc), base, "0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(listings) != 2 || len(skipped) != 4 {
		t.Fatalf("listings %d, skipped %v", len(listings), skipped)
	}
	search := listings[1]
	if search.ID != "acme.search" || search.Latest == nil || search.Latest.Version != "1.2.0" ||
		search.Latest.URL != "https://market.example.com/pkgs/acme-search-1.2.0.wkp" {
		t.Fatalf("search = %+v latest %+v", search, search.Latest)
	}
	if search.Versions[0].Version != "2.0.0" || search.Icon != "" {
		t.Fatalf("versions newest first and unsafe icons dropped: %+v", search.Entry)
	}
	if search.Versions[2].URL != "https://market.example.com/v1/pkgs/acme-search-1.0.0.wkp" {
		t.Fatalf("relative URL = %s", search.Versions[2].URL)
	}
	old := listings[0]
	if old.Latest != nil || !strings.Contains(old.Incompatible, "needs WeKnora") || old.Name.Default != "acme.old" {
		t.Fatalf("old = %+v", old)
	}

	if _, _, err := Parse([]byte(`{"schemaVersion":2,"plugins":[]}`), base, "0.5.0"); err == nil {
		t.Fatal("accepted a newer index format")
	}
}

func TestClientFetches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"schemaVersion":1,"plugins":[{"id":"acme.x","versions":[` +
			`{"version":"1.0.0","url":"x.wkp","digest":"` + digestA + `"}]}]}`))
	}))
	defer srv.Close()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	listings, _, err := New(srv.URL+"/index.json", "0.5.0", srv.Client()).List(context.Background())
	if err != nil || len(listings) != 1 || listings[0].Latest.URL != srv.URL+"/x.wkp" {
		t.Fatalf("list = %+v, %v", listings, err)
	}
	if _, _, err := New(srv.URL+"/missing", "0.5.0", srv.Client()).List(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "404") {
		t.Fatalf("missing index: %v", err)
	}
}
