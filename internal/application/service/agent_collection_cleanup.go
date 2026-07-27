package service

import "context"

type agentCollectionCleaner interface {
	SoftDeleteByAgent(ctx context.Context, agentID string) error
	SoftDeleteByUser(ctx context.Context, userID string) error
}
