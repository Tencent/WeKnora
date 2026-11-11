package whatsapp

import (
	"regexp"
	"strings"
)

// WhatsApp renders its own lightweight markup rather than Markdown:
// *bold*, _italic_, ~strikethrough~, ```code```, "> quote" and "- list".
// The patterns below rewrite the Markdown constructs that would otherwise show
// up as literal symbols.
var (
	mdHeading = regexp.MustCompile(`(?m)^[ \t]*#{1,6}[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	mdBold    = regexp.MustCompile(`\*\*([^*\n]+)\*\*|__([^_\n]+)__`)
	mdStrike  = regexp.MustCompile(`~~([^~\n]+)~~`)
	mdLink    = regexp.MustCompile(`!?\[([^\]\n]*)\]\(([^)\s]+)\)`)
)

// toWhatsAppText converts Markdown to WhatsApp markup. Fenced code blocks are
// left untouched: WhatsApp understands the same triple-backtick fence.
func toWhatsAppText(markdown string) string {
	segments := strings.Split(markdown, "```")
	for i := 0; i < len(segments); i += 2 {
		segments[i] = convertInline(segments[i])
	}
	return strings.TrimSpace(strings.Join(segments, "```"))
}

func convertInline(s string) string {
	// Headings become a bold line. Emphasis markers inside the heading are
	// dropped so the result is not double-wrapped.
	s = mdHeading.ReplaceAllStringFunc(s, func(line string) string {
		title := mdHeading.FindStringSubmatch(line)[1]
		title = strings.TrimSpace(strings.NewReplacer("**", "", "__", "", "*", "").Replace(title))
		if title == "" {
			return ""
		}
		return "*" + title + "*"
	})
	s = mdBold.ReplaceAllString(s, "*$1$2*")
	s = mdStrike.ReplaceAllString(s, "~$1~")
	s = mdLink.ReplaceAllStringFunc(s, func(link string) string {
		m := mdLink.FindStringSubmatch(link)
		label, target := strings.TrimSpace(m[1]), m[2]
		if label == "" || label == target {
			return target
		}
		return label + " (" + target + ")"
	})
	return s
}

// splitText cuts text into chunks of at most limit characters, preferring to
// break on a line boundary so paragraphs and list items stay intact.
func splitText(text string, limit int) []string {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}

	var chunks []string
	for len(runes) > limit {
		cut := limit
		for i := limit; i > limit/2; i-- {
			if runes[i-1] == '\n' {
				cut = i
				break
			}
		}
		if chunk := strings.TrimSpace(string(runes[:cut])); chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = runes[cut:]
	}
	if chunk := strings.TrimSpace(string(runes)); chunk != "" {
		chunks = append(chunks, chunk)
	}
	return chunks
}
