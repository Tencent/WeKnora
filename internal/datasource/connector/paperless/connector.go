package paperless

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.Connector = (*Connector)(nil)

type Connector struct{}

func NewConnector() *Connector { return &Connector{} }

func (c *Connector) Type() string { return types.ConnectorTypePaperless }

func (c *Connector) Validate(ctx context.Context, ds *types.DataSourceConfig) error {
	cfg, err := parseConfig(ds)
	if err != nil {
		return err
	}
	return newClient(cfg.BaseURL, cfg.Token).ping(ctx)
}

func (c *Connector) ListResources(ctx context.Context, ds *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
	if parentID != "" {
		return []types.Resource{}, nil
	}
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg.BaseURL, cfg.Token)
	correspondents, err := cli.listCorrespondents(ctx)
	if err != nil {
		return nil, err
	}
	documentTypes, err := cli.listDocumentTypes(ctx)
	if err != nil {
		return nil, err
	}
	customFields, err := cli.listCustomFields(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]types.Resource, 0, len(correspondents)+len(documentTypes)+len(customFields))
	for _, correspondent := range correspondents {
		id := strconv.Itoa(correspondent.ID)
		out = append(out, types.Resource{
			ExternalID: "correspondent:" + id,
			Name:       correspondent.Name,
			Type:       "paperless_correspondent",
			URL:        cfg.BaseURL + "/correspondents/" + id,
		})
	}
	for _, documentType := range documentTypes {
		id := strconv.Itoa(documentType.ID)
		out = append(out, types.Resource{
			ExternalID: "document_type:" + id,
			Name:       documentType.Name,
			Type:       "paperless_document_type",
			URL:        cfg.BaseURL + "/document_types/" + id,
		})
	}
	for _, field := range customFields {
		id := strconv.Itoa(field.ID)
		out = append(out, types.Resource{
			ExternalID:  "custom_field:" + id,
			Name:        field.Name,
			Type:        "paperless_custom_field",
			Description: field.DataType,
			URL:         cfg.BaseURL + "/custom_fields/" + id,
		})
	}
	return out, nil
}

func (c *Connector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return []string{}, nil
}

func (c *Connector) FetchAll(ctx context.Context, ds *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.fetch(ctx, ds, resourceIDs, nil)
	return items, err
}

func (c *Connector) FetchIncremental(ctx context.Context, ds *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	return c.fetch(ctx, ds, ds.ResourceIDs, cursor)
}

func (c *Connector) fetch(ctx context.Context, ds *types.DataSourceConfig, resourceIDs []string, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, nil, err
	}
	cli := newClient(cfg.BaseURL, cfg.Token)
	values, err := c.queryValues(cfg, resourceIDs, cursor)
	if err != nil {
		return nil, nil, err
	}
	docs, err := cli.listDocuments(ctx, values)
	if err != nil {
		return nil, nil, err
	}
	out := make([]types.FetchedItem, 0, len(docs))
	maxModified := time.Time{}
	fieldNames := map[int]string{}
	customFieldsLoaded := false
	for _, doc := range docs {
		if doc.Modified.After(maxModified) {
			maxModified = doc.Modified.Time
		}
		if strings.TrimSpace(doc.Content) == "" || strings.TrimSpace(doc.Title) == "" || strings.TrimSpace(doc.OriginalFileName) == "" {
			detail, err := cli.document(ctx, doc.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("paperless: fetch document %d detail: %w", doc.ID, err)
			}
			if strings.TrimSpace(doc.Content) == "" {
				doc.Content = detail.Content
			}
			if strings.TrimSpace(doc.Title) == "" {
				doc.Title = detail.Title
			}
			if strings.TrimSpace(doc.OriginalFileName) == "" {
				doc.OriginalFileName = detail.OriginalFileName
			}
			if len(doc.CustomFields) == 0 {
				doc.CustomFields = detail.CustomFields
			}
		}
		if len(doc.CustomFields) > 0 && !customFieldsLoaded {
			fields, err := cli.listCustomFields(ctx)
			if err != nil {
				return nil, nil, fmt.Errorf("paperless: list custom fields: %w", err)
			}
			for _, field := range fields {
				fieldNames[field.ID] = field.Name
			}
			customFieldsLoaded = true
		}
		if strings.TrimSpace(doc.Content) == "" {
			content, contentType, err := cli.downloadDocument(ctx, doc.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("paperless: download document %d: %w", doc.ID, err)
			}
			out = append(out, fetchedOriginalItem(cfg.BaseURL, doc, content, contentType, fieldNames))
			continue
		}
		out = append(out, fetchedItem(cfg.BaseURL, doc, fieldNames))
	}
	if maxModified.IsZero() {
		maxModified = time.Now().UTC()
	}
	return out, &types.SyncCursor{LastSyncTime: maxModified, ConnectorCursor: map[string]interface{}{"last_modified": maxModified.Format(time.RFC3339Nano)}}, nil
}

