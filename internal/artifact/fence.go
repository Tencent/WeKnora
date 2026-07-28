package artifact

import (
	"context"
	"errors"
	"fmt"
)

var ErrAttemptSuperseded = errors.New("processing attempt superseded")

type LatestAttemptReader interface {
	LatestAttempt(ctx context.Context, knowledgeID string) (int, error)
}

// AttemptFence uses persisted attempt state. Artifact computation may continue
// after this returns ErrAttemptSuperseded, but live bind/publish/cleanup must not.
type AttemptFence struct {
	reader LatestAttemptReader
}

func NewAttemptFence(reader LatestAttemptReader) AttemptFence {
	return AttemptFence{reader: reader}
}

func (f AttemptFence) EnsureCurrent(
	ctx context.Context,
	knowledgeID string,
	attempt int,
) error {
	if f.reader == nil {
		return errors.New("attempt fence reader is not configured")
	}
	if knowledgeID == "" {
		return errors.New("attempt fence knowledge ID must not be empty")
	}
	if attempt <= 0 {
		return errors.New("attempt fence attempt must be positive")
	}
	latest, err := f.reader.LatestAttempt(ctx, knowledgeID)
	if err != nil {
		return fmt.Errorf("load latest processing attempt: %w", err)
	}
	if latest != attempt {
		return fmt.Errorf("%w: expected %d, latest %d", ErrAttemptSuperseded, attempt, latest)
	}
	return nil
}
