package confluence

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

const commentsVersionPrefix = "comments:"

type commentView struct {
	Body struct {
		View struct {
			Value *string `json:"value"`
		} `json:"view"`
	} `json:"body"`
}

func (c *client) cloudCommentView(ctx context.Context, id string) (*string, error) {
	// Cloud's bulk footer-comments endpoint does not support rendered view bodies.
	var comment commentView
	endpoint := "/api/v2/footer-comments/" + url.PathEscape(id) + "?body-format=view"
	if err := c.get(ctx, endpoint, &comment); err != nil {
		return nil, err
	}
	return comment.Body.View.Value, nil
}

// pageComments fetches top-level page comments. Keep the original document
// intact when any part of the comment listing is unavailable.
func (c *client) pageComments(ctx context.Context, pageID string) (string, error) {
	query := url.Values{"limit": {"100"}}
	path := "/rest/api/content/" + url.PathEscape(pageID) + "/child/comment"
	query.Set("expand", "body.view")
	query.Set("location", "footer")
	query.Set("depth", "root")
	cloud := c.cfg.cloud()
	if cloud {
		path = "/api/v2/pages/" + url.PathEscape(pageID) + "/footer-comments"
		query = url.Values{"limit": {"100"}, "status": {"current"}}
	}

	var comments strings.Builder
	seen := make(map[string]struct{})
	var bodyBytes int64
	err := c.paginate(ctx, path+"?"+query.Encode(), func(endpoint string) (string, error) {
		var result struct {
			Results *[]struct {
				ID string `json:"id"`
				commentView
			} `json:"results"`
			Links struct {
				Next string `json:"next"`
			} `json:"_links"`
		}
		if err := c.get(ctx, endpoint, &result); err != nil {
			return "", err
		}
		if result.Results == nil {
			return "", fmt.Errorf("confluence comment listing omitted results")
		}
		for _, comment := range *result.Results {
			if _, exists := seen[comment.ID]; comment.ID == "" || exists {
				return "", fmt.Errorf("invalid or repeated Confluence comment ID")
			}
			seen[comment.ID] = struct{}{}
			if len(seen) > maxTraversalNodes {
				return "", fmt.Errorf("too many Confluence comments")
			}
			body := comment.Body.View.Value
			if cloud {
				rendered, err := c.cloudCommentView(ctx, comment.ID)
				if err != nil {
					return "", fmt.Errorf("get rendered Confluence comment %s: %w", comment.ID, err)
				}
				body = rendered
			}
			if body == nil {
				return "", fmt.Errorf("confluence comment %s omitted its body", comment.ID)
			}
			bodyBytes += int64(len(*body))
			if bodyBytes > maxJSONResponseBytes {
				return "", fmt.Errorf("confluence comments exceed %d MiB limit", maxJSONResponseBytes>>20)
			}
			markdown, err := convertPageHTML(*body)
			if err != nil {
				return "", err
			}
			if text := strings.TrimSpace(markdown); text != "" {
				if comments.Len() > 0 {
					comments.WriteString("\n\n---\n\n")
				}
				comments.WriteString(text)
			}
		}
		if result.Links.Next == "" {
			return "", nil
		}
		next, err := url.Parse(result.Links.Next)
		if err != nil {
			return "", err
		}
		if next.Path == "" && !next.IsAbs() && next.Host == "" {
			next.Path = path
		}
		nextQuery := next.Query()
		for key, values := range query {
			nextQuery[key] = values
		}
		next.RawQuery = nextQuery.Encode()
		return next.String(), nil
	})
	return comments.String(), err
}
