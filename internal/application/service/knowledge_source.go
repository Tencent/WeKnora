package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

func knowledgeSourceRevision(k *types.Knowledge) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(k.FilePath+"\x00"+k.FileHash+"\x00"+
		k.UpdatedAt.UTC().Format(time.RFC3339Nano))))
}

func validateEditableKnowledgeSource(k *types.Knowledge) error {
	if k.Type != "file" || k.FilePath == "" {
		return werrors.NewBadRequestError("Only uploaded text files have editable source content")
	}
	switch strings.ToLower(k.FileType) {
	case "md", "markdown", "txt":
	default:
		return werrors.NewBadRequestError("Source editing supports md, markdown and txt files only")
	}
	switch k.ParseStatus {
	case types.ParseStatusCompleted, types.ParseStatusFailed, types.ParseStatusCancelled:
	default:
		return werrors.NewConflictError("Wait for parsing to finish before editing the source")
	}
	return nil
}

// GetKnowledgeSourceContent reads the exact object associated with the revision,
// rather than loading the row a second time through GetKnowledgeFile. Legacy
// non-UTF-8 sources are rejected: lossy conversion would silently corrupt them.
func (s *knowledgeService) GetKnowledgeSourceContent(
	ctx context.Context, id string,
) (*types.KnowledgeSourceContent, error) {
	k, kb, err := loadKnowledgeWrite(ctx, s.repo, s.kbService, id)
	if err != nil {
		return nil, err
	}
	if err := validateEditableKnowledgeSource(k); err != nil {
		return nil, err
	}
	if k.FileSize > types.MaxKnowledgeSourceBytes {
		return nil, werrors.NewBadRequestError("Source exceeds the 2 MiB editor limit")
	}
	file, err := s.resolveFileServiceForPath(ctx, kb, k.FilePath).GetFile(ctx, k.FilePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, types.MaxKnowledgeSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if err := validateKnowledgeSourceText(string(data)); err != nil {
		return nil, err
	}
	return &types.KnowledgeSourceContent{
		Content: string(data), Revision: knowledgeSourceRevision(k), FileName: k.FileName,
	}, nil
}

func validateKnowledgeSourceText(content string) error {
	if len(content) > types.MaxKnowledgeSourceBytes {
		return werrors.NewBadRequestError("Source exceeds the 2 MiB editor limit")
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return werrors.NewBadRequestError("Source editing requires UTF-8 text without NUL bytes")
	}
	return nil
}

// UpdateKnowledgeSourceContent keeps the document identity, overrides and
// metadata, and uses the existing replacement compensation/reparse lifecycle.
func (s *knowledgeService) UpdateKnowledgeSourceContent(
	ctx context.Context, id, content, revision string,
) (*types.Knowledge, error) {
	if revision == "" {
		return nil, werrors.NewBadRequestError("Source revision is required")
	}
	if err := validateKnowledgeSourceText(content); err != nil {
		return nil, err
	}
	if strings.TrimSpace(content) == "" {
		return nil, werrors.NewBadRequestError("Source content cannot be empty")
	}
	k, _, err := loadKnowledgeWrite(ctx, s.repo, s.kbService, id)
	if err != nil {
		return nil, err
	}
	if err := validateEditableKnowledgeSource(k); err != nil {
		return nil, err
	}
	file, err := bytesToFileHeader([]byte(content), k.FileName)
	if err != nil {
		return nil, err
	}
	updated, err := s.replaceKnowledgeFile(ctx, id, file, k.FileName, nil, revision)
	var duplicate *types.DuplicateKnowledgeError
	if errors.As(err, &duplicate) {
		return updated, nil
	}
	return updated, err
}
