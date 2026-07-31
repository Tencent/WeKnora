package im

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errIMKnowledgeCreateCaptured = errors.New("IM knowledge create captured")

type imRootFolderTenantServiceStub struct {
	interfaces.TenantService

	mu       sync.Mutex
	tenant   types.Tenant
	getCalls []uint64
}

func (s *imRootFolderTenantServiceStub) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.getCalls = append(s.getCalls, id)
	tenant := s.tenant
	return &tenant, nil
}

func (s *imRootFolderTenantServiceStub) getCallIDs() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]uint64(nil), s.getCalls...)
}

type imRootFolderCreateCall struct {
	tenantID        uint64
	hasTenantID     bool
	tenantInfoID    uint64
	tenantInfoName  string
	hasTenantInfo   bool
	knowledgeBaseID string
	fileName        string
	fileContent     []byte
	fileCaptureErr  error
	metadata        map[string]string
	enableMultimode *bool
	customFileName  string
	tagIDs          []string
	channel         string
	processOverride *types.KnowledgeProcessOverrides
	folderID        string
}

type imRootFolderKnowledgeServiceStub struct {
	interfaces.KnowledgeService

	mu    sync.Mutex
	calls []imRootFolderCreateCall
}

func (s *imRootFolderKnowledgeServiceStub) CreateKnowledgeFromFile(
	ctx context.Context,
	knowledgeBaseID string,
	file *multipart.FileHeader,
	metadata map[string]string,
	enableMultimode *bool,
	customFileName string,
	tagIDs []string,
	channel string,
	processOverride *types.KnowledgeProcessOverrides,
	folderID string,
) (*types.Knowledge, error) {
	call := imRootFolderCreateCall{
		knowledgeBaseID: knowledgeBaseID,
		customFileName:  customFileName,
		tagIDs:          append([]string(nil), tagIDs...),
		channel:         channel,
		folderID:        folderID,
	}

	call.tenantID, call.hasTenantID = types.TenantIDFromContext(ctx)
	if tenant, ok := types.TenantInfoFromContext(ctx); ok {
		call.hasTenantInfo = true
		call.tenantInfoID = tenant.ID
		call.tenantInfoName = tenant.Name
	}

	if metadata != nil {
		call.metadata = make(map[string]string, len(metadata))
		for key, value := range metadata {
			call.metadata[key] = value
		}
	}
	if enableMultimode != nil {
		value := *enableMultimode
		call.enableMultimode = &value
	}
	if processOverride != nil {
		value := *processOverride
		call.processOverride = &value
	}

	if file == nil {
		call.fileCaptureErr = errors.New("nil multipart file header")
	} else {
		call.fileName = file.Filename
		reader, err := file.Open()
		if err != nil {
			call.fileCaptureErr = err
		} else {
			call.fileContent, call.fileCaptureErr = io.ReadAll(reader)
			_ = reader.Close()
		}
	}

	s.mu.Lock()
	s.calls = append(s.calls, call)
	s.mu.Unlock()

	// Deliberately stop after argument capture. A successful return would start
	// watchAndSendSummary in a background goroutine, which this unit test avoids.
	return nil, errIMKnowledgeCreateCaptured
}

func (s *imRootFolderKnowledgeServiceStub) createCalls() []imRootFolderCreateCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	calls := make([]imRootFolderCreateCall, len(s.calls))
	copy(calls, s.calls)
	for i := range calls {
		calls[i].fileContent = append([]byte(nil), calls[i].fileContent...)
		calls[i].tagIDs = append([]string(nil), calls[i].tagIDs...)
		if calls[i].metadata != nil {
			metadata := make(map[string]string, len(calls[i].metadata))
			for key, value := range calls[i].metadata {
				metadata[key] = value
			}
			calls[i].metadata = metadata
		}
	}
	return calls
}

type imRootFolderDownloaderStub struct {
	mu sync.Mutex

	resolvedName string
	content      []byte
	calls        []imRootFolderDownloadCall
}

type imRootFolderDownloadCall struct {
	messageType MessageType
	platform    Platform
	messageID   string
	fileKey     string
	fileName    string
}

func (s *imRootFolderDownloaderStub) DownloadFile(
	_ context.Context,
	msg *IncomingMessage,
) (io.ReadCloser, string, error) {
	call := imRootFolderDownloadCall{}
	if msg != nil {
		call.messageType = msg.MessageType
		call.platform = msg.Platform
		call.messageID = msg.MessageID
		call.fileKey = msg.FileKey
		call.fileName = msg.FileName
	}

	s.mu.Lock()
	s.calls = append(s.calls, call)
	content := append([]byte(nil), s.content...)
	resolvedName := s.resolvedName
	s.mu.Unlock()

	return io.NopCloser(bytes.NewReader(content)), resolvedName, nil
}

