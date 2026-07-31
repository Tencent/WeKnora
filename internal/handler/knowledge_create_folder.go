package handler

import (
	"errors"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/gin-gonic/gin"
)

func writeKnowledgeCreateFolderError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, service.ErrKnowledgeFolderInvalidArgument),
		errors.Is(err, service.ErrKnowledgeFolderInvalidName),
		errors.Is(err, service.ErrKnowledgeFolderNotFound),
		errors.Is(err, service.ErrKnowledgeFolderDataIntegrity),
		errors.Is(err, service.ErrKnowledgeFolderInternal):
		writeKnowledgeFolderError(c, err)
		return true
	default:
		return false
	}
}
