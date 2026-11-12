package rss

import (
	"strings"

	"github.com/mmcdole/gofeed"
	feedjson "github.com/mmcdole/gofeed/json"
)

// jsonFeedTranslator retains the distinction between JSON Feed's plain-text
// fields and content_html so plain text bypasses HTML interpretation.
type jsonFeedTranslator struct {
	gofeed.DefaultJSONTranslator
}

func (t *jsonFeedTranslator) Translate(feed interface{}) (*gofeed.Feed, error) {
	translated, err := t.DefaultJSONTranslator.Translate(feed)
	if err != nil {
		return nil, err
	}
	// The default translator validates the type and preserves item order.
	for i, item := range feed.(*feedjson.Feed).Items {
		if strings.TrimSpace(item.ContentHTML) == "" {
			translated.Items[i].Custom = map[string]string{"content_type": "text/plain"}
		}
	}
	return translated, nil
}

// plainTextToMarkdown escapes Markdown punctuation without interpreting tags or
// entities as HTML. JSON Feed content_text and summary are already plain text.
func plainTextToMarkdown(text string) string {
	var out strings.Builder
	for _, char := range strings.TrimSpace(text) {
		if strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", char) {
			out.WriteByte('\\')
		}
		out.WriteRune(char)
	}
	return out.String()
}
