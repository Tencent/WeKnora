package core

import "testing"

func TestParseFeishuDocURL_WikiCopylink(t *testing.T) {
	got := ParseFeishuDocURL("https://ruijie.feishu.cn/wiki/P0uPwE6DgiAG44kVoZucLxbSnC6?from=from_copylink")
	if got.Skip || got.RejectReason != "" {
		t.Fatalf("unexpected reject/skip: %+v", got)
	}
	if got.Kind != LinkKindWiki {
		t.Errorf("Kind = %q, want wiki", got.Kind)
	}
	if got.Token != "P0uPwE6DgiAG44kVoZucLxbSnC6" {
		t.Errorf("Token = %q, want node token", got.Token)
	}
}

func TestParseFeishuDocURL_AcceptedKinds(t *testing.T) {
	cases := []struct {
		raw  string
		kind string
		tok  string
	}{
		{"https://xxx.feishu.cn/docx/ABC123", LinkKindDocx, "ABC123"},
		{"https://xxx.larksuite.com/docs/OLDDOC#heading", LinkKindDoc, "OLDDOC"},
		{"https://foo.feishu.cn/sheets/SHT1?from=x", LinkKindSheet, "SHT1"},
		{"https://foo.feishu.cn/base/APP1?table=tbl", LinkKindBitable, "APP1"},
		{"https://foo.feishu.cn/file/FIL1", LinkKindFile, "FIL1"},
		{"https://open.feishu.cn/wiki/wikcnNode", LinkKindWiki, "wikcnNode"},
	}
	for _, c := range cases {
		got := ParseFeishuDocURL(c.raw)
		if got.RejectReason != "" || got.Kind != c.kind || got.Token != c.tok {
			t.Errorf("ParseFeishuDocURL(%q) = kind=%q token=%q reject=%q, want kind=%q token=%q",
				c.raw, got.Kind, got.Token, got.RejectReason, c.kind, c.tok)
		}
	}
}

func TestParseFeishuDocURL_Rejected(t *testing.T) {
	cases := []struct {
		raw    string
		reason string
	}{
		{"https://xxx.feishu.cn/wiki/space/SPC123", RejectWikiSpace},
		{"https://xxx.feishu.cn/drive/folder/FLD123", RejectDriveFolder},
		{"https://xxx.larksuite.com/drive/folder/FLD9?from=1", RejectDriveFolder},
		{"https://example.com/not-feishu", RejectUnrecognized},
		{"not a url", RejectUnrecognized},
		{"https://xxx.feishu.cn/mindnotes/M1", RejectUnrecognized},
		{"https://xxx.feishu.cn/wiki/", RejectUnrecognized},
	}
	for _, c := range cases {
		got := ParseFeishuDocURL(c.raw)
		if got.RejectReason != c.reason {
			t.Errorf("ParseFeishuDocURL(%q) reject=%q, want %q", c.raw, got.RejectReason, c.reason)
		}
	}
}

func TestParseFeishuDocURL_EmptyLineSkipped(t *testing.T) {
	got := ParseFeishuDocURL("  \n")
	if !got.Skip {
		t.Errorf("blank input should skip, got %+v", got)
	}
}

func TestDedupeParsedURLs_QueryAndRepeat(t *testing.T) {
	parsed := []ParsedDocURL{
		ParseFeishuDocURL("https://a.feishu.cn/docx/TOK?from=from_copylink"),
		ParseFeishuDocURL("https://a.feishu.cn/docx/TOK"),
		ParseFeishuDocURL("https://a.feishu.cn/wiki/NODE1"),
		ParseFeishuDocURL(""),
		ParseFeishuDocURL("https://a.feishu.cn/wiki/NODE1?from=x"),
	}
	unique, n := DedupeParsedURLs(parsed)
	if n != 2 {
		t.Errorf("duplicateCount = %d, want 2", n)
	}
	if len(unique) != 2 {
		t.Fatalf("unique len = %d, want 2", len(unique))
	}
	if unique[0].Token != "TOK" || unique[1].Token != "NODE1" {
		t.Errorf("order/tokens = %+v", unique)
	}
}

func TestLinkResourceID_RoundTrip(t *testing.T) {
	id := LinkResourceID("docx", "objABC")
	if id != "docx:objABC" {
		t.Errorf("id = %q", id)
	}
	typ, tok := ParseLinkResourceID(id)
	if typ != "docx" || tok != "objABC" {
		t.Errorf("parse = %q %q", typ, tok)
	}
}

func TestExtractLinkURLs(t *testing.T) {
	got := ExtractLinkURLs(map[string]interface{}{
		"urls": []interface{}{" https://a.feishu.cn/docx/T1 ", "https://a.feishu.cn/wiki/N1"},
	})
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	got2 := ExtractLinkURLs(map[string]interface{}{
		"urls": "https://a/docx/x\nhttps://a/wiki/y",
	})
	if len(got2) != 2 {
		t.Fatalf("newline split len = %d", len(got2))
	}
}
