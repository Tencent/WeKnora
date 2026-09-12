package modelobs

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/Tencent/WeKnora/internal/models/call"
	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

func finishProviderCall(
	ctx context.Context,
	call *activeCall,
	strict bool,
	status string,
	providerErr error,
	usage *types.TokenUsage,
) error {
	finishErr := call.finish(ctx, status, providerErr, usage)
	if finishErr == nil {
		return providerErr
	}
	if strict {
		accountingErr := types.RecordModelAccountingError(ctx, finishErr)
		return errors.Join(providerErr, accountingErr)
	}
	logger.Errorf(ctx, "[modelobs] terminal accounting failed: %v", finishErr)
	return providerErr
}

type observedChat struct {
	recorder *Recorder
	model    *types.Model
	inner    chat.Chat
}

// WrapChat records unary and streaming chat calls.
func (r *Recorder) WrapChat(model *types.Model, inner chat.Chat) chat.Chat {
	if r == nil || r.store == nil || inner == nil {
		return inner
	}
	return &observedChat{recorder: r, model: model, inner: inner}
}

func (o *observedChat) Chat(
	ctx context.Context,
	messages []chat.Message,
	opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	if supported, ok := o.inner.(interface{ RequestAccountingSupported() bool }); ok &&
		supported.RequestAccountingSupported() {
		return o.inner.Chat(o.recorder.attemptContext(ctx, o.model), messages, opts)
	}
	call, strict, err := o.recorder.start(ctx, o.model, "chat")
	if err != nil && strict {
		return nil, err
	}
	response, providerErr := o.inner.Chat(ctx, messages, opts)
	var usage *types.TokenUsage
	if response != nil {
		usage = &response.Usage
	}
	return response, finishProviderCall(
		ctx, call, strict, statusForError(providerErr), providerErr, usage,
	)
}

func (o *observedChat) ChatStream(
	ctx context.Context,
	messages []chat.Message,
	opts *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	if supported, ok := o.inner.(interface{ RequestAccountingSupported() bool }); ok &&
		supported.RequestAccountingSupported() {
		return o.inner.ChatStream(o.recorder.attemptContext(ctx, o.model), messages, opts)
	}
	call, strict, err := o.recorder.start(ctx, o.model, "chat_stream")
	if err != nil && strict {
		return nil, err
	}
	upstream, providerErr := o.inner.ChatStream(ctx, messages, opts)
	if providerErr != nil {
		return nil, finishProviderCall(
			ctx, call, strict, statusForError(providerErr), providerErr, nil,
		)
	}
	output := make(chan types.StreamResponse)
	go func() {
		defer close(output)
		status := types.ModelCallStatusSuccess
		var terminalErr error
		var usage *types.TokenUsage
		var terminal *types.StreamResponse
		send := func(r types.StreamResponse) bool {
			select {
			case output <- r:
				return true
			case <-ctx.Done():
				return false
			}
		}
		defer func() {
			if terminal == nil && terminalErr == nil {
				terminalErr = errors.New("provider stream closed without a terminal response")
				status = types.ModelCallStatusError
			}
			finishErr := call.finish(ctx, status, terminalErr, usage)
			if finishErr != nil && strict {
				_ = types.RecordModelAccountingError(ctx, finishErr)
				send(types.StreamResponse{
					ResponseType: types.ResponseTypeError, Content: "model call accounting failed", Done: true,
					Usage: usage, FinishReason: types.FinishReasonIncomplete,
					Data: map[string]interface{}{"error_code": "model_call_accounting_failed"},
				})
				return
			}
			if finishErr != nil {
				logger.Errorf(ctx, "[modelobs] terminal stream accounting failed: %v", finishErr)
			}
			if terminalErr != nil && (terminal == nil || terminal.ResponseType != types.ResponseTypeError) {
				terminal = &types.StreamResponse{
					ResponseType: types.ResponseTypeError, Content: terminalErr.Error(),
					Done: true, FinishReason: types.FinishReasonIncomplete,
				}
			}
			if terminal != nil {
				terminal.Usage = usage
				send(*terminal)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				status = types.ModelCallStatusCanceled
				terminalErr = context.Cause(ctx)
				return
			case response, ok := <-upstream:
				if !ok {
					return
				}
				if response.Usage != nil {
					cloned := *response.Usage
					usage = &cloned
				}
				if response.ResponseType == types.ResponseTypeError {
					status = types.ModelCallStatusError
					terminalErr = errors.New(response.Content)
					if response.Data["error_code"] == "model_call_accounting_failed" {
						terminalErr = types.RecordModelAccountingError(ctx, terminalErr)
					}
				}
				// Thinking Done terminates a segment. Answer/error Done terminates the call.
				if response.Done && response.ResponseType != types.ResponseTypeThinking {
					if terminal == nil || terminal.ResponseType != types.ResponseTypeError {
						cloned := response
						terminal = &cloned
					}
					continue
				}
				if terminal != nil {
					continue
				}
				if !send(response) {
					status = types.ModelCallStatusCanceled
					terminalErr = context.Cause(ctx)
					return
				}
			}
		}
	}()
	return output, nil
}

