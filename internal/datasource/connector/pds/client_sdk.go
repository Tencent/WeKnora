package pds

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	pds "github.com/alibabacloud-go/pds-20220301/v5/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/alibabacloud-go/tea/tea"

	"github.com/Tencent/WeKnora/internal/datasource"
)

// sdkClient wraps the official alibabacloud-go/pds-20220301 SDK client
// and converts between the SDK's request/response types and our internal
// pdsDrive / pdsFile / pdsDeltaItem shapes.
//
// Why an SDK and not hand-rolled signing: the Alibaba Cloud API
// gateway enforces ACS3-HMAC-SHA256 with several non-obvious details
// (canonical request ordering, signed-header list, replay-nonce
// inclusion, content-type signed-headers entry). The SDK is the
// supported, tested implementation; we don't re-implement it.
type sdkClient struct {
	inner          *pds.Client
	cfg            *Config
	downloadClient *http.Client
}

// newSDKClient constructs the SDK client from our Config. AK/SK
// credentials must be present — callers should check first.
func newSDKClient(cfg *Config) (*sdkClient, error) {
	if cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return nil, fmt.Errorf("newSDKClient requires access_key_id and access_key_secret")
	}
	rawEndpoint := cfg.GetEndpoint()
	// The SDK endpoint is the bare host (no scheme). We strip whatever
	// the user configured and remember the scheme separately so we can
	// pass it to the SDK via Config.Protocol — without this the SDK
	// forces HTTPS even when the configured endpoint was http://...
	// (matters for tests and for any HTTP-only deployment).
	protocol := "https"
	endpoint := strings.TrimPrefix(rawEndpoint, "https://")
	if strings.HasPrefix(rawEndpoint, "http://") {
		protocol = "http"
		endpoint = strings.TrimPrefix(rawEndpoint, "http://")
	}
	endpoint = strings.TrimRight(endpoint, "/")

	config := &openapiutil.Config{
		AccessKeyId:     tea.String(cfg.AccessKeyID),
		AccessKeySecret: tea.String(cfg.AccessKeySecret),
		Endpoint:        tea.String(endpoint),
		Protocol:        tea.String(protocol),
		// Force ACS3-HMAC-SHA256 (v3) signing — the default "acs AK:..."
		// (v1 / HMAC-SHA1) is rejected by the Enterprise PDS gateway with
		// InvalidAuthorization. "v2" here is the SDK's internal label for
		// the ACS3 algorithm (see darabonba-openapi CallApi dispatch).
		SignatureAlgorithm: tea.String("v2"),
	}
	c, err := pds.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("pds sdk init: %w", err)
	}
	return &sdkClient{
		inner:          c,
		cfg:            cfg,
		downloadClient: datasource.NewConnectorHTTPClient(downloadTimeout),
	}, nil
}

// defaultRuntime returns a runtime options block tuned for our needs.
// The SDK uses *int (not *int32) for timeouts.
func defaultRuntime() *dara.RuntimeOptions {
	return &dara.RuntimeOptions{
		ConnectTimeout: tea.Int(60_000),  // 60s
		ReadTimeout:    tea.Int(120_000), // 120s
		Autoretry:      tea.Bool(false),
		MaxAttempts:    tea.Int(1),
	}
}

// ListDrive returns every PDS drive the AK/SK can read, following the
// marker pagination until drained (bounded by maxListDrivePages).
func (c *sdkClient) ListDrive(_ context.Context) ([]pdsDrive, error) {
	var out []pdsDrive
	marker := ""
	for page := 0; page < maxListDrivePages; page++ {
		limit := int32(100)
		req := &pds.ListDriveRequest{Limit: &limit}
		if marker != "" {
			req.Marker = tea.String(marker)
		}
		resp, err := c.inner.ListDriveWithOptions(req, nil, defaultRuntime())
		if err != nil {
			return nil, fmt.Errorf("pds list drive: %w", err)
		}
		if resp.Body == nil {
			return out, nil
		}
		for _, d := range resp.Body.Items {
			out = append(out, driveFromSDK(d))
		}
		if resp.Body.NextMarker == nil || *resp.Body.NextMarker == "" {
			return out, nil
		}
		marker = *resp.Body.NextMarker
	}
	return out, nil
}

