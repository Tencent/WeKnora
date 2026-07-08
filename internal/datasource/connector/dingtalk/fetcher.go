package dingtalk

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

type dingtalkContentFetcher interface {
	Fetch(ctx context.Context, cli *client, doc wikiNode, workspaceID, selectedResourceID string) (*types.FetchedItem, string, bool, error)
}

func (c *Connector) contentFetchersForConfig(config *types.DataSourceConfig) []dingtalkContentFetcher {
	if len(c.contentFetchers) > 0 {
		return c.contentFetchers
	}
	return defaultDingTalkContentFetchers(config)
}

func defaultDingTalkContentFetchers(config *types.DataSourceConfig) []dingtalkContentFetcher {
	if dingTalkExportEnabled(config) {
		return []dingtalkContentFetcher{
			fileFetcher{},
			exportFetcher{},
			blockFetcher{},
		}
	}
	return []dingtalkContentFetcher{
		fileFetcher{},
		blockFetcher{},
	}
}

func dingTalkExportEnabled(config *types.DataSourceConfig) bool {
	if config == nil || config.Settings == nil {
		return false
	}
	mode := configSettingString(config.Settings,
		"online_doc_fetcher",
		"online_document_fetcher",
		"dingtalk_online_doc_fetcher",
	)
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "export", "official_export", "markdown_export":
		return true
	default:
		return false
	}
}

func configSettingString(settings map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		raw, ok := settings[key]
		if !ok || raw == nil {
			continue
		}
		if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		return strings.TrimSpace(fmt.Sprint(raw))
	}
	return ""
}

// exportFetcher starts DingTalk's official Markdown export task. The content
// arrives later through a dingdoc_export_finish callback, so the returned item
// is a pending marker for the application service to persist.
type exportFetcher struct{}

func (exportFetcher) Fetch(
	ctx context.Context,
	cli *client,
	doc wikiNode,
	workspaceID string,
	selectedResourceID string,
) (*types.FetchedItem, string, bool, error) {
	if !doc.isOnlineDocument() {
		return nil, "", false, nil
	}
	dentryUUID := doc.downloadDentryUUID()
	if strings.TrimSpace(dentryUUID) == "" {
		return nil, "", true, fmt.Errorf("dingtalk online document %s has empty dentry uuid", doc.NodeID)
	}
	taskID, err := cli.SubmitMarkdownExport(ctx, dentryUUID)
	if err != nil {
		return nil, "", true, err
	}
	hash := contentHash([]byte("export:" + dentryUUID + ":" + doc.modifiedTime()))
	item := &types.FetchedItem{
		ExternalID:       makeResourceID(workspaceID, doc.NodeID),
		Title:            doc.displayName(),
		ContentType:      "text/markdown",
		FileName:         markdownFileName(doc.displayName()),
		URL:              doc.URL,
		UpdatedAt:        parseDingTalkTime(doc.modifiedTime()),
		SourceResourceID: selectedResourceID,
		Metadata: map[string]string{
			"channel":           types.ChannelDingtalk,
			"workspace_id":      workspaceID,
			"node_id":           doc.NodeID,
			"doc_key":           doc.docKey(),
			"dentry_uuid":       dentryUUID,
			"category":          doc.Category,
			"fetcher":           "export",
			"fidelity":          "official_markdown_export",
			"content_hash":      hash,
			"export_status":     "pending",
			"export_task_id":    taskID,
			"target_format":     "markdown",
			"supported_formats": "alidoc/asheet/pdf/docx/xlsx -> markdown export",
		},
	}
	return item, hash, true, nil
}

type fileFetcher struct{}

