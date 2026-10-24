package nextcloud

import (
	"path"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestKnowledgeFileNames(t *testing.T) {
	if got := knowledgeFileName("/产品/100% 说明:示例.PDF"); got != "Nextcloud/产品/100% 说明_示例.PDF" {
		t.Fatalf("sanitized filename = %q", got)
	}
	got := knowledgeFileName("/docs/" + strings.Repeat("文", 100) + ".pdf")
	if !utf8.ValidString(got) || path.Ext(got) != ".pdf" || len(path.Base(got)) > 204 {
		t.Fatalf("filename truncation must retain a valid UTF-8 stem and extension: %q", got)
	}
}