// ListFile returns one page of the direct children of a folder inside a
// drive. parentID == "" means "drive root" (PDS expects "root" there —
// we translate in the request to keep the connector contract simple).
func (c *sdkClient) ListFile(_ context.Context, driveID, parentID, marker string) ([]pdsFile, string, error) {
	limit := int32(100)
	req := &pds.ListFileRequest{
		DriveId: tea.String(driveID),
		Limit:   &limit,
	}
	if parentID == "" {
		parentID = "root"
	}
	req.ParentFileId = tea.String(parentID)
	if marker != "" {
		req.Marker = tea.String(marker)
	}
	resp, err := c.inner.ListFileWithOptions(req, nil, defaultRuntime())
	if err != nil {
		return nil, "", fmt.Errorf("pds list file: %w", err)
	}
	files, nextMarker := filesFromSDK(resp.Body)
	return files, nextMarker, nil
}

// GetFile fetches the metadata of a single file or folder. Used to walk
// up the parent chain when reconstructing readable paths for delta items.
func (c *sdkClient) GetFile(_ context.Context, driveID, fileID string) (pdsFile, error) {
	resp, err := c.inner.GetFileWithOptions(
		&pds.GetFileRequest{
			DriveId: tea.String(driveID),
			FileId:  tea.String(fileID),
		},
		nil,
		defaultRuntime(),
	)
	if err != nil {
		return pdsFile{}, fmt.Errorf("pds get file: %w", err)
	}
	return fileFromSDK(resp.Body), nil
}

// GetDownloadURL returns a signed HTTPS URL for the file's body.
func (c *sdkClient) GetDownloadURL(_ context.Context, driveID, fileID string) (string, error) {
	resp, err := c.inner.GetDownloadUrlWithOptions(
		&pds.GetDownloadUrlRequest{
			DriveId: tea.String(driveID),
			FileId:  tea.String(fileID),
		},
		nil,
		defaultRuntime(),
	)
	if err != nil {
		return "", fmt.Errorf("pds get download url: %w", err)
	}
	if resp.Body == nil || resp.Body.Url == nil {
		return "", fmt.Errorf("pds get download url: empty url in response")
	}
	return *resp.Body.Url, nil
}

// GetLastCursor returns the latest server-side cursor for the drive's
// change feed. Used as the resume point on the first incremental sync.
func (c *sdkClient) GetLastCursor(_ context.Context, driveID string) (string, error) {
	resp, err := c.inner.DeltaGetLastCursorWithOptions(
		&pds.DeltaGetLastCursorRequest{DriveId: tea.String(driveID)},
		nil,
		defaultRuntime(),
	)
	if err != nil {
		return "", fmt.Errorf("pds get last cursor: %w", err)
	}
	if resp.Body == nil || resp.Body.Cursor == nil {
		return "", fmt.Errorf("pds get last cursor: empty cursor in response")
	}
	return *resp.Body.Cursor, nil
}

// ListDelta returns one page of the drive's change feed since the given
// cursor. Each item keeps its op (create/overwrite/update/move/delete) —
// deletion MUST be detected via op, since absence from a delta page says
// nothing about whether a file still exists. A rejected cursor surfaces
// as *cursorExpiredError so callers can re-handshake.
func (c *sdkClient) ListDelta(_ context.Context, driveID, cursor string) ([]pdsDeltaItem, string, bool, error) {
	limit := int32(200)
	resp, err := c.inner.ListDeltaWithOptions(
		&pds.ListDeltaRequest{
			DriveId: tea.String(driveID),
			Cursor:  tea.String(cursor),
			Limit:   &limit,
		},
		nil,
		defaultRuntime(),
	)
	if err != nil {
		if isCursorExpiredErr(err) {
			return nil, "", false, &cursorExpiredError{err: err}
		}
		return nil, "", false, fmt.Errorf("pds list delta: %w", err)
	}
	items, nextCursor := deltaItemsFromSDK(resp.Body)
	hasMore := false
	if resp.Body != nil && resp.Body.HasMore != nil {
		hasMore = *resp.Body.HasMore
	}
	return items, nextCursor, hasMore, nil
}