func (o *observedChat) GetModelName() string { return o.inner.GetModelName() }
func (o *observedChat) GetModelID() string   { return o.inner.GetModelID() }

type observedEmbedder struct {
	recorder *Recorder
	model    *types.Model
	inner    embedding.Embedder
}

// WrapEmbedder records provider embedding calls.
func (r *Recorder) WrapEmbedder(model *types.Model, inner embedding.Embedder) embedding.Embedder {
	if r == nil || r.store == nil || inner == nil {
		return inner
	}
	return &observedEmbedder{recorder: r, model: model, inner: inner}
}

func (o *observedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if supported, ok := o.inner.(interface{ RequestAccountingSupported() bool }); ok &&
		supported.RequestAccountingSupported() {
		ctx = o.recorder.attemptContext(ctx, o.model)
		return o.inner.Embed(ctx, text)
	}
	call, strict, err := o.recorder.start(ctx, o.model, "embedding")
	if err != nil && strict {
		return nil, err
	}
	result, providerErr := o.inner.Embed(ctx, text)
	return result, finishProviderCall(
		ctx, call, strict, statusForError(providerErr), providerErr, nil,
	)
}

func (o *observedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if supported, ok := o.inner.(interface{ RequestAccountingSupported() bool }); ok &&
		supported.RequestAccountingSupported() {
		ctx = o.recorder.attemptContext(ctx, o.model)
		return o.inner.BatchEmbed(ctx, texts)
	}
	call, strict, err := o.recorder.start(ctx, o.model, "embedding_batch")
	if err != nil && strict {
		return nil, err
	}
	result, providerErr := o.inner.BatchEmbed(ctx, texts)
	return result, finishProviderCall(
		ctx, call, strict, statusForError(providerErr), providerErr, nil,
	)
}

func (o *observedEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	if supported, ok := o.inner.(interface{ RequestAccountingSupported() bool }); ok &&
		supported.RequestAccountingSupported() {
		ctx = o.recorder.attemptContext(ctx, o.model)
		return o.inner.BatchEmbedWithPool(ctx, o.inner, texts)
	}
	call, strict, err := o.recorder.start(ctx, o.model, "embedding_batch")
	if err != nil && strict {
		return nil, err
	}
	result, providerErr := o.inner.BatchEmbedWithPool(ctx, o.inner, texts)
	return result, finishProviderCall(
		ctx, call, strict, statusForError(providerErr), providerErr, nil,
	)
}
func (o *observedEmbedder) GetModelName() string { return o.inner.GetModelName() }
func (o *observedEmbedder) GetDimensions() int   { return o.inner.GetDimensions() }
func (o *observedEmbedder) GetModelID() string   { return o.inner.GetModelID() }

type observedReranker struct {
	recorder *Recorder
	model    *types.Model
	inner    rerank.Reranker
}

// WrapReranker records provider rerank calls.
func (r *Recorder) WrapReranker(model *types.Model, inner rerank.Reranker) rerank.Reranker {
	if r == nil || r.store == nil || inner == nil {
		return inner
	}
	return &observedReranker{recorder: r, model: model, inner: inner}
}

