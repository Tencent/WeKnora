// Package links implements the Feishu/Lark document-URL-list connector.
package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connector syncs individual Feishu/Lark documents from a user-supplied URL
// list. It never enumerates wiki spaces or Drive folders.
type Connector struct {
	region core.Region
}

// NewConnector creates a URL-list connector for RegionFeishuLinks or RegionLarkLinks.
func NewConnector(region core.Region) *Connector {
	return &Connector{region: region}
}

var _ datasource.StreamingConnector = (*Connector)(nil)

// Type returns the connector type identifier.
func (c *Connector) Type() string {
	return c.region.ConnectorType
}

// Validate verifies that the Feishu/Lark configuration is valid by testing connectivity.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return err
	}
	client := core.NewClient(feishuConfig)
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("%s connection failed: %w", c.region.Label, err)
	}
	return nil
}

// ListResources resolves the configured document URLs into picker rows.
// parentID is unused: the list is flat and has no children.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	if parentID != "" {
		return nil, nil
	}
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := core.NewClient(feishuConfig)
	urls := core.ExtractLinkURLs(config.Settings)
	_, resources := resolveURLs(ctx, client, urls)
	return resources, nil
}

// ResolveResourceAncestors is a no-op: URL-list resources are not nested.
func (c *Connector) ResolveResourceAncestors(
	_ context.Context, _ *types.DataSourceConfig, _ []string,
) ([]string, error) {
	return nil, nil
}

const linksRootResourceID = "links"

// FetchAll performs a full sync of the configured document URLs.
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, _ []string,
) ([]types.FetchedItem, error) {
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := core.NewClient(feishuConfig)
	cfg := withLinksRoot(config)
	return core.FetchAllEngine(ctx, client, cfg, cfg.ResourceIDs, newLinksOps(c.region, config))
}

// FetchIncremental performs an incremental sync of the configured document URLs.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, nil, err
	}
	client := core.NewClient(feishuConfig)
	ops := newLinksOps(c.region, config)
	if !hasLinksConfigured(config) {
		return nil, nil, errors.New(ops.EmptyResourceIDsError())
	}
	return core.FetchIncrementalEngine(ctx, client, withLinksRoot(config), cursor, ops)
}

// FetchStream performs a resumable sync of the configured document URLs.
func (c *Connector) FetchStream(
	ctx context.Context, config *types.DataSourceConfig,
	cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	feishuConfig, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	client := core.NewClient(feishuConfig)
	ops := newLinksOps(c.region, config)
	if !hasLinksConfigured(config) {
		return nil, errors.New(ops.EmptyResourceIDsError())
	}
	return core.FetchStreamEngine(ctx, client, withLinksRoot(config), cursor, h, ops)
}

func hasLinksConfigured(config *types.DataSourceConfig) bool {
	if config == nil {
		return false
	}
	if len(core.ExtractLinkURLs(config.Settings)) > 0 {
		return true
	}
	return len(config.ResourceIDs) > 0
}

func withLinksRoot(config *types.DataSourceConfig) *types.DataSourceConfig {
	if config == nil {
		return &types.DataSourceConfig{ResourceIDs: []string{linksRootResourceID}}
	}
	cp := *config
	cp.ResourceIDs = []string{linksRootResourceID}
	return &cp
}

type linkDoc struct {
	ResourceID string
	ObjType    string
	ObjToken   string
	Title      string
	URL        string
	EditTime   string
	NodeToken  string
}

type linksOps struct {
	region core.Region
	urls   []string

	mu   sync.Mutex
	docs map[string]linkDoc
}

func newLinksOps(region core.Region, config *types.DataSourceConfig) *linksOps {
	var urls []string
	if config != nil {
		urls = core.ExtractLinkURLs(config.Settings)
	}
	return &linksOps{region: region, urls: urls}
}

func (o *linksOps) resolve(ctx context.Context, client *core.Client) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.docs != nil {
		return
	}
	docs, _ := resolveURLs(ctx, client, o.urls)
	o.docs = make(map[string]linkDoc, len(docs))
	for _, d := range docs {
		o.docs[d.ResourceID] = d
	}
}

