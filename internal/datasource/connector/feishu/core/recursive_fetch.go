package core

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// RecursiveFetchOptions 描述一次递归抓取的上下文。
type RecursiveFetchOptions struct {
	SourceResourceID string
	ParentExternalID string
	Channel          string
	ParseMode        string
	MaxDepth         int

	// State 在同一次数据源同步中共享，避免多个父节点指向同一资源时重复抓取。
	State *RecursiveState

	// SeedVisited 用于把当前父资源预先标记为已访问，
	// 防止 A -> B -> A 这种循环重新把父资源抓回来。
	SeedVisited []LinkedResource
}

// RecursiveState 保存一次同步期间的全局递归去重状态。
type RecursiveState struct {
	Visited  map[string]struct{}
	Emitted  map[string]struct{}
	Present  map[string]struct{}
	Previous map[string]struct{}

	// Incomplete 表示本轮有资源/表格无法完整枚举。
	// 此时禁止执行递归删除，避免因临时权限/API异常误删旧知识。
	Incomplete bool
}

// NewRecursiveState initializes the per-sync deduplication and deletion state.
func NewRecursiveState() *RecursiveState {
	return &RecursiveState{
		Visited:  make(map[string]struct{}),
		Emitted:  make(map[string]struct{}),
		Present:  make(map[string]struct{}),
		Previous: make(map[string]struct{}),
	}
}

// LoadPrevious restores the last traversal's deletion identities.
func (s *RecursiveState) LoadPrevious(ids []string) {
	if s == nil {
		return
	}
	if s.Previous == nil {
		s.Previous = make(map[string]struct{})
	}
	for _, id := range ids {
		if id != "" {
			s.Previous[id] = struct{}{}
		}
	}
}

// MarkIncomplete suppresses deletion after a partial discovery.
func (s *RecursiveState) MarkIncomplete() {
	if s != nil {
		s.Incomplete = true
	}
}

