package router

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

// documentDispatcher is optional so alternate knowledge services retain their
// existing document handler while production services admit legacy queue work.
type documentDispatcher interface {
	AdmitLegacyDocument(context.Context, *asynq.Task) error
	ProcessDocumentDispatch(context.Context, *asynq.Task) error
}

type legacyDocumentFailureHandler interface {
	FailLegacyDocument(context.Context, *asynq.Task, error) (bool, error)
}

func documentTaskHandlers(svc interfaces.KnowledgeService) (asynq.HandlerFunc, asynq.HandlerFunc) {
	if scheduler, ok := svc.(documentDispatcher); ok {
		return scheduler.AdmitLegacyDocument, scheduler.ProcessDocumentDispatch
	}
	return svc.ProcessDocument, nil
}