func (o *linksOps) List(ctx context.Context, client *core.Client, resourceID string) ([]linkDoc, error, error) {
	o.resolve(ctx, client)
	if resourceID == linksRootResourceID {
		out := make([]linkDoc, 0, len(o.docs))
		for _, d := range o.docs {
			out = append(out, d)
		}
		return out, nil, nil
	}
	if d, ok := o.docs[resourceID]; ok {
		return []linkDoc{d}, nil, nil
	}
	objType, objToken := core.ParseLinkResourceID(resourceID)
	if objType == "" || objToken == "" {
		return nil, nil, fmt.Errorf("invalid link resource id %s", resourceID)
	}
	metas, failed, err := client.BatchQueryMetas(ctx, []core.DriveDocMetaRequest{
		{DocToken: objToken, DocType: objType},
	})
	if err != nil {
		return nil, nil, err
	}
	if len(metas) == 1 {
		d := linkDocFromMeta(metas[0], "")
		return []linkDoc{d}, nil, nil
	}
	if len(failed) > 0 {
		return nil, nil, fmt.Errorf("document %s inaccessible: token=%s code=%d", resourceID, failed[0].Token, failed[0].Code)
	}
	return nil, nil, fmt.Errorf("document %s not found", resourceID)
}

func (o *linksOps) Token(n linkDoc) string    { return n.ObjToken }
func (o *linksOps) Title(n linkDoc) string    { return n.Title }
func (o *linksOps) ObjType(n linkDoc) string  { return n.ObjType }
func (o *linksOps) EditTime(n linkDoc) string { return n.EditTime }

func (o *linksOps) Fetch(ctx context.Context, client *core.Client, n linkDoc, resourceID string, multimodal bool) ([]*types.FetchedItem, error) {
	return fetchLinkDoc(ctx, client, n, resourceID, multimodal, o.region)
}

func (o *linksOps) ListFailureItems(_ string, _ error) []types.FetchedItem { return nil }
func (o *linksOps) ResourceNoun() string                                   { return "documents" }
func (o *linksOps) EmptyResourceIDsError() string {
	return "no resource IDs (Feishu document links) configured"
}
func (o *linksOps) LogTag() string { return "[" + o.region.Label + "]" }

func (o *linksOps) DecodeCursorTimes(m map[string]interface{}) map[string]map[string]string {
	var prev core.FeishuLinksCursor
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &prev)
	return prev.DocTimes
}

func (o *linksOps) EncodeCursor(times map[string]map[string]string, lastSync time.Time) *types.SyncCursor {
	fc := core.FeishuLinksCursor{LastSyncTime: lastSync, DocTimes: times}
	m := make(map[string]interface{})
	b, _ := json.Marshal(fc)
	_ = json.Unmarshal(b, &m)
	return &types.SyncCursor{LastSyncTime: lastSync, ConnectorCursor: m}
}

func fetchLinkDoc(
	ctx context.Context, client *core.Client, doc linkDoc, resourceID string, multimodal bool, region core.Region,
) ([]*types.FetchedItem, error) {
	if !core.IsSupportedDocType(doc.ObjType) {
		return nil, nil
	}

	editTime := core.ParseFeishuTimestamp(doc.EditTime)
	channel := types.ChannelFeishuLinks
	if region.ConnectorType == types.ConnectorTypeLarkLinks {
		channel = types.ChannelLarkLinks
	}
	title := doc.Title
	if title == "" {
		title = doc.ObjToken
	}
	baseMeta := map[string]string{
		"obj_token":  doc.ObjToken,
		"obj_type":   doc.ObjType,
		"node_token": doc.NodeToken,
		"channel":    channel,
	}

	switch doc.ObjType {
	case "docx":
		return core.FetchDocxWithBlocks(ctx, client, core.DocxFetchInput{
			DocToken:          doc.ObjToken,
			ObjToken:          doc.ObjToken,
			Title:             title,
			URL:               doc.URL,
			ResourceID:        resourceID,
			EditTime:          editTime,
			BaseMeta:          baseMeta,
			MultimodalEnabled: multimodal,
		})
	case "doc", "sheet", "bitable":
		data, fileName, err := client.ExportAndDownload(ctx, doc.ObjToken, doc.ObjType)
		if err != nil {
			return nil, fmt.Errorf("export %s (%s): %w", title, doc.ObjType, err)
		}
		ext := core.ExportFileExtToSuffix[core.ObjTypeToExportFileExtension[doc.ObjType]]
		if fileName == "" {
			fileName = core.SanitizeFileName(title) + ext
		} else if !strings.HasSuffix(strings.ToLower(fileName), ext) {
			fileName = core.SanitizeFileName(fileName) + ext
		}
		return []*types.FetchedItem{{
			ExternalID:       doc.ObjToken,
			Title:            title,
			Content:          data,
			ContentType:      "application/octet-stream",
			FileName:         fileName,
			URL:              doc.URL,
			UpdatedAt:        editTime,
			SourceResourceID: resourceID,
			Metadata:         baseMeta,
		}}, nil
	case "file":
		data, err := client.DownloadDriveFile(ctx, doc.ObjToken)
		if err != nil {
			return nil, fmt.Errorf("download file %s (%s): %w", title, doc.ObjToken, err)
		}
		fileName := title
		return []*types.FetchedItem{{
			ExternalID:       doc.ObjToken,
			Title:            title,
			Content:          data,
			ContentType:      "application/octet-stream",
			FileName:         fileName,
			URL:              doc.URL,
			UpdatedAt:        editTime,
			SourceResourceID: resourceID,
			Metadata:         baseMeta,
		}}, nil
	default:
		return nil, nil
	}
}

