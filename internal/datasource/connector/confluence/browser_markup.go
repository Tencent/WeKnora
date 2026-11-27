package confluence

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// stripBrowserOnlyMarkup removes body.view markup that only the Confluence web
// UI consumes, so it does not become page text:
//   - the draw.io macro's hidden container with its editor configuration
//     (base64 JSON that also carries author display names);
//   - Jira macro placeholders that the browser later replaces with issue data:
//     "Getting issue details... STATUS" for a single issue, and an empty
//     header-only table with "Loading... Refresh" controls for a JQL table.
//
// A Jira issue key and its link are kept. Pages without these macros are
// returned unchanged.
func stripBrowserOnlyMarkup(content string) string {
	if !strings.Contains(content, "drawio-macro-data-") &&
		!strings.Contains(content, "jira-issue") && !strings.Contains(content, "jira-table") {
		return content
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return content
	}
	removed := 0
	remove := func(selection *goquery.Selection) {
		removed += selection.Length()
		selection.Remove()
	}

	remove(doc.Find(`div[id^="drawio-macro-data-"]`))
	doc.Find("span.jira-issue").Each(func(_ int, issue *goquery.Selection) {
		placeholders := issue.Find(".issue-placeholder")
		if placeholders.Length() == 0 {
			return
		}
		remove(placeholders)
		key := issue.ChildrenFiltered("a.jira-issue-key").First()
		if key.Length() > 0 {
			remove(issue.Contents().NotSelection(key))
		}
	})
	doc.Find("div.jira-table").Each(func(_ int, table *goquery.Selection) {
		remove(table.Find(".refresh-issues-bottom"))
		table.Find(".jira-issues table").Each(func(_ int, issues *goquery.Selection) {
			if issues.Find("td").Length() == 0 {
				remove(issues)
			}
		})
	})
	if removed == 0 {
		return content
	}
	stripped, err := doc.Find("body").Html()
	if err != nil {
		return content
	}
	return stripped
}
