// Copyright 2022 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package testutil

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hibiken/asynq/internal/base"
)

func TestTaskMessageBuilder(t *testing.T) {
	tests := []struct {
		desc string
		ops  func(b *TaskMessageBuilder) // operations to perform on the builder
		want *base.TaskMessage
	}{
		{
			desc: "zero value and build",
			ops:  nil,
			want: &base.TaskMessage{
				Type:     "default_task",
				Queue:    "default",
				Payload:  nil,
				Retry:    25,
				Timeout:  1800, // 30m
				Deadline: 0,
			},
		},
		{
			desc: "with type, payload, and queue",
			ops: func(b *TaskMessageBuilder) {
				b.SetType("foo").SetPayload([]byte("hello")).SetQueue("myqueue")
			},
			want: &base.TaskMessage{
				Type:     "foo",
				Queue:    "myqueue",
				Payload:  []byte("hello"),
				Retry:    25,
				Timeout:  1800, // 30m
				Deadline: 0,
			},
		},
		{
			desc: "with retry, timeout, and deadline",
			ops: func(b *TaskMessageBuilder) {
				b.SetRetry(1).
					SetTimeout(20 * time.Second).
					SetDeadline(time.Date(2017, 3, 6, 0, 0, 0, 0, time.UTC))
			},
			want: &base.TaskMessage{
				Type:     "default_task",
				Queue:    "default",
				Payload:  nil,
				Retry:    1,
				Timeout:  20,
				Deadline: time.Date(2017, 3, 6, 0, 0, 0, 0, time.UTC).Unix(),
			},
		},
		{
			desc: "with group",
			ops: func(b *TaskMessageBuilder) {
				b.SetGroup("mygroup")
			},
			want: &base.TaskMessage{
				Type:     "default_task",
				Queue:    "default",
				Payload:  nil,
				Retry:    25,
				Timeout:  1800,
				Deadline: 0,
				GroupKey: "mygroup",
			},
		},
	}
	cmpOpts := []cmp.Option{cmpopts.IgnoreFields(base.TaskMessage{}, "ID")}

	for _, tc := range tests {
		var b TaskMessageBuilder
		if tc.ops != nil {
			tc.ops(&b)
		}

		got := b.Build()
		if diff := cmp.Diff(tc.want, got, cmpOpts...); diff != "" {
			t.Errorf("%s: TaskMessageBuilder.Build() = %+v, want %+v;\n(-want,+got)\n%s",
				tc.desc, got, tc.want, diff)
		}
	}
}