func (fileFetcher) Fetch(
	ctx context.Context,
	cli *client,
	doc wikiNode,
	workspaceID string,
	selectedResourceID string,
) (*types.FetchedItem, string, bool, error) {
	if !doc.isDownloadableFile() {
		return nil, "", false, nil
	}

	spaceID := doc.downloadSpaceID(workspaceID)
	dentryID := doc.downloadDentryID()
	if doc.SpaceID == "" || doc.DentryID == "" {
		identity, err := cli.GetDentryIDByUUID(ctx, doc.downloadDentryUUID())
		if err != nil {
			return nil, "", true, err
		}
		spaceID = identity.SpaceID
		dentryID = identity.DentryID
	}
	info, err := cli.GetDownloadInfo(ctx, spaceID, dentryID)
	if err != nil {
		return nil, "", true, err
	}
	content, err := cli.DownloadFile(ctx, info)
	if err != nil {
		return nil, "", true, err
	}
	hash := contentHash(content)
	item := &types.FetchedItem{
		ExternalID:       makeResourceID(workspaceID, doc.NodeID),
		Title:            doc.displayName(),
		Content:          content,
		ContentType:      "application/octet-stream",
		FileName:         doc.downloadFileName(),
		URL:              doc.URL,
		UpdatedAt:        parseDingTalkTime(doc.modifiedTime()),
		SourceResourceID: selectedResourceID,
		Metadata: map[string]string{
			"channel":           types.ChannelDingtalk,
			"workspace_id":      workspaceID,
			"node_id":           doc.NodeID,
			"space_id":          spaceID,
			"dentry_id":         dentryID,
			"dentry_uuid":       doc.downloadDentryUUID(),
			"category":          doc.Category,
			"fetcher":           "file",
			"fidelity":          "original_file",
			"content_hash":      hash,
			"supported_formats": "pdf,doc,docx,xls,xlsx,ppt,pptx,txt,md,csv,images",
		},
	}
	return item, hash, true, nil
}

type blockFetcher struct{}

func (blockFetcher) Fetch(
	ctx context.Context,
	cli *client,
	doc wikiNode,
	workspaceID string,
	selectedResourceID string,
) (*types.FetchedItem, string, bool, error) {
	if !doc.isOnlineDocument() {
		return nil, "", false, nil
	}

	blocks, err := cli.QueryDocBlocks(ctx, doc.docKey())
	if err != nil {
		return nil, "", true, err
	}
	content, hash, err := renderDocumentContent(blocks)
	if err != nil {
		return nil, "", true, err
	}
	item := &types.FetchedItem{
		ExternalID:       makeResourceID(workspaceID, doc.NodeID),
		Title:            doc.displayName(),
		Content:          content,
		ContentType:      "text/markdown",
		FileName:         markdownFileName(doc.displayName()),
		URL:              doc.URL,
		UpdatedAt:        parseDingTalkTime(doc.modifiedTime()),
		SourceResourceID: selectedResourceID,
		Metadata: map[string]string{
			"channel":           types.ChannelDingtalk,
			"workspace_id":      workspaceID,
			"node_id":           doc.NodeID,
			"doc_key":           doc.docKey(),
			"category":          doc.Category,
			"fetcher":           "block",
			"fidelity":          "best_effort",
			"content_hash":      hash,
			"supported_formats": "alidoc/doc blocks -> markdown",
		},
	}
	return item, hash, true, nil
}

func (c *Connector) fetchDingTalkItem(
	ctx context.Context,
	cli *client,
	doc wikiNode,
	workspaceID string,
	selectedResourceID string,
	fetchers []dingtalkContentFetcher,
) (*types.FetchedItem, string, error) {
	for _, fetcher := range fetchers {
		item, hash, handled, err := fetcher.Fetch(ctx, cli, doc, workspaceID, selectedResourceID)
		if !handled {
			continue
		}
		if shouldFallbackFromDingTalkFetcher(fetcher, err) {
			continue
		}
		return item, hash, err
	}
	return nil, "", fmt.Errorf("unsupported dingtalk node category %q", doc.Category)
}

func shouldFallbackFromDingTalkFetcher(fetcher dingtalkContentFetcher, err error) bool {
	if err == nil {
		return false
	}
	if _, ok := fetcher.(exportFetcher); !ok {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "document.aiassistant.read")
}

func errorFetchedItem(doc wikiNode, workspaceID, selectedResourceID string, err error) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       makeResourceID(workspaceID, doc.NodeID),
		Title:            doc.displayName(),
		SourceResourceID: selectedResourceID,
		Metadata: map[string]string{
			"channel":      types.ChannelDingtalk,
			"workspace_id": workspaceID,
			"node_id":      doc.NodeID,
			"doc_key":      doc.docKey(),
			"category":     doc.Category,
			"error":        err.Error(),
		},
	}
}
