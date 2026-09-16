package call

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// FinishStream publishes the terminal response after provider accounting completes.
func FinishStream(
	ctx context.Context,
	upstream <-chan types.StreamResponse,
	finish Finish,
) <-chan types.StreamResponse {
	output := make(chan types.StreamResponse)
	go func() {
		defer close(output)

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
			}
			if cause := context.Cause(ctx); cause != nil {
				terminalErr = cause
			}
			finishErr := finish(terminalErr, usage)
			if errors.Is(finishErr, types.ErrModelAccounting) {
				_ = types.RecordModelAccountingError(ctx, finishErr)
				send(types.StreamResponse{
					ResponseType: types.ResponseTypeError, Content: "model call accounting failed", Done: true,
					Usage: usage, FinishReason: types.FinishReasonIncomplete,
					Data: map[string]interface{}{"error_code": "model_call_accounting_failed"},
				})
				return
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

				terminalErr = context.Cause(ctx)
				// The parser closes its response body and publishes known usage on cancellation.
				timer := time.NewTimer(200 * time.Millisecond)
				defer timer.Stop()
				for {
					select {
					case response, ok := <-upstream:
						if !ok {
							return
						}
						if response.Usage != nil {
							cloned := *response.Usage
							usage = &cloned
						}
					case <-timer.C:
						return
					}
				}
			case response, ok := <-upstream:
				if !ok {
					return
				}
				if response.Usage != nil {
					cloned := *response.Usage
					usage = &cloned
				}
				if response.ResponseType == types.ResponseTypeError {

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

					terminalErr = context.Cause(ctx)
					return
				}
			}
		}
	}()
	return output
}
