package wiki

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
)

func TestParseModeChangeRefetchesExistingDocument(t *testing.T) {
	nodes := []core.WikiNode{{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"}}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()
	conf := makeConfig(cfg, []string{"space1"})
	conf.Settings = map[string]interface{}{"parse_mode": "export"}
	c := NewConnector(core.RegionFeishu)
	items, cursor, err := c.FetchIncremental(context.Background(), conf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("first sync items=%d", len(items))
	}
	t.Setenv("FEISHU_DOCX_PARSE_MODE", "export")
	for _, mode := range []string{"blocks", "", "blocks", "export"} {
		conf.Settings["parse_mode"] = mode
		items, cursor, err = c.FetchIncremental(context.Background(), conf, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("parse_mode change must refetch the document: got %d items", len(items))
		}
		items, _, err = c.FetchIncremental(context.Background(), conf, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 {
			t.Fatalf("unchanged effective mode must skip document: got %d items", len(items))
		}

	}
}
