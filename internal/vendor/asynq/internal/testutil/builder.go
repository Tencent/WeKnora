// Copyright 2022 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package testutil

import (
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq/internal/base"
)

func makeDefaultTaskMessage() *base.TaskMessage {
	return &base.TaskMessage{
		ID:       uuid.NewString(),
		Type:     "default_task",
		Queue:    "default",
		Retry:    25,
		Timeout:  1800, // default timeout of 30 mins
		Deadline: 0,    // no deadline
	}
}

type TaskMessageBuilder struct {
	msg *base.TaskMessage
}

func NewTaskMessageBuilder() *TaskMessageBuilder {
	return &TaskMessageBuilder{}
}

func (b *TaskMessageBuilder) lazyInit() {
	if b.msg == nil {
		b.msg = makeDefaultTaskMessage()
	}
}

func (b *TaskMessageBuilder) Build() *base.TaskMessage {
	b.lazyInit()
	return b.msg
}

func (b *TaskMessageBuilder) SetType(typename string) *TaskMessageBuilder {
	b.lazyInit()
	b.msg.Type = typename
	return b
}

func (b *TaskMessageBuilder) SetPayload(payload []byte) *TaskMessageBuilder {
	b.lazyInit()
	b.msg.Payload = payload
	return b
}

func (b *TaskMessageBuilder) SetQueue(qname string) *TaskMessageBuilder {
	b.lazyInit()
	b.msg.Queue = qname
	return b
}

func (b *TaskMessageBuilder) SetRetry(n int) *TaskMessageBuilder {
	b.lazyInit()
	b.msg.Retry = n
	return b
}

func (b *TaskMessageBuilder) SetTimeout(timeout time.Duration) *TaskMessageBuilder {
	b.lazyInit()
	b.msg.Timeout = int64(timeout.Seconds())
	return b
}

func (b *TaskMessageBuilder) SetDeadline(deadline time.Time) *TaskMessageBuilder {
	b.lazyInit()
	b.msg.Deadline = deadline.Unix()
	return b
}

func (b *TaskMessageBuilder) SetGroup(gname string) *TaskMessageBuilder {
	b.lazyInit()
	b.msg.GroupKey = gname
	return b
}