// Download fetches the bytes of a PDS file from its signed download URL.
// Identical contract to client.Download — both transports implement it so
// the connector's pdsAPI interface stays flat.
func (c *sdkClient) Download(ctx context.Context, url string) ([]byte, string, error) {
	return downloadWithClient(ctx, c.downloadClient, url)
}

// --- SDK <-> internal type mappers --------------------------------------

// parseRFC3339 parses PDS's RFC3339 timestamp strings best-effort.
func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func driveFromSDK(d *pds.Drive) pdsDrive {
	if d == nil {
		return pdsDrive{}
	}
	out := pdsDrive{}
	if d.DriveId != nil {
		out.DriveID = *d.DriveId
	}
	if d.DriveName != nil {
		out.Name = *d.DriveName
	}
	if d.Description != nil {
		out.Description = *d.Description
	}
	if d.CreatedAt != nil {
		out.CreatedAtRaw = *d.CreatedAt
		out.CreatedAt = parseRFC3339(*d.CreatedAt)
	}
	return out
}

// filesFromSDK maps a *ListFileResponseBody (which contains []*File
// directly). The next-page marker is the next_marker field.
func filesFromSDK(body *pds.ListFileResponseBody) ([]pdsFile, string) {
	if body == nil {
		return nil, ""
	}
	out := make([]pdsFile, 0, len(body.Items))
	for _, f := range body.Items {
		out = append(out, fileFromSDK(f))
	}
	var next string
	if body.NextMarker != nil {
		next = *body.NextMarker
	}
	return out, next
}

// deltaItemsFromSDK maps a *ListDeltaResponseBody. The items here are
// *ListDeltaResponseBodyItems (each with {File, FileId, Op}); we keep
// the op because it is the ONLY reliable deletion signal in the delta
// feed.
func deltaItemsFromSDK(body *pds.ListDeltaResponseBody) ([]pdsDeltaItem, string) {
	if body == nil {
		return nil, ""
	}
	out := make([]pdsDeltaItem, 0, len(body.Items))
	for _, it := range body.Items {
		if it == nil {
			continue
		}
		item := pdsDeltaItem{File: fileFromSDK(it.File)}
		if it.FileId != nil {
			item.FileID = *it.FileId
		}
		if it.Op != nil {
			item.Op = *it.Op
		}
		out = append(out, item)
	}
	var next string
	if body.Cursor != nil {
		next = *body.Cursor
	}
	return out, next
}

func fileFromSDK(f *pds.File) pdsFile {
	if f == nil {
		return pdsFile{}
	}
	out := pdsFile{}
	if f.FileId != nil {
		out.FileID = *f.FileId
	}
	if f.Name != nil {
		out.Name = *f.Name
	}
	if f.Type != nil {
		out.Type = *f.Type
	}
	if f.ParentFileId != nil {
		out.ParentID = *f.ParentFileId
	}
	if f.NamePath != nil {
		out.NamePath = *f.NamePath
	}
	if f.DownloadUrl != nil {
		out.URL = *f.DownloadUrl
	}
	if f.Size != nil {
		out.Size = *f.Size
	}
	if f.ContentType != nil {
		out.ContentType = *f.ContentType
	}
	if f.UpdatedAt != nil {
		out.UpdatedAtRaw = *f.UpdatedAt
		out.UpdatedAt = parseRFC3339(*f.UpdatedAt)
	}
	return out
}
