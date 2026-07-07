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

// UserStorageQuotaExceededError represents a per-user storage quota
// exceeded error. It is intentionally a DISTINCT type from the
// tenant-level StorageQuotaExceededError so handlers can map them to
// different business codes and the frontend can show targeted
// messaging ("contact admin to raise your personal quota" vs "tenant
// storage is full").
type UserStorageQuotaExceededError struct {
	Message string
}

func (e *UserStorageQuotaExceededError) Error() string {
	return e.Message
}

// NewUserStorageQuotaExceededError creates a user storage quota exceeded
// error. Returns the new dedicated type so callers can errors.As it
// apart from the tenant-level quota error.
func NewUserStorageQuotaExceededError() *UserStorageQuotaExceededError {
	return &UserStorageQuotaExceededError{
		Message: "User storage quota exceeded, please contact your tenant admin to increase the storage quota",
	}
}