func (s *imRootFolderDownloaderStub) downloadCalls() []imRootFolderDownloadCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]imRootFolderDownloadCall(nil), s.calls...)
}

type imRootFolderAdapterStub struct {
	Adapter

	mu      sync.Mutex
	replies []ReplyMessage
}

func (s *imRootFolderAdapterStub) SendReply(
	_ context.Context,
	_ *IncomingMessage,
	reply *ReplyMessage,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if reply == nil {
		s.replies = append(s.replies, ReplyMessage{})
		return nil
	}
	copied := *reply
	if reply.Extra != nil {
		copied.Extra = make(map[string]string, len(reply.Extra))
		for key, value := range reply.Extra {
			copied.Extra[key] = value
		}
	}
	s.replies = append(s.replies, copied)
	return nil
}

func (s *imRootFolderAdapterStub) replyCalls() []ReplyMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]ReplyMessage(nil), s.replies...)
}

func TestProcessFileToKnowledgeBasePassesExplicitRootFolderID(t *testing.T) {
	const (
		tenantID        = uint64(1701)
		knowledgeBaseID = "kb-im-root-folder"
	)

	tests := []struct {
		name         string
		messageType  MessageType
		originalName string
		resolvedName string
		content      []byte
	}{
		{
			name:         "file",
			messageType:  MessageTypeFile,
			originalName: "quarterly-report.pdf",
			resolvedName: "resolved-quarterly-report.pdf",
			content:      []byte("%PDF-1.7 IM file content"),
		},
		{
			name:         "image",
			messageType:  MessageTypeImage,
			originalName: "architecture.png",
			resolvedName: "resolved-architecture.png",
			content:      []byte("\x89PNG\r\n\x1a\nIM image content"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenantService := &imRootFolderTenantServiceStub{
				tenant: types.Tenant{ID: tenantID, Name: "IM root tenant"},
			}
			knowledgeService := &imRootFolderKnowledgeServiceStub{}
			downloader := &imRootFolderDownloaderStub{
				resolvedName: tt.resolvedName,
				content:      append([]byte(nil), tt.content...),
			}
			adapter := &imRootFolderAdapterStub{}
			service := &Service{
				tenantService:    tenantService,
				knowledgeService: knowledgeService,
			}
			msg := &IncomingMessage{
				Platform:    PlatformFeishu,
				MessageType: tt.messageType,
				UserID:      "im-user-root",
				ChatID:      "im-chat-root",
				MessageID:   "im-message-root-" + tt.name,
				FileKey:     "im-file-key-" + tt.name,
				FileName:    tt.originalName,
			}
			channel := &IMChannel{
				TenantID:        tenantID,
				Platform:        string(PlatformFeishu),
				KnowledgeBaseID: knowledgeBaseID,
				// Keep AgentID empty so the error notification uses the
				// synchronous fallback and never resolves a chat model.
			}

			service.processFileToKnowledgeBase(
				context.Background(),
				msg,
				downloader,
				adapter,
				channel,
			)

			assert.Equal(t, []uint64{tenantID}, tenantService.getCallIDs())

			downloadCalls := downloader.downloadCalls()
			require.Len(t, downloadCalls, 1)
			assert.Equal(t, tt.messageType, downloadCalls[0].messageType)
			assert.Equal(t, PlatformFeishu, downloadCalls[0].platform)
			assert.Equal(t, msg.MessageID, downloadCalls[0].messageID)
			assert.Equal(t, msg.FileKey, downloadCalls[0].fileKey)
			assert.Equal(t, tt.originalName, downloadCalls[0].fileName)

			createCalls := knowledgeService.createCalls()
			require.Len(t, createCalls, 1)
			call := createCalls[0]
			require.NoError(t, call.fileCaptureErr)
			assert.True(t, call.hasTenantID)
			assert.Equal(t, tenantID, call.tenantID)
			assert.True(t, call.hasTenantInfo)
			assert.Equal(t, tenantID, call.tenantInfoID)
			assert.Equal(t, "IM root tenant", call.tenantInfoName)
			assert.Equal(t, knowledgeBaseID, call.knowledgeBaseID)
			assert.Equal(t, tt.resolvedName, call.fileName)
			assert.Equal(t, tt.content, call.fileContent)
			assert.Nil(t, call.metadata)
			assert.Nil(t, call.enableMultimode)
			assert.Empty(t, call.customFileName)
			assert.Nil(t, call.tagIDs)
			assert.Equal(t, types.ChannelFeishu, call.channel)
			assert.Nil(t, call.processOverride)
			assert.Equal(t, "", call.folderID)

			replies := adapter.replyCalls()
			require.Len(t, replies, 1)
			assert.True(t, replies[0].IsFinal)
		})
	}
}