func resolveURLs(ctx context.Context, client *core.Client, urls []string) ([]linkDoc, []types.Resource) {
	parsed := make([]core.ParsedDocURL, 0, len(urls))
	for _, u := range urls {
		parsed = append(parsed, core.ParseFeishuDocURL(u))
	}
	unique, _ := core.DedupeParsedURLs(parsed)

	docs := make([]linkDoc, 0, len(unique))
	resources := make([]types.Resource, 0, len(unique))
	seen := make(map[string]struct{})

	var driveParsed []core.ParsedDocURL
	for _, p := range unique {
		if p.RejectReason != "" {
			resources = append(resources, errorResource(p, p.RejectReason, rejectMessage(p.RejectReason)))
			continue
		}
		if p.Kind == core.LinkKindWiki {
			node, err := client.GetWikiNode(ctx, "", p.Token)
			if err != nil {
				logger.Warnf(ctx, "[FeishuLinks] get_node %s: %v", p.Token, err)
				resources = append(resources, errorResource(p, classifyResolveErr(err), err.Error()))
				continue
			}
			if !core.IsSupportedDocType(node.ObjType) {
				resources = append(resources, errorResource(p, "unsupported_type", node.ObjType))
				continue
			}
			doc := linkDocFromWiki(p, node)
			if _, dup := seen[doc.ResourceID]; dup {
				continue
			}
			seen[doc.ResourceID] = struct{}{}
			docs = append(docs, doc)
			resources = append(resources, doc.toResource())
			continue
		}
		driveParsed = append(driveParsed, p)
	}

	if len(driveParsed) == 0 {
		return docs, resources
	}

	reqs := make([]core.DriveDocMetaRequest, 0, len(driveParsed))
	byToken := make(map[string]core.ParsedDocURL, len(driveParsed))
	for _, p := range driveParsed {
		reqs = append(reqs, core.DriveDocMetaRequest{DocToken: p.Token, DocType: p.Kind})
		byToken[p.Kind+":"+p.Token] = p
	}
	metas, failed, err := client.BatchQueryMetas(ctx, reqs)
	if err != nil {
		logger.Warnf(ctx, "[FeishuLinks] batch_query: %v", err)
		for _, p := range driveParsed {
			resources = append(resources, errorResource(p, classifyResolveErr(err), err.Error()))
		}
		return docs, resources
	}

	failedSet := make(map[string]core.DriveMetaFailedItem, len(failed))
	for _, f := range failed {
		failedSet[f.Token] = f
	}
	matched := make(map[string]struct{}, len(metas))
	for _, meta := range metas {
		p := lookupParsed(byToken, meta)
		doc := linkDocFromMeta(meta, originalURL(p))
		if !core.IsSupportedDocType(doc.ObjType) {
			resources = append(resources, errorResource(p, "unsupported_type", doc.ObjType))
			continue
		}
		if _, dup := seen[doc.ResourceID]; dup {
			matched[p.Kind+":"+p.Token] = struct{}{}
			continue
		}
		seen[doc.ResourceID] = struct{}{}
		matched[p.Kind+":"+p.Token] = struct{}{}
		docs = append(docs, doc)
		resources = append(resources, doc.toResource())
	}
	for _, p := range driveParsed {
		if _, ok := matched[p.Kind+":"+p.Token]; ok {
			continue
		}
		if f, ok := failedSet[p.Token]; ok {
			resources = append(resources, errorResource(
				p, classifyMetaFailCode(f.Code), fmt.Sprintf("code=%d", f.Code),
			))
			continue
		}
		resources = append(resources, errorResource(p, "not_found", "document meta not returned"))
	}
	return docs, resources
}