func (o *observedReranker) Rerank(ctx context.Context, query string, documents []string) ([]rerank.RankResult, error) {
	if supported, ok := o.inner.(interface{ RequestAccountingSupported() bool }); ok &&
		supported.RequestAccountingSupported() {
		return o.inner.Rerank(o.recorder.attemptContext(ctx, o.model), query, documents)
	}
	call, strict, err := o.recorder.start(ctx, o.model, "rerank")
	if err != nil && strict {
		return nil, err
	}
	result, providerErr := o.inner.Rerank(ctx, query, documents)
	return result, finishProviderCall(
		ctx, call, strict, statusForError(providerErr), providerErr, nil,
	)
}
func (o *observedReranker) GetModelName() string { return o.inner.GetModelName() }
func (o *observedReranker) GetModelID() string   { return o.inner.GetModelID() }

type observedVLM struct {
	recorder *Recorder
	model    *types.Model
	inner    vlm.VLM
}

// WrapVLM records vision-language model calls.
func (r *Recorder) WrapVLM(model *types.Model, inner vlm.VLM) vlm.VLM {
	if r == nil || r.store == nil || inner == nil {
		return inner
	}
	return &observedVLM{recorder: r, model: model, inner: inner}
}

func (o *observedVLM) Predict(ctx context.Context, images [][]byte, prompt string) (string, error) {
	call, strict, err := o.recorder.start(ctx, o.model, "vlm")
	if err != nil && strict {
		return "", err
	}
	result, providerErr := o.inner.Predict(ctx, images, prompt)
	return result, finishProviderCall(
		ctx, call, strict, statusForError(providerErr), providerErr, nil,
	)
}
func (o *observedVLM) GetModelName() string { return o.inner.GetModelName() }
func (o *observedVLM) GetModelID() string   { return o.inner.GetModelID() }

type observedASR struct {
	recorder *Recorder
	model    *types.Model
	inner    asr.ASR
}

// WrapASR records automatic speech recognition calls.
func (r *Recorder) WrapASR(model *types.Model, inner asr.ASR) asr.ASR {
	if r == nil || r.store == nil || inner == nil {
		return inner
	}
	return &observedASR{recorder: r, model: model, inner: inner}
}

func (o *observedASR) Transcribe(ctx context.Context, audio []byte, fileName string) (*asr.TranscriptionResult, error) {
	call, strict, err := o.recorder.start(ctx, o.model, "asr")
	if err != nil && strict {
		return nil, err
	}
	result, providerErr := o.inner.Transcribe(ctx, audio, fileName)
	return result, finishProviderCall(
		ctx, call, strict, statusForError(providerErr), providerErr, nil,
	)
}
func (o *observedASR) GetModelName() string { return o.inner.GetModelName() }
func (o *observedASR) GetModelID() string   { return o.inner.GetModelID() }

func (r *Recorder) attemptContext(ctx context.Context, model *types.Model) context.Context {
	logicalID := uuid.NewString()
	var sequence atomic.Int64
	return call.WithObserver(ctx, func(attemptCtx context.Context, operation string) (call.Finish, error) {
		metadata := map[string]any{"logical_operation_id": logicalID, "attempt_sequence": sequence.Add(1)}
		for key, value := range call.Metadata(attemptCtx) {
			if key != "logical_operation_id" && key != "attempt_sequence" {
				metadata[key] = value
			}
		}
		attemptCtx = call.WithMetadata(attemptCtx, metadata)
		types.StreamActivity(attemptCtx, false)
		active, strict, err := r.start(attemptCtx, model, operation)
		types.StreamActivity(attemptCtx, true)
		if err != nil && strict {
			return nil, err
		}
		return func(providerErr error, usage *types.TokenUsage) error {
			types.StreamActivity(attemptCtx, false)
			return finishProviderCall(attemptCtx, active, strict, statusForError(providerErr), providerErr, usage)
		}, nil
	})
}

func (o *observedChat) OutputTokenLimit() int {
	if bounded, ok := o.inner.(interface{ OutputTokenLimit() int }); ok {
		return bounded.OutputTokenLimit()
	}
	return o.model.Parameters.MaxOutputTokens
}

func (o *observedChat) BehaviorFingerprint() string {
	return types.EvaluationModelConfigSHA256(o.model)
}
