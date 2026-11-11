package im

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

// IM 归档路径曾经另维护一套扩展名名单，和通用导入的单一真相源漂移。
// 用户通过微信发 html 附件时：文件能收到、能解析、当轮问答能读到，
// 但进入绑定知识库那一步被扩展名检查直接 return，且不告诉用户被跳过（issue #5403）。
func TestCanArchiveToKnowledgeBaseAgreesWithImportFileTypes(t *testing.T) {
	// 通用导入支持的格式，IM 归档路径必须全部接受，
	// 否则「附件已读」会被误认为「已入库」。
	for _, ext := range utils.SupportedImportExtensions() {
		if !canArchiveToKnowledgeBase(ext) {
			t.Errorf("IM 归档路径不接受通用导入已支持的扩展名 %q", ext)
		}
	}
}

func TestHTMLAttachmentCanBeArchivedToKnowledgeBase(t *testing.T) {
	// issue 里点名的三种 HTML 家族扩展名
	for _, ext := range []string{"html", "htm", "mhtml"} {
		if !canArchiveToKnowledgeBase(ext) {
			t.Errorf("扩展名 %q 应能归档到知识库", ext)
		}
	}
}

func TestCanArchiveToKnowledgeBaseRejectsUnknown(t *testing.T) {
	// 白名单不能放宽成「什么都收」
	for _, ext := range []string{"exe", "sh", "", "unknownext"} {
		if canArchiveToKnowledgeBase(ext) {
			t.Errorf("扩展名 %q 不应被接受", ext)
		}
	}
}

func TestFileExtensionNormalizesHTML(t *testing.T) {
	cases := map[string]string{
		"im-html-repro.html": "html",
		"REPORT.HTML":        "html",
		"page.htm":           "htm",
		"archive.mhtml":      "mhtml",
		"no-extension":       "",
	}
	for name, want := range cases {
		if got := fileExtension(name); got != want {
			t.Errorf("fileExtension(%q) = %q, 期望 %q", name, got, want)
		}
	}
}