func lookupParsed(byToken map[string]core.ParsedDocURL, meta core.DriveDocMeta) core.ParsedDocURL {
	if p, ok := byToken[meta.DocType+":"+meta.DocToken]; ok {
		return p
	}
	for _, p := range byToken {
		if p.Token == meta.DocToken {
			return p
		}
	}
	return core.ParsedDocURL{Original: meta.URL, Kind: meta.DocType, Token: meta.DocToken}
}

func originalURL(p core.ParsedDocURL) string {
	if p.Original != "" {
		return p.Original
	}
	return ""
}

func linkDocFromWiki(p core.ParsedDocURL, node core.WikiNode) linkDoc {
	edit := node.ObjEditTime
	if edit == "" {
		edit = node.NodeEditTime
	}
	title := node.Title
	if title == "" {
		title = node.ObjToken
	}
	return linkDoc{
		ResourceID: core.LinkResourceID(node.ObjType, node.ObjToken),
		ObjType:    node.ObjType,
		ObjToken:   node.ObjToken,
		Title:      title,
		URL:        p.Original,
		EditTime:   edit,
		NodeToken:  node.NodeToken,
	}
}

func linkDocFromMeta(meta core.DriveDocMeta, original string) linkDoc {
	u := original
	if u == "" {
		u = meta.URL
	}
	title := meta.Title
	if title == "" {
		title = meta.DocToken
	}
	return linkDoc{
		ResourceID: core.LinkResourceID(meta.DocType, meta.DocToken),
		ObjType:    meta.DocType,
		ObjToken:   meta.DocToken,
		Title:      title,
		URL:        u,
		EditTime:   meta.LatestModifyTime,
	}
}

func (d linkDoc) toResource() types.Resource {
	return types.Resource{
		ExternalID:  d.ResourceID,
		Name:        d.Title,
		Type:        d.ObjType,
		URL:         d.URL,
		HasChildren: false,
		ModifiedAt:  core.ParseFeishuTimestamp(d.EditTime),
		Metadata: map[string]interface{}{
			"obj_type":     d.ObjType,
			"obj_token":    d.ObjToken,
			"node_token":   d.NodeToken,
			"original_url": d.URL,
		},
	}
}

func errorResource(p core.ParsedDocURL, code, msg string) types.Resource {
	id := p.Original
	if id == "" {
		id = code
	}
	return types.Resource{
		ExternalID:  "error:" + id,
		Name:        p.Original,
		Type:        "link_error",
		URL:         p.Original,
		HasChildren: false,
		Metadata: map[string]interface{}{
			"error_code":   code,
			"error":        msg,
			"original_url": p.Original,
		},
	}
}

func rejectMessage(code string) string {
	switch code {
	case core.RejectWikiSpace:
		return "this is a wiki space URL; use the Feishu Wiki connector"
	case core.RejectDriveFolder:
		return "this is a Drive folder URL; use the Feishu Drive connector"
	default:
		return "unrecognized Feishu document URL"
	}
}

func classifyResolveErr(err error) string {
	if err == nil {
		return "resolve_failed"
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "status=403") || strings.Contains(s, "forbidden") || strings.Contains(s, "1061004") {
		return "no_permission"
	}
	if strings.Contains(s, "status=404") || strings.Contains(s, "not found") || strings.Contains(s, "1061003") {
		return "not_found"
	}
	return "resolve_failed"
}

func classifyMetaFailCode(code int) string {
	switch code {
	case 970005, 1061004:
		return "no_permission"
	case 1061003:
		return "not_found"
	default:
		return "resolve_failed"
	}
}
