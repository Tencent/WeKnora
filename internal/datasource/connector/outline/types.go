package outline

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/types"
)

type document struct {
	ID           string      `json:"id"`
	CollectionID string      `json:"collectionId"`
	ParentID     string      `json:"parentDocumentId"`
	Title        string      `json:"title"`
	Text         *string     `json:"text"`
	URL          string      `json:"url"`
	CreatedAt    string      `json:"createdAt"`
	UpdatedAt    string      `json:"updatedAt"`
	PublishedAt  string      `json:"publishedAt"`
	ArchivedAt   string      `json:"archivedAt"`
	DeletedAt    string      `json:"deletedAt"`
	Revision     json.Number `json:"revision"`
}

type collection struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ArchivedAt string `json:"archivedAt"`
	DeletedAt  string `json:"deletedAt"`
}

type identity struct {
	BaseURL     string `json:"base_url"`
	WorkspaceID string `json:"workspace_id"`
	ActorID     string `json:"actor_id"`
}

func (d document) active(selected map[string]bool) bool {
	return selected[d.CollectionID] && d.PublishedAt != "" && d.ArchivedAt == "" && d.DeletedAt == ""
}

func decodeDocument(raw json.RawMessage) (document, error) {
	var wrapper struct {
		Document json.RawMessage `json:"document"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return document{}, errors.New("outline_response_invalid")
	}
	if len(wrapper.Document) > 0 {
		raw = wrapper.Document
	}
	var d document
	if json.Unmarshal(raw, &d) != nil || d.ID == "" {
		return d, errors.New("outline_response_invalid")
	}
	return d, nil
}

func sourceURL(base, raw string) (string, error) {
	root, _ := url.Parse(base + "/")
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("outline_response_invalid")
	}
	u = root.ResolveReference(u)
	if u.User != nil || u.Host != root.Host || u.Scheme != root.Scheme {
		return "", errors.New("outline_response_invalid")
	}
	return u.String(), nil
}

func fingerprint(d document, source string) string {
	body := ""
	if _, err := time.Parse(time.RFC3339, d.UpdatedAt); err != nil && d.Revision == "" && d.Text != nil {
		body = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ReplaceAll(*d.Text, "\r\n", "\n"))))
	}
	fields := []string{d.UpdatedAt, string(d.Revision), d.Title, d.CollectionID, d.ParentID, source,
		d.PublishedAt, d.ArchivedAt, d.DeletedAt, body}
	b, _ := json.Marshal(fields)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func fileName(title string) string {
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(title))
	if title == "" || title == "." || title == ".." {
		title = "untitled"
	}
	var out strings.Builder
	for _, r := range title {
		if out.Len()+len(string(r)) > 200 {
			break
		}
		out.WriteRune(r)
	}
	return out.String() + ".md"
}

func mappedItem(d document, instance identity) (types.FetchedItem, string, error) {
	if d.Text == nil {
		return types.FetchedItem{}, "", errors.New("outline_format_unsupported")
	}
	if len(*d.Text) > maxResponse {
		return types.FetchedItem{}, "", errTooLarge
	}
	source, err := sourceURL(instance.BaseURL, d.URL)
	if err != nil {
		return types.FetchedItem{}, "", err
	}
	fp := fingerprint(d, source)
	title := strings.Join(strings.Fields(d.Title), " ")
	title = strings.NewReplacer("\\", "\\\\", "#", "\\#", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;").Replace(title)
	if title == "" {
		title = "untitled"
	}
	created, _ := time.Parse(time.RFC3339, d.CreatedAt)
	updated, _ := time.Parse(time.RFC3339, d.UpdatedAt)
	return types.FetchedItem{
		ExternalID: d.ID, SourceResourceID: d.CollectionID, Title: d.Title,
		Content: []byte("# " + title + "\n\n" + absoluteLinks(*d.Text, source)), ContentType: "text/markdown",
		FileName: fileName(d.Title), URL: source, CreatedAt: created, UpdatedAt: updated,
		Metadata: map[string]string{
			"channel": types.ChannelOutline, "collection_id": d.CollectionID,
			"source_title": d.Title, "source_url": source, "source_revision": string(d.Revision),
			"source_fingerprint": fp, "parent_document_id": d.ParentID,
			"outline_workspace_id": instance.WorkspaceID,
		},
	}, fp, nil
}
