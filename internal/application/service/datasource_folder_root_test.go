package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dataSourceRootFileCreateCall struct {
	knowledgeBaseID     string
	fileName            string
	fileSize            int64
	fileContent         []byte
	metadata            map[string]string
	enableMultimodelSet bool
	enableMultimodel    bool
	customFileName      string
	tagIDs              []string
	channel             string
	processOverridesSet bool
	folderID            string
}

type dataSourceRootURLCreateCall struct {
	knowledgeBaseID     string
	url                 string
	fileName            string
	fileType            string
	enableMultimodelSet bool
	enableMultimodel    bool
	title               string
	tagIDs              []string
	channel             string
	processOverridesSet bool
	folderID            string
}

type dataSourceRootKnowledgeServiceStub struct {
	interfaces.KnowledgeService

	mu                 sync.Mutex
	repository         interfaces.KnowledgeRepository
	fileErr            error
	urlErr             error
	fileCalls          []dataSourceRootFileCreateCall
	urlCalls           []dataSourceRootURLCreateCall
	getRepositoryCalls int
	deleteCalls        []string
}

func (s *dataSourceRootKnowledgeServiceStub) CreateKnowledgeFromFile(
	_ context.Context,
	knowledgeBaseID string,
	file *multipart.FileHeader,
	metadata map[string]string,
	enableMultimodel *bool,
	customFileName string,
	tagIDs []string,
	channel string,
	processOverrides *types.KnowledgeProcessOverrides,
	folderID string,
) (*types.Knowledge, error) {
	fileName, fileSize, fileContent, err := readDataSourceRootFileHeader(file)
	if err != nil {
		return nil, err
	}

	call := dataSourceRootFileCreateCall{
		knowledgeBaseID:     knowledgeBaseID,
		fileName:            fileName,
		fileSize:            fileSize,
		fileContent:         append([]byte(nil), fileContent...),
		metadata:            cloneDataSourceRootStringMap(metadata),
		customFileName:      customFileName,
		tagIDs:              append([]string(nil), tagIDs...),
		channel:             channel,
		processOverridesSet: processOverrides != nil,
		folderID:            folderID,
	}
	if enableMultimodel != nil {
		call.enableMultimodelSet = true
		call.enableMultimodel = *enableMultimodel
	}

	s.mu.Lock()
	s.fileCalls = append(s.fileCalls, call)
	createErr := s.fileErr
	s.mu.Unlock()

	if createErr != nil {
		return nil, createErr
	}
	return &types.Knowledge{ID: "captured-file-knowledge"}, nil
}

func (s *dataSourceRootKnowledgeServiceStub) CreateKnowledgeFromURL(
	_ context.Context,
	knowledgeBaseID string,
	rawURL string,
	fileName string,
	fileType string,
	enableMultimodel *bool,
	title string,
	tagIDs []string,
	channel string,
	processOverrides *types.KnowledgeProcessOverrides,
	folderID string,
) (*types.Knowledge, error) {
	call := dataSourceRootURLCreateCall{
		knowledgeBaseID:     knowledgeBaseID,
		url:                 rawURL,
		fileName:            fileName,
		fileType:            fileType,
		title:               title,
		tagIDs:              append([]string(nil), tagIDs...),
		channel:             channel,
		processOverridesSet: processOverrides != nil,
		folderID:            folderID,
	}
	if enableMultimodel != nil {
		call.enableMultimodelSet = true
		call.enableMultimodel = *enableMultimodel
	}

	s.mu.Lock()
	s.urlCalls = append(s.urlCalls, call)
	createErr := s.urlErr
	s.mu.Unlock()

	if createErr != nil {
		return nil, createErr
	}
	return &types.Knowledge{ID: "captured-url-knowledge"}, nil
}

func (s *dataSourceRootKnowledgeServiceStub) GetRepository() interfaces.KnowledgeRepository {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getRepositoryCalls++
	return s.repository
}

func (s *dataSourceRootKnowledgeServiceStub) DeleteKnowledge(_ context.Context, knowledgeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls = append(s.deleteCalls, knowledgeID)
	return fmt.Errorf("unexpected DeleteKnowledge call for %s", knowledgeID)
}