func (c *Connector) queryValues(cfg *config, resourceIDs []string, cursor *types.SyncCursor) (url.Values, error) {
	values := url.Values{}
	values.Set("ordering", "modified")
	if !cfg.IncludeArchived {
		values.Set("is_archived", "false")
	}
	var correspondentIDs, documentTypeIDs []string
	for _, resourceID := range resourceIDs {
		resourceID = strings.TrimSpace(resourceID)
		switch {
		case resourceID == "" || resourceID == "all":
		case strings.HasPrefix(resourceID, "correspondent:"):
			correspondentIDs = append(correspondentIDs, strings.TrimPrefix(resourceID, "correspondent:"))
		case strings.HasPrefix(resourceID, "document_type:"):
			documentTypeIDs = append(documentTypeIDs, strings.TrimPrefix(resourceID, "document_type:"))
		default:
			return nil, fmt.Errorf("paperless: unsupported resource filter %q", resourceID)
		}
	}
	correspondentIDs = cleanStrings(correspondentIDs)
	documentTypeIDs = cleanStrings(documentTypeIDs)
	if len(correspondentIDs) > 0 {
		values.Set("correspondent__id__in", strings.Join(correspondentIDs, ","))
	}
	if len(documentTypeIDs) > 0 {
		values.Set("document_type__id__in", strings.Join(documentTypeIDs, ","))
	}
	if customFieldQuery := encodeCustomFieldQuery(cfg.CustomFieldFilters); customFieldQuery != "" {
		values.Set("custom_field_query", customFieldQuery)
	}
	if cursor != nil && !cursor.LastSyncTime.IsZero() {
		values.Set("modified__gt", cursor.LastSyncTime.Format(time.RFC3339))
	}
	return values, nil
}

func encodeCustomFieldQuery(filters []customFieldFilter) string {
	if len(filters) == 0 {
		return ""
	}
	atoms := make([][]interface{}, 0, len(filters))
	for _, filter := range filters {
		atoms = append(atoms, []interface{}{filter.FieldID, filter.Operator, filter.Value})
	}
	var query interface{} = atoms[0]
	if len(atoms) > 1 {
		query = []interface{}{"AND", atoms}
	}
	encoded, err := json.Marshal(query)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func fetchedItem(baseURL string, doc document, fieldNames map[int]string) types.FetchedItem {
	title := strings.TrimSpace(doc.Title)
	if title == "" {
		title = fmt.Sprintf("Paperless document %d", doc.ID)
	}
	return types.FetchedItem{
		ExternalID:  fmt.Sprintf("paperless:%d", doc.ID),
		Title:       title,
		Content:     []byte(doc.Content),
		ContentType: "text/plain",
		FileName:    textFileName(doc.OriginalFileName, title),
		URL:         documentURL(baseURL, doc.ID),
		UpdatedAt:   doc.Modified.Time,
		CreatedAt:   firstTime(doc.Created.Time, doc.Added.Time),
		Metadata:    documentMetadata(doc, fieldNames),
	}
}

func fetchedOriginalItem(baseURL string, doc document, content []byte, contentType string, fieldNames map[int]string) types.FetchedItem {
	title := strings.TrimSpace(doc.Title)
	if title == "" {
		title = fmt.Sprintf("Paperless document %d", doc.ID)
	}
	fileName := strings.TrimSpace(doc.OriginalFileName)
	if fileName == "" {
		fileName = title + ".pdf"
	}
	return types.FetchedItem{
		ExternalID:  fmt.Sprintf("paperless:%d", doc.ID),
		Title:       title,
		Content:     content,
		ContentType: contentType,
		FileName:    fileName,
		URL:         documentURL(baseURL, doc.ID),
		UpdatedAt:   doc.Modified.Time,
		CreatedAt:   firstTime(doc.Created.Time, doc.Added.Time),
		Metadata:    documentMetadata(doc, fieldNames),
	}
}

func textFileName(originalFileName, title string) string {
	fileName := strings.TrimSpace(originalFileName)
	if fileName == "" {
		fileName = title
	}
	if ext := filepath.Ext(fileName); ext != "" {
		fileName = strings.TrimSuffix(fileName, ext)
	}
	return fileName + ".txt"
}

func documentURL(baseURL string, id int) string {
	return strings.TrimRight(baseURL, "/") + "/documents/" + strconv.Itoa(id) + "/details"
}

func documentMetadata(doc document, fieldNames map[int]string) map[string]string {
	metadata := map[string]string{"paperless_id": strconv.Itoa(doc.ID)}
	if doc.ArchiveSerial != "" {
		metadata["archive_serial_number"] = doc.ArchiveSerial
	}
	if doc.Correspondent != 0 {
		metadata["correspondent_id"] = strconv.Itoa(doc.Correspondent)
	}
	if doc.DocumentType != 0 {
		metadata["document_type_id"] = strconv.Itoa(doc.DocumentType)
	}
	for _, field := range doc.CustomFields {
		if field.Value == nil {
			continue
		}
		name := fieldNames[field.Field]
		if name == "" {
			name = strconv.Itoa(field.Field)
		}
		metadata["custom_field_"+name] = customFieldValueString(field.Value)
	}
	return metadata
}

func customFieldValueString(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func firstTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
