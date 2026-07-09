package wecom_chat_archive

import (
	"context"
	"fmt"
)

type ArchiveClient interface {
	Validate(ctx context.Context) error
	FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error)
	Close() error
}

type clientFactory func(cfg *Config) ArchiveClient

func newUnavailableClient(cfg *Config) ArchiveClient {
	return unavailableClient{}
}

type unavailableClient struct{}

func (unavailableClient) Validate(ctx context.Context) error {
	return fmt.Errorf("wecom chat archive SDK client is not configured in this build")
}

func (unavailableClient) FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error) {
	return nil, false, fmt.Errorf("wecom chat archive SDK client is not configured in this build")
}

func (unavailableClient) Close() error { return nil }