func (s *dataSourceRootKnowledgeServiceStub) snapshot() (
	[]dataSourceRootFileCreateCall,
	[]dataSourceRootURLCreateCall,
	int,
	[]string,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]dataSourceRootFileCreateCall(nil), s.fileCalls...),
		append([]dataSourceRootURLCreateCall(nil), s.urlCalls...),
		s.getRepositoryCalls,
		append([]string(nil), s.deleteCalls...)
}

var _ interfaces.KnowledgeService = (*dataSourceRootKnowledgeServiceStub)(nil)

type dataSourceRootFindCall struct {
	tenantID        uint64
	knowledgeBaseID string
	key             string
	value           string
}

type dataSourceRootKnowledgeRepositoryStub struct {
	interfaces.KnowledgeRepository

	mu        sync.Mutex
	findCalls []dataSourceRootFindCall
	existing  *types.Knowledge
	findErr   error
}

func (r *dataSourceRootKnowledgeRepositoryStub) FindByMetadataKey(
	_ context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	key string,
	value string,
) (*types.Knowledge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.findCalls = append(r.findCalls, dataSourceRootFindCall{
		tenantID:        tenantID,
		knowledgeBaseID: knowledgeBaseID,
		key:             key,
		value:           value,
	})
	if r.existing == nil {
		return nil, r.findErr
	}
	existingCopy := *r.existing
	return &existingCopy, r.findErr
}

func (r *dataSourceRootKnowledgeRepositoryStub) snapshot() []dataSourceRootFindCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]dataSourceRootFindCall(nil), r.findCalls...)
}

var _ interfaces.KnowledgeRepository = (*dataSourceRootKnowledgeRepositoryStub)(nil)

func TestDataSourceIngestItemPassesRootFolderToFileCreate(t *testing.T) {
	repository := &dataSourceRootKnowledgeRepositoryStub{}
	knowledgeService := &dataSourceRootKnowledgeServiceStub{repository: repository}
	service := &DataSourceService{knowledgeService: knowledgeService}
	dataSource := newDataSourceRootTestDataSource()
	item := &types.FetchedItem{
		ExternalID:       "connector-file-external-id",
		Title:            "Connector file title",
		Content:          []byte("connector file body"),
		ContentType:      "text/markdown",
		FileName:         "connector-document.md",
		SourceResourceID: "source-folder-1",
		Metadata: map[string]string{
			"description": "original connector description",
			"custom":      "custom metadata",
		},
	}
	tagIDs := []string{"tag-1", "tag-2"}

	isUpdate, err := service.ingestItem(context.Background(), dataSource, item, tagIDs)

	require.NoError(t, err)
	assert.False(t, isUpdate)

	fileCalls, urlCalls, getRepositoryCalls, deleteCalls := knowledgeService.snapshot()
	require.Len(t, fileCalls, 1)
	assert.Empty(t, urlCalls)
	assert.Equal(t, 1, getRepositoryCalls)
	assert.Empty(t, deleteCalls)

	call := fileCalls[0]
	assert.Equal(t, dataSource.KnowledgeBaseID, call.knowledgeBaseID)
	assert.Equal(t, item.FileName, call.fileName)
	assert.Equal(t, int64(len(item.Content)), call.fileSize)
	assert.Equal(t, item.Content, call.fileContent)
	assert.Equal(t, item.FileName, call.customFileName)
	assert.Equal(t, tagIDs, call.tagIDs)
	assert.Equal(t, dataSource.Type, call.channel)
	assert.False(t, call.enableMultimodelSet)
	assert.False(t, call.processOverridesSet)
	assert.Equal(t, "", call.folderID)
	assert.Equal(t, map[string]string{
		"external_id":        item.ExternalID,
		"source_resource_id": item.SourceResourceID,
		"datasource_id":      dataSource.ID,
		"description":        "original connector description",
		"custom":             "custom metadata",
	}, call.metadata)

	assert.Equal(t, []dataSourceRootFindCall{{
		tenantID:        dataSource.TenantID,
		knowledgeBaseID: dataSource.KnowledgeBaseID,
		key:             "external_id",
		value:           item.ExternalID,
	}}, repository.snapshot())
}

