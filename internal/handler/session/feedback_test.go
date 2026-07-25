package session

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMessageFeedbackErrorTreatsInvalidFeedbackAsBadRequest(t *testing.T) {
	tests := []string{
		"invalid feedback action",
		"feedback can only be submitted for assistant messages",
		"feedback can only be submitted after the assistant message is completed",
	}

	for _, message := range tests {
		t.Run(message, func(t *testing.T) {
			appErr := messageFeedbackError(errors.New(message))
			assert.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
		})
	}
}
