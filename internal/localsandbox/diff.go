package localsandbox

import "github.com/pmezard/go-difflib/difflib"

// unifiedDiff renders before→after for an approval. A diff that cannot be
// rendered falls back to the new content so the approver still sees it.
func unifiedDiff(path string, before, after []byte) string {
	text, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(string(before)), B: difflib.SplitLines(string(after)),
		FromFile: path, ToFile: path, Context: 3,
	})
	if err != nil {
		return string(after)
	}
	return text
}
