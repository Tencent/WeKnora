package jira

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
)

// issueToMarkdown formats a Jira issue and its comments into structured Markdown.
func issueToMarkdown(iss issue, baseURL string, includeComments bool) (string, error) {
	var b strings.Builder

	summary := strings.TrimSpace(iss.Fields.Summary)
	b.WriteString(fmt.Sprintf("# [%s] %s\n\n", iss.Key, summary))

	// Metadata Table
	b.WriteString("| Field | Value |\n")
	b.WriteString("| :--- | :--- |\n")
	b.WriteString(fmt.Sprintf("| Issue Key | %s |\n", iss.Key))

	if iss.Fields.IssueType.Name != "" {
		b.WriteString(fmt.Sprintf("| Issue Type | %s |\n", escapeTable(iss.Fields.IssueType.Name)))
	}
	if iss.Fields.Status.Name != "" {
		b.WriteString(fmt.Sprintf("| Status | %s |\n", escapeTable(iss.Fields.Status.Name)))
	}
	if iss.Fields.Priority != nil && iss.Fields.Priority.Name != "" {
		b.WriteString(fmt.Sprintf("| Priority | %s |\n", escapeTable(iss.Fields.Priority.Name)))
	}
	if iss.Fields.Resolution != nil && iss.Fields.Resolution.Name != "" {
		b.WriteString(fmt.Sprintf("| Resolution | %s |\n", escapeTable(iss.Fields.Resolution.Name)))
	}
	if iss.Fields.Assignee != nil && iss.Fields.Assignee.DisplayName != "" {
		b.WriteString(fmt.Sprintf("| Assignee | %s |\n", escapeTable(iss.Fields.Assignee.DisplayName)))
	}
	if iss.Fields.Reporter != nil && iss.Fields.Reporter.DisplayName != "" {
		b.WriteString(fmt.Sprintf("| Reporter | %s |\n", escapeTable(iss.Fields.Reporter.DisplayName)))
	}
	if iss.Fields.Created != "" {
		b.WriteString(fmt.Sprintf("| Created | %s |\n", escapeTable(iss.Fields.Created)))
	}
	if iss.Fields.Updated != "" {
		b.WriteString(fmt.Sprintf("| Updated | %s |\n", escapeTable(iss.Fields.Updated)))
	}
	if len(iss.Fields.Components) > 0 {
		names := make([]string, 0, len(iss.Fields.Components))
		for _, c := range iss.Fields.Components {
			if c.Name != "" {
				names = append(names, c.Name)
			}
		}
		if len(names) > 0 {
			b.WriteString(fmt.Sprintf("| Components | %s |\n", escapeTable(strings.Join(names, ", "))))
		}
	}
	if len(iss.Fields.Labels) > 0 {
		b.WriteString(fmt.Sprintf("| Labels | %s |\n", escapeTable(strings.Join(iss.Fields.Labels, ", "))))
	}
	if len(iss.Fields.FixVersions) > 0 {
		versions := make([]string, 0, len(iss.Fields.FixVersions))
		for _, v := range iss.Fields.FixVersions {
			if v.Name != "" {
				versions = append(versions, v.Name)
			}
		}
		if len(versions) > 0 {
			b.WriteString(fmt.Sprintf("| Fix Versions | %s |\n", escapeTable(strings.Join(versions, ", "))))
		}
	}
	if baseURL != "" {
		issueURL := fmt.Sprintf("%s/browse/%s", strings.TrimRight(baseURL, "/"), iss.Key)
		b.WriteString(fmt.Sprintf("| URL | [%s](%s) |\n", iss.Key, issueURL))
	}
	b.WriteString("\n")

	// Description
	b.WriteString("## Description\n\n")
	descMd := extractDescription(iss)
	if strings.TrimSpace(descMd) == "" {
		b.WriteString("*No description provided.*\n\n")
	} else {
		b.WriteString(strings.TrimSpace(descMd))
		b.WriteString("\n\n")
	}

	// Comments
	if includeComments {
		commentsMd := extractComments(iss)
		if commentsMd != "" {
			b.WriteString("## Comments\n\n")
			b.WriteString(commentsMd)
		}
	}

	// Attachments
	if len(iss.Fields.Attachment) > 0 {
		b.WriteString("## Attachments\n\n")
		b.WriteString("| File Name | Size | Uploaded By | Date |\n")
		b.WriteString("| :--- | :--- | :--- | :--- |\n")
		for _, a := range iss.Fields.Attachment {
			author := "Unknown"
			if a.Author != nil && a.Author.DisplayName != "" {
				author = a.Author.DisplayName
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				escapeTable(a.Filename),
				humanFileSize(a.Size),
				escapeTable(author),
				escapeTable(a.Created),
			))
		}
		b.WriteString("\n")

		// Embedded text contents for logs, scripts, and text files
		for _, a := range iss.Fields.Attachment {
			if strings.TrimSpace(a.TextContent) == "" {
				continue
			}
			ext := strings.ToLower(filepath.Ext(a.Filename))
			lang := embeddableTextExts[ext]
			if lang == "" {
				lang = "text"
			}
			b.WriteString(fmt.Sprintf("### 📎 %s (%s)\n\n", escapeTable(a.Filename), humanFileSize(a.Size)))
			b.WriteString("```" + lang + "\n")
			b.WriteString(strings.TrimSpace(a.TextContent))
			b.WriteString("\n```\n\n")
		}
	}

	return b.String(), nil
}

func humanFileSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}