// CursorIDs includes old and current identities for resumable checkpoints.
func (s *RecursiveState) CursorIDs() []string {
	if s == nil {
		return nil
	}

	ids := make(map[string]struct{})

	// Checkpoint 阶段必须保留 Previous，
	// 否则同步中途崩溃会丢失尚未处理资源的删除状态。
	for id := range s.Previous {
		ids[id] = struct{}{}
	}
	for id := range s.Present {
		ids[id] = struct{}{}
	}

	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// CommitPresent replaces the baseline only after a complete traversal.
func (s *RecursiveState) CommitPresent() {
	if s == nil {
		return
	}

	s.Previous = make(map[string]struct{}, len(s.Present))
	for id := range s.Present {
		s.Previous[id] = struct{}{}
	}
}

// PreservePreviousAndPresent retains both sets after incomplete traversal.
func (s *RecursiveState) PreservePreviousAndPresent() {
	if s == nil {
		return
	}
	if s.Previous == nil {
		s.Previous = make(map[string]struct{})
	}
	for id := range s.Present {
		s.Previous[id] = struct{}{}
	}
}

type recursiveFetcher struct {
	client  *Client
	options RecursiveFetchOptions
	state   *RecursiveState

	visited map[string]struct{}
	emitted map[string]struct{}

	items []*types.FetchedItem
}

func recursiveTraversalKey(r LinkedResource) string {
	return strings.Join(
		[]string{
			r.Type,
			r.Token,
			r.TableID,
			r.ViewID,
		},
		"|",
	)
}

func recursiveExternalID(r LinkedResource) string {
	return "recursive:" + r.Type + ":" + r.Token
}

// DiscoverDirectLinkedResources 只扫描当前 Bitable / Sheet 的直接子链接，
// 不抓取内容、不继续递归。供 Wiki / Drive connector 接入递归入口时使用。
func DiscoverDirectLinkedResources(
	ctx context.Context,
	client *Client,
	resource LinkedResource,
) ([]LinkedResource, error) {
	r := &recursiveFetcher{client: client}

	switch resource.Type {
	case "bitable":
		return r.discoverBitable(ctx, resource)
	case "sheet":
		return r.discoverSheet(ctx, resource)
	default:
		return nil, nil
	}
}

// FetchRecursiveLinkedResources 从若干已发现的飞书链接开始递归抓取。
// depth 从 1 开始：调用方自身是 depth=0，链接出去的第一层是 depth=1。
//
// 单个资源失败不会中断整棵递归树；失败会转成 FetchedItem，最终在
// WeKnora 同步日志中显示 partial / failed item。
func FetchRecursiveLinkedResources(
	ctx context.Context,
	client *Client,
	roots []LinkedResource,
	options RecursiveFetchOptions,
) ([]*types.FetchedItem, error) {
	if len(roots) == 0 {
		return nil, nil
	}

	if options.MaxDepth <= 0 {
		options.MaxDepth = RecursiveMaxDepth()
	}
	if options.MaxDepth > maxRecursiveMaxDepth {
		options.MaxDepth = maxRecursiveMaxDepth
	}

	state := options.State
	if state == nil {
		state = NewRecursiveState()
	}
	if state.Visited == nil {
		state.Visited = make(map[string]struct{})
	}
	if state.Emitted == nil {
		state.Emitted = make(map[string]struct{})
	}
	if state.Present == nil {
		state.Present = make(map[string]struct{})
	}
	if state.Previous == nil {
		state.Previous = make(map[string]struct{})
	}

	for _, seed := range options.SeedVisited {
		state.Visited[recursiveTraversalKey(seed)] = struct{}{}
	}

	r := &recursiveFetcher{
		client:  client,
		options: options,
		state:   state,
		visited: state.Visited,
		emitted: state.Emitted,
		items:   make([]*types.FetchedItem, 0),
	}

	for _, root := range roots {
		if err := r.walk(
			ctx,
			root,
			1,
			options.ParentExternalID,
		); err != nil {
			return nil, err
		}
	}

	return r.items, nil
}

func (r *recursiveFetcher) walk(
	ctx context.Context,
	resource LinkedResource,
	depth int,
	parentExternalID string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if depth > r.options.MaxDepth {
		return nil
	}

	originalURL := resource.URL
	title := resource.Token
	editTime := time.Time{}

	// Wiki URL 是包装层。先用 node_token 去重，避免同一个 Wiki 链接
	// 被重复解析；随后解析成真实 obj_type + obj_token，并从此使用
	// 真实资源身份作为 canonical external_id。
	if resource.Type == "wiki" {
		wikiKey := recursiveTraversalKey(resource)

		if _, ok := r.visited[wikiKey]; ok {
			return nil
		}
		r.visited[wikiKey] = struct{}{}

		failureExternalID := recursiveExternalID(resource)

		node, err := r.client.GetWikiNode(
			ctx,
			"",
			resource.Token,
		)
		if err != nil {
			if r.state != nil {
				r.state.MarkIncomplete()
			}

			r.appendFailure(
				resource,
				failureExternalID,
				parentExternalID,
				depth,
				fmt.Errorf("resolve wiki node: %w", err),
			)
			return nil
		}

		if node.Title != "" {
			title = node.Title
		}

		if node.ObjEditTime != "" {
			editTime = ParseFeishuTimestamp(node.ObjEditTime)
		} else {
			editTime = ParseFeishuTimestamp(node.NodeEditTime)
		}

		resource = LinkedResource{
			Type:    node.ObjType,
			Token:   node.ObjToken,
			URL:     originalURL,
			TableID: resource.TableID,
			ViewID:  resource.ViewID,
		}

		if resource.Type == "" || resource.Token == "" {
			if r.state != nil {
				r.state.MarkIncomplete()
			}

			r.appendFailure(
				resource,
				failureExternalID,
				parentExternalID,
				depth,
				fmt.Errorf("wiki node missing obj_type/obj_token"),
			)
			return nil
		}

		// Wiki 包装地址和直接 /docx /base /sheets 地址最终都落到
		// 相同的真实资源 key。
		resolvedKey := recursiveTraversalKey(resource)

		// 无论是否已经通过另一种 URL 访问过，都先登记本轮仍存在。
		externalID := recursiveExternalID(resource)
		if r.state != nil {
			r.state.Present[externalID] = struct{}{}
		}

		if _, exists := r.visited[resolvedKey]; exists {
			return nil
		}
		r.visited[resolvedKey] = struct{}{}

		return r.fetchResolved(
			ctx,
			resource,
			depth,
			parentExternalID,
			externalID,
			title,
			editTime,
			originalURL,
		)
	}

	// 非 Wiki URL 本身就是真实资源身份。
	externalID := recursiveExternalID(resource)

	if r.state != nil {
		r.state.Present[externalID] = struct{}{}
	}

	key := recursiveTraversalKey(resource)
	if _, ok := r.visited[key]; ok {
		return nil
	}
	r.visited[key] = struct{}{}

	return r.fetchResolved(
		ctx,
		resource,
		depth,
		parentExternalID,
		externalID,
		title,
		editTime,
		originalURL,
	)
}

func (r *recursiveFetcher) fetchResolved(
	ctx context.Context,
	resource LinkedResource,
	depth int,
	parentExternalID string,
	externalID string,
	title string,
	editTime time.Time,
	originalURL string,
) error {
	baseMeta := map[string]string{
		"channel":            r.options.Channel,
		"recursive":          "true",
		"recursive_depth":    strconv.Itoa(depth),
		"parent_external_id": parentExternalID,
		"obj_type":           resource.Type,
		"obj_token":          resource.Token,
		"source_url":         originalURL,
	}

	// 先抓取当前真实资源本身。
	if err := r.fetchCurrent(
		ctx,
		resource,
		externalID,
		title,
		editTime,
		baseMeta,
	); err != nil {
		r.appendFailure(
			resource,
			externalID,
			parentExternalID,
			depth,
			err,
		)

		// 导出失败不一定意味着 records / sheet values API 同样失败，
		// 因此仍尝试继续发现下一级链接。
	}

	if depth >= r.options.MaxDepth {
		return nil
	}

	switch resource.Type {
	case "bitable":
		children, err := r.discoverBitable(
			ctx,
			resource,
		)
		if err != nil {
			if r.state != nil {
				r.state.MarkIncomplete()
			}

			r.appendDiscoveryFailure(
				resource,
				externalID,
				depth,
				err,
			)
			return nil
		}

		for _, child := range children {
			if err := r.walk(
				ctx,
				child,
				depth+1,
				externalID,
			); err != nil {
				return err
			}
		}

	case "sheet":
		children, err := r.discoverSheet(
			ctx,
			resource,
		)
		if err != nil {
			if r.state != nil {
				r.state.MarkIncomplete()
			}

			r.appendDiscoveryFailure(
				resource,
				externalID,
				depth,
				err,
			)
			return nil
		}

		for _, child := range children {
			if err := r.walk(
				ctx,
				child,
				depth+1,
				externalID,
			); err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *recursiveFetcher) fetchCurrent(
	ctx context.Context,
	resource LinkedResource,
	externalID string,
	title string,
	editTime time.Time,
	baseMeta map[string]string,
) error {
	switch resource.Type {
	case "docx":
		items, err := FetchDocxWithBlocks(
			ctx,
			r.client,
			DocxFetchInput{
				DocToken:   externalID,
				ObjToken:   resource.Token,
				Title:      title,
				URL:        resource.URL,
				ResourceID: r.options.SourceResourceID,
				EditTime:   editTime,
				BaseMeta:   baseMeta,
				ParseMode:  r.options.ParseMode,
			},
		)
		if err != nil {
			return err
		}

		for _, item := range items {
			r.appendItem(item)
		}
		return nil

	case "doc", "sheet", "bitable":
		data, fileName, err := r.client.ExportAndDownload(
			ctx,
			resource.Token,
			resource.Type,
		)
		if err != nil {
			return fmt.Errorf(
				"export recursive %s: %w",
				resource.Type,
				err,
			)
		}
		fileName = SanitizeFileName(fileName)

		ext := ExportFileExtToSuffix[ObjTypeToExportFileExtension[resource.Type]]

		if fileName == "" {
			fileName = SanitizeFileName(title) + ext
		} else if !strings.HasSuffix(
			strings.ToLower(fileName),
			ext,
		) {
			fileName = SanitizeFileName(fileName) + ext
		}

		if title == "" || title == resource.Token {
			title = strings.TrimSuffix(fileName, ext)
		}

		r.appendItem(&types.FetchedItem{
			ExternalID:       externalID,
			Title:            title,
			Content:          data,
			ContentType:      "application/octet-stream",
			FileName:         fileName,
			URL:              resource.URL,
			UpdatedAt:        editTime,
			SourceResourceID: r.options.SourceResourceID,
			Metadata:         baseMeta,
		})

		return nil

	case "file":
		// 直接 /file/<token> URL 缺少可靠文件名/扩展名；
		// 当前先不入库，避免错误扩展名导致解析失败。
		// Drive 树中的 file 仍由官方 Drive connector 正常处理。
		logger.Infof(
			ctx,
			"[FeishuRecursive] skip direct file token=%s: no reliable filename",
			resource.Token,
		)
		return nil

	case "folder":
		// 文件夹链接由 Drive connector 的目录树负责；
		// 递归 URL 层暂不二次展开 Drive 文件夹。
		return nil

	default:
		logger.Infof(
			ctx,
			"[FeishuRecursive] unsupported linked type=%s token=%s",
			resource.Type,
			resource.Token,
		)
		return nil
	}
}

func (r *recursiveFetcher) discoverBitable(
	ctx context.Context,
	resource LinkedResource,
) ([]LinkedResource, error) {
	tableIDs := make([]string, 0)

	if resource.TableID != "" {
		tableIDs = append(tableIDs, resource.TableID)
	} else {
		tables, err := r.client.ListBitableTables(
			ctx,
			resource.Token,
		)
		if err != nil {
			return nil, err
		}

		for _, table := range tables {
			if table.TableID != "" {
				tableIDs = append(
					tableIDs,
					table.TableID,
				)
			}
		}
	}

	var children []LinkedResource

	for _, tableID := range tableIDs {
		rows, err := r.client.ReadBitableRawRecords(
			ctx,
			resource.Token,
			tableID,
			resource.ViewID,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"read bitable table app=%s table=%s: %w",
				resource.Token,
				tableID,
				err,
			)
		}

		found := DiscoverLinkedResources(rows)
		children = append(children, found...)
	}

	return deduplicateLinkedResources(children), nil
}

func (r *recursiveFetcher) discoverSheet(
	ctx context.Context,
	resource LinkedResource,
) ([]LinkedResource, error) {
	tabs, err := r.client.ListSheetTabs(
		ctx,
		resource.Token,
	)
	if err != nil {
		return nil, err
	}

	var children []LinkedResource

	for _, tab := range tabs {
		if tab.SheetID == "" {
			continue
		}

		values, err := r.client.ReadSheetRawValues(
			ctx,
			resource.Token,
			tab.SheetID,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"read sheet spreadsheet=%s sheet=%s: %w",
				resource.Token,
				tab.SheetID,
				err,
			)
		}

		found := DiscoverLinkedResources(values)
		children = append(children, found...)
	}

	return deduplicateLinkedResources(children), nil
}

func deduplicateLinkedResources(
	in []LinkedResource,
) []LinkedResource {
	seen := make(map[string]struct{})
	out := make([]LinkedResource, 0, len(in))

	for _, item := range in {
		key := recursiveTraversalKey(item)

		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		out = append(out, item)
	}

	return out
}

func (r *recursiveFetcher) appendItem(
	item *types.FetchedItem,
) {
	if item == nil || item.ExternalID == "" {
		return
	}

	if _, ok := r.emitted[item.ExternalID]; ok {
		return
	}

	r.emitted[item.ExternalID] = struct{}{}
	r.items = append(r.items, item)
}

func (r *recursiveFetcher) appendFailure(
	resource LinkedResource,
	externalID string,
	parentExternalID string,
	depth int,
	err error,
) {
	if err == nil {
		return
	}

	logger.Warnf(
		context.Background(),
		"[FeishuRecursive] resource failed type=%s token=%s depth=%d: %v",
		resource.Type,
		resource.Token,
		depth,
		err,
	)

	r.appendItem(&types.FetchedItem{
		ExternalID:       externalID + "#recursive-error",
		Title:            resource.Token,
		SourceResourceID: r.options.SourceResourceID,
		Metadata: FeishuErrorItemMeta(
			err,
			map[string]string{
				"channel":            r.options.Channel,
				"recursive":          "true",
				"recursive_depth":    strconv.Itoa(depth),
				"parent_external_id": parentExternalID,
				"obj_type":           resource.Type,
				"obj_token":          resource.Token,
				"failure_stage":      "recursive_fetch",
			},
		),
	})
}

func (r *recursiveFetcher) appendDiscoveryFailure(
	resource LinkedResource,
	externalID string,
	depth int,
	err error,
) {
	if err == nil {
		return
	}

	logger.Warnf(
		context.Background(),
		"[FeishuRecursive] discovery failed type=%s token=%s depth=%d: %v",
		resource.Type,
		resource.Token,
		depth,
		err,
	)

	r.appendItem(&types.FetchedItem{
		ExternalID:       externalID + "#recursive-discovery-error",
		Title:            resource.Token,
		SourceResourceID: r.options.SourceResourceID,
		Metadata: FeishuErrorItemMeta(
			err,
			map[string]string{
				"channel":         r.options.Channel,
				"recursive":       "true",
				"recursive_depth": strconv.Itoa(depth),
				"obj_type":        resource.Type,
				"obj_token":       resource.Token,
				"failure_stage":   "recursive_discovery",
			},
		),
	})
}