func TestDataSourceIngestItemPassesRootFolderToURLCreate(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		fileName string
		title    string
	}{
		{
			name:   "ordinary web URL",
			rawURL: "https://connector.example.invalid/articles/root-folder",
			title:  "Connector web page",
		},
		{
			name:     "remote file URL",
			rawURL:   "https://connector.example.invalid/files/report.pdf",
			fileName: "report.pdf",
			title:    "Connector PDF",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &dataSourceRootKnowledgeRepositoryStub{}
			knowledgeService := &dataSourceRootKnowledgeServiceStub{repository: repository}
			service := &DataSourceService{knowledgeService: knowledgeService}
			dataSource := newDataSourceRootTestDataSource()
			item := &types.FetchedItem{
				ExternalID:       "connector-url-external-id",
				Title:            test.title,
				FileName:         test.fileName,
				URL:              test.rawURL,
				SourceResourceID: "source-folder-2",
			}
			tagIDs := []string{"tag-url"}

			isUpdate, err := service.ingestItem(context.Background(), dataSource, item, tagIDs)

			require.NoError(t, err)
			assert.False(t, isUpdate)

			fileCalls, urlCalls, getRepositoryCalls, deleteCalls := knowledgeService.snapshot()
			assert.Empty(t, fileCalls)
			require.Len(t, urlCalls, 1)
			assert.Equal(t, 1, getRepositoryCalls)
			assert.Empty(t, deleteCalls)

			call := urlCalls[0]
			assert.Equal(t, dataSource.KnowledgeBaseID, call.knowledgeBaseID)
			assert.Equal(t, item.URL, call.url)
			assert.Equal(t, item.FileName, call.fileName)
			assert.Equal(t, "", call.fileType)
			assert.Equal(t, item.Title, call.title)
			assert.Equal(t, tagIDs, call.tagIDs)
			assert.Equal(t, dataSource.Type, call.channel)
			assert.False(t, call.enableMultimodelSet)
			assert.False(t, call.processOverridesSet)
			assert.Equal(t, "", call.folderID)

			assert.Equal(t, []dataSourceRootFindCall{{
				tenantID:        dataSource.TenantID,
				knowledgeBaseID: dataSource.KnowledgeBaseID,
				key:             "external_id",
				value:           item.ExternalID,
			}}, repository.snapshot())
		})
	}
}

func TestDataSourceIngestItemPreservesInvalidURLSentinel(t *testing.T) {
	knowledgeService := &dataSourceRootKnowledgeServiceStub{urlErr: ErrInvalidURL}
	service := &DataSourceService{knowledgeService: knowledgeService}
	dataSource := newDataSourceRootTestDataSource()
	item := &types.FetchedItem{
		Title: "Unsafe connector URL",
		URL:   "http://127.0.0.1/private",
	}

	isUpdate, err := service.ingestItem(context.Background(), dataSource, item, []string{"tag-url"})

	require.Error(t, err)
	assert.Same(t, ErrInvalidURL, err)
	assert.True(t, errors.Is(err, ErrInvalidURL))
	assert.False(t, isUpdate)

	fileCalls, urlCalls, getRepositoryCalls, deleteCalls := knowledgeService.snapshot()
	assert.Empty(t, fileCalls)
	require.Len(t, urlCalls, 1)
	assert.Equal(t, "", urlCalls[0].folderID)
	assert.Equal(t, 0, getRepositoryCalls)
	assert.Empty(t, deleteCalls)
}

func newDataSourceRootTestDataSource() *types.DataSource {
	return &types.DataSource{
		ID:              "datasource-1",
		TenantID:        42,
		KnowledgeBaseID: "knowledge-base-1",
		Type:            "test-connector",
		Config: types.JSON([]byte(
			`{"settings":{"folder_id":"must-not-be-read-by-ingest-item"}}`,
		)),
	}
}

func readDataSourceRootFileHeader(file *multipart.FileHeader) (string, int64, []byte, error) {
	if file == nil {
		return "", 0, nil, errors.New("captured nil file header")
	}
	opened, err := file.Open()
	if err != nil {
		return "", 0, nil, fmt.Errorf("open captured file header: %w", err)
	}
	content, readErr := io.ReadAll(opened)
	closeErr := opened.Close()
	if readErr != nil {
		return "", 0, nil, fmt.Errorf("read captured file header: %w", readErr)
	}
	if closeErr != nil {
		return "", 0, nil, fmt.Errorf("close captured file header: %w", closeErr)
	}
	return file.Filename, file.Size, content, nil
}

func cloneDataSourceRootStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
