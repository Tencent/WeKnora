package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	typingInterval = 5 * time.Second
	typingTimeout  = 3 * time.Second
	typingStart    = 1
	typingStop     = 2
)

type typingRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// BeginTyping starts the native indicator and returns an idempotent stop function.
func (a *Adapter) BeginTyping(ctx context.Context, msg *im.IncomingMessage) func() {
	userID, token := msg.UserID, msg.Extra["context_token"]
	if userID == "" || ctx.Err() != nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	run := &typingRun{cancel: cancel, done: make(chan struct{})}
	previous, replaced := a.typing.Swap(userID, run)
	if replaced {
		previous.(*typingRun).cancel()
	}
	go func() {
		defer close(run.done)
		defer a.typing.CompareAndDelete(userID, run)
		// A previous turn must finish clearing its indicator before we can start ours.
		if replaced {
			<-previous.(*typingRun).done
		}
		a.keepTyping(ctx, userID, token)
	}()
	return sync.OnceFunc(func() {
		cancel()
		timer := time.NewTimer(typingTimeout)
		defer timer.Stop()
		select {
		case <-run.done:
		case <-timer.C:
		}
	})
}

func (a *Adapter) keepTyping(ctx context.Context, userID, token string) {
	ticker := time.NewTicker(typingInterval)
	defer ticker.Stop()
	attempted, warned := false, false
	for ctx.Err() == nil {
		sent, err := a.setTyping(ctx, userID, token, typingStart)
		attempted = attempted || sent
		if err != nil && !warned && ctx.Err() == nil {
			logger.Warn(ctx, "[WeChat] Could not refresh typing indicator")
			warned = true
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	// A timed-out start may still have reached iLink, so clear after any send attempt.
	if attempted {
		if _, err := a.setTyping(context.WithoutCancel(ctx), userID, token, typingStop); err != nil {
			logger.Debugf(ctx, "[WeChat] Could not clear typing indicator")
		}
	}
}

func (a *Adapter) setTyping(ctx context.Context, userID, token string, status int) (attempted bool, err error) {
	requestCtx, cancel := context.WithTimeout(ctx, typingTimeout)
	defer cancel()
	config, err := a.typingPost(requestCtx, "/ilink/bot/getconfig", map[string]any{
		"ilink_user_id": userID,
		"context_token": token,
		"base_info":     newBaseInfo(),
	})
	if err != nil {
		return false, err
	}
	ticket := config.Ticket
	if ticket == "" {
		ticket = config.CamelCaseTicket
	}
	if ticket == "" {
		return false, errors.New("iLink did not return a typing ticket")
	}
	if err := requestCtx.Err(); err != nil {
		return false, err
	}
	// Let an in-flight status update finish before sending the opposite status.
	deadline, _ := requestCtx.Deadline()
	sendCtx, cancelSend := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancelSend()
	_, err = a.typingPost(sendCtx, "/ilink/bot/sendtyping", map[string]any{
		"ilink_user_id": userID,
		"typing_ticket": ticket,
		"status":        status,
		"base_info":     newBaseInfo(),
	})
	return true, err
}

type typingResponse struct {
	Ret             int    `json:"ret"`
	ErrCode         int    `json:"errcode"`
	Ticket          string `json:"typing_ticket"`
	CamelCaseTicket string `json:"typingTicket"`
}

func (a *Adapter) typingPost(ctx context.Context, path string, payload map[string]any) (*typingResponse, error) {
	body, err := a.ilinkPost(ctx, path, payload)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return &typingResponse{}, nil
	}
	var response *typingResponse
	if err := json.Unmarshal(body, &response); err != nil || response == nil {
		return nil, fmt.Errorf("invalid iLink %s response", path)
	}
	if response.Ret != 0 || response.ErrCode != 0 {
		return nil, fmt.Errorf("iLink %s failed (ret=%d, errcode=%d)", path, response.Ret, response.ErrCode)
	}
	return response, nil
}
