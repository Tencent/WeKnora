package confluence

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

// convertPageWithTasks converts a page body.view like convertPageHTML and
// keeps inline task states as GFM task markers.
func convertPageWithTasks(content string) (string, error) {
	content, restoreTasks := markTaskItems(content)
	markdown, err := convertPageHTML(content)
	if err != nil {
		return "", err
	}
	return restoreTasks(markdown), nil
}

// markTaskItems prefixes each item of a Confluence task list
// (<ul class="inline-task-list">, completed items carry class "checked") with
// a placeholder. The converter would otherwise drop the state, and literal
// "[x]" text would be escaped. The returned function turns the placeholders
// into GFM task markers.
func markTaskItems(content string) (string, func(string) string) {
	identity := func(markdown string) string { return markdown }
	if !strings.Contains(content, "inline-task-list") {
		return content, identity
	}
	done, todo := "WEKNORACONFLUENCETASKDONE", "WEKNORACONFLUENCETASKTODO"
	for strings.Contains(content, done) || strings.Contains(content, todo) {
		done, todo = done+"X", todo+"X"
	}

	var out strings.Builder
	var taskLists []bool // one entry per open <ul>/<ol>: is it a task list?
	marked := false
	tokenizer := html.NewTokenizer(strings.NewReader(content))
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			if tokenizer.Err() != io.EOF {
				return content, identity
			}
			break
		}
		out.Write(tokenizer.Raw())
		if tokenType != html.StartTagToken && tokenType != html.EndTagToken {
			continue
		}
		token := tokenizer.Token()
		switch token.Data {
		case "ul", "ol":
			if tokenType == html.StartTagToken {
				taskLists = append(taskLists, token.Data == "ul" && hasClass(token, "inline-task-list"))
			} else if len(taskLists) > 0 {
				taskLists = taskLists[:len(taskLists)-1]
			}
		case "li":
			if tokenType == html.StartTagToken && len(taskLists) > 0 && taskLists[len(taskLists)-1] {
				if hasClass(token, "checked") {
					out.WriteString(done + " ")
				} else {
					out.WriteString(todo + " ")
				}
				marked = true
			}
		}
	}
	if !marked {
		return content, identity
	}
	return out.String(), func(markdown string) string {
		return strings.NewReplacer(done, "[x]", todo, "[ ]").Replace(markdown)
	}
}

func hasClass(token html.Token, class string) bool {
	for _, attr := range token.Attr {
		if attr.Key == "class" {
			for _, name := range strings.Fields(attr.Val) {
				if name == class {
					return true
				}
			}
		}
	}
	return false
}
