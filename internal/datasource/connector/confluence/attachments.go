package confluence

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const maxAttachmentBytes int64 = 20 << 20

// A full pass starts a fresh acknowledgement map, retaining the previous map
// only as a deletion baseline. Retries reuse confirmed children. If attachments
// are disabled or an interrupted full pass becomes incremental, keep every
// previously known child so a later removal remains observable.
func prepareAttachmentCursor(previous cursor, next *cursor, full, enabled bool) {
	if full && enabled && !previous.FullSync {
		next.FullAttachmentsBaseline = clonePageMap(previous.Attachments)
		next.Attachments = map[string]map[string]string{}
	} else if !full || !enabled {
		retainAttachmentBaseline(next)
	}
}

// Keep known children of pages whose existence was verified without a complete
// listing. Successful acknowledgements and deletions already updated these maps.
func retainAttachmentBaseline(next *cursor) {
	for pageID, versions := range next.FullAttachmentsBaseline {
		if next.Attachments[pageID] == nil {
			next.Attachments[pageID] = map[string]string{}
		}
		for id, version := range versions {
			if _, confirmed := next.Attachments[pageID][id]; !confirmed {
				next.Attachments[pageID][id] = version
			}
		}
	}
	next.FullAttachmentsBaseline = nil
}

type attachment struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	MediaType    string `json:"mediaType"`
	DownloadLink string `json:"downloadLink"`
	FileSize     int64  `json:"fileSize"`
	Version      struct {
		Number    int    `json:"number"`
		When      string `json:"when"`
		CreatedAt string `json:"createdAt"`
	} `json:"version"`
	Metadata struct {
		MediaType string `json:"mediaType"`
	} `json:"metadata"`
	Extensions struct {
		FileSize int64 `json:"fileSize"`
	} `json:"extensions"`
	Links struct {
		Download string `json:"download"`
	} `json:"_links"`
}

func syncAttachmentsEnabled(ds *types.DataSourceConfig) bool {
	v := ds.Settings["sync_attachments"]
	return v == true || v == "true"
}

func attachmentMIME(filename string) string {
	switch strings.ToLower(path.Ext(filename)) {
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	default:
		return ""
	}
}

// Enumerate completely before reconciling removals. A failed later page must
// never make an attachment that was not listed yet look deleted.
func (c *client) attachments(ctx context.Context, pageID string) ([]attachment, error) {
	start := "/rest/api/content/" + url.PathEscape(pageID) +
		"/child/attachment?expand=version,metadata,extensions&limit=100"
	if c.cfg.cloud() {
		start = "/api/v2/pages/" + url.PathEscape(pageID) + "/attachments?status=current&limit=100"
	}
	all := []attachment{}
	seen := map[string]bool{}
	err := c.paginate(ctx, start, func(endpoint string) (string, error) {
		var result struct {
			Results *[]attachment `json:"results"`
			Links   struct {
				Next string `json:"next"`
			} `json:"_links"`
		}
		if err := c.get(ctx, endpoint, &result); err != nil {
			return "", err
		}
		if result.Results == nil {
			return "", fmt.Errorf("confluence attachment listing omitted results")
		}
		for _, a := range *result.Results {
			if a.ID == "" || seen[a.ID] {
				return "", fmt.Errorf("invalid or repeated Confluence attachment ID")
			}
			seen[a.ID] = true
			if len(seen) > maxTraversalNodes {
				return "", fmt.Errorf("too many Confluence attachments")
			}
			all = append(all, a)
		}
		next := result.Links.Next
		if next != "" && !c.cfg.cloud() {
			u, err := url.Parse(next)
			if err != nil {
				return "", err
			}
			q := u.Query()
			q.Set("expand", "version,metadata,extensions")
			u.RawQuery = q.Encode()
			next = u.String()
		}
		return next, nil
	})
	return all, err
}

