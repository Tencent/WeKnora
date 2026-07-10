package wecom_chat_archive

import "context"

type ArchiveClient interface {
	Validate(ctx context.Context) error
	FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error)
	Close() error
}

type clientFactory func(cfg *Config) ArchiveClient
