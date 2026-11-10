package types

import "fmt"

// StorageQuotaExceededError represents the storage quota exceeded error
type StorageQuotaExceededError struct {
	Message string
}

// Error implements the error interface
func (e *StorageQuotaExceededError) Error() string {
	return e.Message
}

// NewStorageQuotaExceededError creates a storage quota exceeded error
func NewStorageQuotaExceededError() *StorageQuotaExceededError {
	return &StorageQuotaExceededError{
		Message: "Storage quota exceeded",
	}
}

// DuplicateKnowledgeError duplicate knowledge error, contains the existing knowledge object
type DuplicateKnowledgeError struct {
	Message   string
	Knowledge *Knowledge
}

func (e *DuplicateKnowledgeError) Error() string {
	return e.Message
}

// NewDuplicateFileError creates a duplicate file error
func NewDuplicateFileError(knowledge *Knowledge) *DuplicateKnowledgeError {
	return &DuplicateKnowledgeError{
		Message:   fmt.Sprintf("File already exists: %s", knowledge.FileName),
		Knowledge: knowledge,
	}
}

// NewDuplicateURLError creates a duplicate URL error
func NewDuplicateURLError(knowledge *Knowledge) *DuplicateKnowledgeError {
	return &DuplicateKnowledgeError{
		Message:   fmt.Sprintf("URL already exists: %s", knowledge.Source),
		Knowledge: knowledge,
	}
}
