package dingtalk

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

type dingtalkContentFetcher interface {
	Fetch(ctx context.Context, cli *client, doc wikiNode, workspaceID, selectedResourceID string) (*types.FetchedItem, string, bool, error)
}

func defaultDingTalkContentFetchers() []dingtalkContentFetcher {
	return []dingtalkContentFetcher{
		fileFetcher{},
		blockFetcher{},
	}
}

// exportFetcher is reserved for deployments that can handle DingTalk's export
// completion event or an external exporter. It is not in the default fetcher
// chain because the official Markdown export API returns results via event
// push, while the datasource connector is a synchronous pull interface.
type exportFetcher struct{}

func (exportFetcher) Fetch(
	ctx context.Context,
	cli *client,
	doc wikiNode,
	workspaceID string,
	selectedResourceID string,
) (*types.FetchedItem, string, bool, error) {
	return nil, "", false, nil
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
) (*types.FetchedItem, string, error) {
	fetchers := c.contentFetchers
	if len(fetchers) == 0 {
		fetchers = defaultDingTalkContentFetchers()
	}
	for _, fetcher := range fetchers {
		item, hash, handled, err := fetcher.Fetch(ctx, cli, doc, workspaceID, selectedResourceID)
		if !handled {
			continue
		}
		return item, hash, err
	}
	return nil, "", fmt.Errorf("unsupported dingtalk node category %q", doc.Category)
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