func extractDescription(iss issue) string {
	if iss.RenderedFields != nil && strings.TrimSpace(iss.RenderedFields.Description) != "" {
		if md, err := htmltomd.ConvertString(iss.RenderedFields.Description); err == nil && strings.TrimSpace(md) != "" {
			return md
		}
	}
	if str, ok := iss.Fields.Description.(string); ok && strings.TrimSpace(str) != "" {
		return str
	}
	if md := adfToMarkdown(iss.Fields.Description); strings.TrimSpace(md) != "" {
		return md
	}
	return ""
}

func extractComments(iss issue) string {
	if iss.Fields.Comment == nil || len(iss.Fields.Comment.Comments) == 0 {
		return ""
	}

	renderedMap := make(map[string]string)
	if iss.RenderedFields != nil && iss.RenderedFields.Comment != nil {
		for _, rc := range iss.RenderedFields.Comment.Comments {
			renderedMap[rc.ID] = rc.Body
		}
	}

	var b strings.Builder
	for i, c := range iss.Fields.Comment.Comments {
		author := "Unknown"
		if c.Author != nil && c.Author.DisplayName != "" {
			author = c.Author.DisplayName
		}
		date := c.Created
		if c.Updated != "" && c.Updated != c.Created {
			date = fmt.Sprintf("%s (updated %s)", c.Created, c.Updated)
		}

		b.WriteString(fmt.Sprintf("### Comment %d by %s", i+1, author))
		if date != "" {
			b.WriteString(fmt.Sprintf(" on %s", date))
		}
		b.WriteString("\n\n")

		body := ""
		if renderedHTML, exists := renderedMap[c.ID]; exists && strings.TrimSpace(renderedHTML) != "" {
			if converted, err := htmltomd.ConvertString(renderedHTML); err == nil && strings.TrimSpace(converted) != "" {
				body = converted
			}
		}
		if body == "" {
			if strBody, ok := c.Body.(string); ok {
				body = strBody
			}
		}
		if body == "" {
			body = adfToMarkdown(c.Body)
		}

		if strings.TrimSpace(body) == "" {
			b.WriteString("*Empty comment.*\n\n")
		} else {
			b.WriteString(strings.TrimSpace(body))
			b.WriteString("\n\n")
		}
	}

	return b.String()
}

// adfToMarkdown converts Jira Cloud's Atlassian Document Format to Markdown.
// Jira v3 returns descriptions and comments as ADF objects rather than HTML.
func adfToMarkdown(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var doc struct {
		Content []adfNode `json:"content"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	return strings.TrimSpace(renderADFNodes(doc.Content, 0))
}

type adfNode struct {
	Type    string                 `json:"type"`
	Text    string                 `json:"text"`
	Attrs   map[string]interface{} `json:"attrs,omitempty"`
	Marks   []map[string]interface{} `json:"marks,omitempty"`
	Content []adfNode              `json:"content,omitempty"`
}

func renderADFNodes(nodes []adfNode, indent int) string {
	var b strings.Builder
	for _, node := range nodes {
		content := renderADFNodes(node.Content, indent)
		switch node.Type {
		case "text":
			b.WriteString(applyADFMarks(node.Text, node.Marks))
		case "hardBreak":
			b.WriteString("\n")
		case "paragraph":
			b.WriteString(strings.TrimSpace(content))
			b.WriteString("\n\n")
		case "heading":
			level := 1
			if n, ok := node.Attrs["level"].(float64); ok && n >= 1 && n <= 6 { level = int(n) }
			b.WriteString(strings.Repeat("#", level) + " " + strings.TrimSpace(content) + "\n\n")
		case "bulletList":
			b.WriteString(renderADFList(node.Content, indent, "-"))
		case "orderedList":
			b.WriteString(renderADFList(node.Content, indent, "1."))
		case "listItem":
			b.WriteString(strings.TrimSpace(content))
		case "blockquote":
			for _, line := range strings.Split(strings.TrimSpace(content), "\n") { b.WriteString("> " + line + "\n") }
			b.WriteString("\n")
		case "codeBlock":
			lang, _ := node.Attrs["language"].(string)
			b.WriteString("```" + lang + "\n" + strings.TrimSpace(content) + "\n```\n\n")
		case "rule":
			b.WriteString("---\n\n")
		case "inlineCard":
			if url, ok := node.Attrs["url"].(string); ok { b.WriteString(url) }
		default:
			b.WriteString(content)
		}
	}
	return b.String()
}

func renderADFList(items []adfNode, indent int, bullet string) string {
	var b strings.Builder
	for _, item := range items {
		content := strings.TrimSpace(renderADFNodes(item.Content, indent+1))
		lines := strings.Split(content, "\n")
		prefix := strings.Repeat("  ", indent) + bullet + " "
		if len(lines) > 0 { b.WriteString(prefix + lines[0] + "\n") }
		for _, line := range lines[1:] { if strings.TrimSpace(line) != "" { b.WriteString(strings.Repeat("  ", indent+1) + line + "\n") } }
	}
	b.WriteString("\n")
	return b.String()
}

func applyADFMarks(text string, marks []map[string]interface{}) string {
	for _, mark := range marks {
		typ, _ := mark["type"].(string)
		switch typ {
		case "strong": text = "**" + text + "**"
		case "em": text = "*" + text + "*"
		case "code": text = "`" + text + "`"
		case "strike": text = "~~" + text + "~~"
		case "link":
			if attrs, ok := mark["attrs"].(map[string]interface{}); ok { if href, ok := attrs["href"].(string); ok { text = "[" + text + "](" + href + ")" } }
		}
	}
	return text
}

func escapeTable(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}