func (c *client) downloadAttachment(ctx context.Context, a attachment) ([]byte, error) {
	if a.FileSize > maxAttachmentBytes || a.Extensions.FileSize > maxAttachmentBytes {
		return nil, fmt.Errorf("attachment exceeds %d MiB limit", maxAttachmentBytes>>20)
	}
	link := a.DownloadLink
	if link == "" {
		link = a.Links.Download
	}
	if link == "" {
		return nil, fmt.Errorf("attachment has no download link")
	}
	endpoint, err := c.resolveEndpoint(link)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.authenticate(req)
	// Reuse the private-image transport's origin/context-path redirect boundary.
	// Downloads never relax that boundary or forward credentials to other hosts.
	resp, err := c.imageHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &apiError{endpoint: "attachment", status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxAttachmentBytes {
		return nil, fmt.Errorf("attachment exceeds %d MiB limit", maxAttachmentBytes>>20)
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if len(body) == 0 || mediaType == "text/html" || http.DetectContentType(body) == "text/html; charset=utf-8" {
		return nil, fmt.Errorf("attachment returned empty content or HTML instead of a document")
	}
	return body, nil
}

func (c *client) syncPageAttachments(
	ctx context.Context, root string, p page, next *cursor, h datasource.StreamHandler,
) error {
	files, err := c.attachments(ctx, p.ID)
	if err != nil {
		return fmt.Errorf("list attachments for Confluence page %s: %w", p.ID, err)
	}
	if next.Attachments == nil {
		next.Attachments = map[string]map[string]string{}
	}
	if next.Attachments[p.ID] == nil {
		next.Attachments[p.ID] = map[string]string{}
	}
	versions := next.Attachments[p.ID]
	keep := map[string]bool{}
	for _, a := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := types.SubtreeChildID(p.ID, "file", a.ID)
		keep[id] = true
		if attachmentMIME(a.Title) == "" {
			_, known := versions[id]
			_, baselineKnown := next.FullAttachmentsBaseline[p.ID][id]
			if known || baselineKnown {
				return emitAttachmentFailure(ctx, root, p, a,
					fmt.Errorf("attachment changed to an unsupported file type"), h)
			}
			continue
		} // Only the documented PDF/Office whitelist is mirrored.
		version := fmt.Sprintf("%d:%s", a.Version.Number, a.Title)
		if a.Version.Number > 0 && versions[id] == version {
			continue
		}
		item := types.FetchedItem{
			ExternalID: id, Title: a.Title,
			FileName:    safeFilename(strings.TrimSuffix(a.Title, path.Ext(a.Title))) + path.Ext(a.Title),
			ContentType: attachmentMIME(a.Title),
			URL:         c.resourceURL(p.Links.WebUI), SourceResourceID: root, RetryOnFailure: true,
			Metadata: map[string]string{
				"channel": types.ChannelConfluence, "page_id": p.ID, "source_page_title": p.Title,
				"source_page_url": c.resourceURL(p.Links.WebUI), "attachment_id": a.ID,
				"attachment_version": strconv.Itoa(a.Version.Number),
			},
		}
		item.Metadata["attachment_filename"] = a.Title
		mediaType := a.MediaType
		if mediaType == "" {
			mediaType = a.Metadata.MediaType
		}
		if mediaType == "" {
			mediaType = item.ContentType
		}
		item.Metadata["attachment_mime_type"] = mediaType
		stamp := a.Version.When
		if stamp == "" {
			stamp = a.Version.CreatedAt
		}
		item.UpdatedAt, _ = time.Parse(time.RFC3339, stamp)
		item.Content, err = c.downloadAttachment(ctx, a)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return emitAttachmentFailure(ctx, root, p, a, err, h)
		}
		if err := h.Emit(ctx, item); err != nil {
			return err
		}
		versions[id] = version
		if next.AttachmentOwners == nil {
			next.AttachmentOwners = map[string]string{}
		}
		next.AttachmentOwners[p.ID] = root
		if err := h.Checkpoint(ctx, next.syncCursor()); err != nil {
			return err
		}
	}
	return removeMissingAttachments(ctx, root, p.ID, keep, next, h)
}

func emitAttachmentFailure(
	ctx context.Context, root string, p page, a attachment, err error, h datasource.StreamHandler,
) error {
	failed := failedPageItem(root, p, err)
	failed.ExternalID = types.SubtreeChildID(p.ID, "file", a.ID)
	failed.Title = a.Title
	failed.RetryOnFailure = true
	if emitErr := h.Emit(ctx, failed); emitErr != nil {
		return emitErr
	}
	return fmt.Errorf("fetch Confluence attachment %s: %w", a.ID, err)
}

func removeMissingAttachments(
	ctx context.Context, root, pageID string, keep map[string]bool, next *cursor, h datasource.StreamHandler,
) error {
	pending := map[string]bool{}
	for id := range next.FullAttachmentsBaseline[pageID] {
		pending[id] = true
	}
	for id := range next.Attachments[pageID] {
		pending[id] = true
	}
	ids := []string{}
	for id := range pending {
		if !keep[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := types.FetchedItem{
			ExternalID: id, IsDeleted: true, SourceResourceID: root, RetryOnFailure: true,
			Metadata: map[string]string{"channel": types.ChannelConfluence, "page_id": pageID},
		}
		if err := h.Emit(ctx, item); err != nil {
			return err
		}
		delete(next.Attachments[pageID], id)
		delete(next.FullAttachmentsBaseline[pageID], id)
		if err := h.Checkpoint(ctx, next.syncCursor()); err != nil {
			return err
		}
	}
	return nil
}
