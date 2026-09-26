package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// adfNode is a node of the Atlassian Document Format, the JSON Jira Cloud
// returns rich text in (descriptions, comments).
type adfNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Marks   []adfMark      `json:"marks"`
	Content []adfNode      `json:"content"`
}

type adfMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// adfToMarkdown renders ADF as Markdown. Unknown nodes keep their text, so
// nothing a new node type holds is lost from search. Plain strings (Jira's
// v2 API, old comments) pass through.
func adfToMarkdown(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var doc adfNode
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var b strings.Builder
	blocks(&b, doc.Content, "")
	return strings.TrimSpace(b.String())
}

// blocks writes block nodes, each line prefixed with indent (list nesting,
// quotes).
func blocks(b *strings.Builder, nodes []adfNode, indent string) {
	for _, n := range nodes {
		switch n.Type {
		case "paragraph":
			line(b, indent, inline(n.Content))
		case "heading":
			level := 1
			if v, ok := n.Attrs["level"].(float64); ok && v >= 1 && v <= 6 {
				level = int(v)
			}
			line(b, indent, strings.Repeat("#", level)+" "+inline(n.Content))
		case "bulletList", "orderedList":
			for i, item := range n.Content {
				marker := "- "
				if n.Type == "orderedList" {
					marker = fmt.Sprintf("%d. ", i+1)
				}
				listItem(b, item, indent, marker)
			}
			b.WriteString("\n")
		case "codeBlock":
			lang, _ := n.Attrs["language"].(string)
			b.WriteString(indent + "```" + lang + "\n")
			for _, l := range strings.Split(plain(n.Content), "\n") {
				b.WriteString(indent + l + "\n")
			}
			b.WriteString(indent + "```\n\n")
		case "blockquote":
			blocks(b, n.Content, indent+"> ")
		case "rule":
			line(b, indent, "---")
		case "table":
			table(b, n, indent)
		case "panel", "expand", "nestedExpand", "layoutSection", "layoutColumn", "bodiedExtension":
			if title, _ := n.Attrs["title"].(string); title != "" {
				line(b, indent, "**"+title+"**")
			}
			blocks(b, n.Content, indent)
		case "mediaSingle", "mediaGroup", "media":
			// Attachments are not synced; say where one was.
			line(b, indent, "[attachment]")
		default:
			if len(n.Content) > 0 {
				blocks(b, n.Content, indent)
			} else if t := inline([]adfNode{n}); t != "" {
				line(b, indent, t)
			}
		}
	}
}

func line(b *strings.Builder, indent, text string) {
	b.WriteString(indent + text + "\n\n")
}

func listItem(b *strings.Builder, item adfNode, indent, marker string) {
	first := true
	for _, child := range item.Content {
		switch child.Type {
		case "paragraph":
			prefix := indent + strings.Repeat(" ", len(marker))
			if first {
				prefix = indent + marker
			}
			b.WriteString(prefix + inline(child.Content) + "\n")
		default:
			// Nested lists and other blocks go one level in.
			var sub strings.Builder
			blocks(&sub, []adfNode{child}, indent+"  ")
			b.WriteString(strings.TrimRight(sub.String(), "\n") + "\n")
		}
		first = false
	}
}

func table(b *strings.Builder, n adfNode, indent string) {
	for i, row := range n.Content {
		cells := make([]string, 0, len(row.Content))
		for _, cell := range row.Content {
			var parts []string
			for _, p := range cell.Content {
				if t := strings.TrimSpace(inline(p.Content)); t != "" {
					parts = append(parts, t)
				}
			}
			cells = append(cells, strings.ReplaceAll(strings.Join(parts, " "), "|", "\\|"))
		}
		b.WriteString(indent + "| " + strings.Join(cells, " | ") + " |\n")
		if i == 0 {
			b.WriteString(indent + "|" + strings.Repeat(" --- |", len(cells)) + "\n")
		}
	}
	b.WriteString("\n")
}

// inline renders inline nodes with their marks.
func inline(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Type {
		case "text":
			b.WriteString(marked(n.Text, n.Marks))
		case "hardBreak":
			b.WriteString("  \n")
		case "mention":
			text, _ := n.Attrs["text"].(string)
			if !strings.HasPrefix(text, "@") {
				text = "@" + text
			}
			b.WriteString(text)
		case "emoji":
			text, _ := n.Attrs["text"].(string)
			if text == "" {
				text, _ = n.Attrs["shortName"].(string)
			}
			b.WriteString(text)
		case "inlineCard", "blockCard":
			u, _ := n.Attrs["url"].(string)
			b.WriteString(u)
		case "status":
			text, _ := n.Attrs["text"].(string)
			b.WriteString("[" + text + "]")
		case "date":
			if ts, ok := n.Attrs["timestamp"].(string); ok {
				b.WriteString(ts)
			}
		default:
			b.WriteString(inline(n.Content))
		}
	}
	return b.String()
}

func marked(text string, marks []adfMark) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	for _, m := range marks {
		switch m.Type {
		case "strong":
			text = "**" + text + "**"
		case "em":
			text = "*" + text + "*"
		case "strike":
			text = "~~" + text + "~~"
		case "code":
			text = "`" + text + "`"
		case "link":
			if href, _ := m.Attrs["href"].(string); href != "" {
				text = "[" + text + "](" + href + ")"
			}
		}
	}
	return text
}

// plain is the text of nodes without formatting (code blocks).
func plain(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range nodes {
		if n.Type == "hardBreak" {
			b.WriteString("\n")
		}
		b.WriteString(n.Text)
		b.WriteString(plain(n.Content))
	}
	return b.String()
}
