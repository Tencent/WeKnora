package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// respondSuccess writes a success JSON response.
func respondSuccess(c *gin.Context, status int, data any) {
	if data == nil {
		c.Status(status)
		return
	}
	c.JSON(status, gin.H{"data": data})
}

// respondError writes an error JSON response.
func respondError(c *gin.Context, status int, message string, err error) {
	payload := gin.H{"error": message}
	if err != nil {
		payload["details"] = err.Error()
	}
	c.JSON(status, payload)
}

// respondBadRequest writes a standardized bad request response.
func respondBadRequest(c *gin.Context, message string, err error) {
	respondError(c, http.StatusBadRequest, message, err)
}

// respondInternal writes an internal server error response.
func respondInternal(c *gin.Context, err error) {
	respondError(c, http.StatusInternalServerError, "internal server error", err)
}
