package types

import "fmt"

// StorageQuotaExceededError 저장소 할당량 초과 오류를 나타냅니다.
type StorageQuotaExceededError struct {
	Message string
}

// Error 오류 인터페이스를 구현합니다.
func (e *StorageQuotaExceededError) Error() string {
	return e.Message
}

// NewStorageQuotaExceededError 저장소 할당량 초과 오류를 생성합니다.
func NewStorageQuotaExceededError() *StorageQuotaExceededError {
	return &StorageQuotaExceededError{
		Message: "저장소 할당량이 초과되었습니다",
	}
}

// DuplicateKnowledgeError 중복 지식 오류, 기존 지식 객체를 포함합니다.
type DuplicateKnowledgeError struct {
	Message   string
	Knowledge *Knowledge
}

func (e *DuplicateKnowledgeError) Error() string {
	return e.Message
}

// NewDuplicateFileError 중복 파일 오류를 생성합니다.
func NewDuplicateFileError(knowledge *Knowledge) *DuplicateKnowledgeError {
	return &DuplicateKnowledgeError{
		Message:   fmt.Sprintf("파일이 이미 존재합니다: %s", knowledge.FileName),
		Knowledge: knowledge,
	}
}

// NewDuplicateURLError 중복 URL 오류를 생성합니다.
func NewDuplicateURLError(knowledge *Knowledge) *DuplicateKnowledgeError {
	return &DuplicateKnowledgeError{
		Message:   fmt.Sprintf("URL이 이미 존재합니다: %s", knowledge.Source),
		Knowledge: knowledge,
	}
}
