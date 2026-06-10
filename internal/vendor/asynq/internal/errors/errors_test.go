// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package errors

import "testing"

func TestErrorDebugString(t *testing.T) {
	// DebugString should include Op since its meant to be used by
	// maintainers/contributors of the asynq package.
	tests := []struct {
		desc string
		err  error
		want string
	}{
		{
			desc: "With Op, Code, and string",
			err:  E(Op("rdb.DeleteTask"), NotFound, "cannot find task with id=123"),
			want: "rdb.DeleteTask: NOT_FOUND: cannot find task with id=123",
		},
		{
			desc: "With Op, Code and error",
			err:  E(Op("rdb.DeleteTask"), NotFound, &TaskNotFoundError{Queue: "default", ID: "123"}),
			want: `rdb.DeleteTask: NOT_FOUND: cannot find task with id=123 in queue "default"`,
		},
	}

	for _, tc := range tests {
		if got := tc.err.(*Error).DebugString(); got != tc.want {
			t.Errorf("%s: got=%q, want=%q", tc.desc, got, tc.want)
		}
	}
}

func TestErrorString(t *testing.T) {
	// String method should omit Op since op is an internal detail
	// and we don't want to provide it to users of the package.
	tests := []struct {
		desc string
		err  error
		want string
	}{
		{
			desc: "With Op, Code, and string",
			err:  E(Op("rdb.DeleteTask"), NotFound, "cannot find task with id=123"),
			want: "NOT_FOUND: cannot find task with id=123",
		},
		{
			desc: "With Op, Code and error",
			err:  E(Op("rdb.DeleteTask"), NotFound, &TaskNotFoundError{Queue: "default", ID: "123"}),
			want: `NOT_FOUND: cannot find task with id=123 in queue "default"`,
		},
	}

	for _, tc := range tests {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("%s: got=%q, want=%q", tc.desc, got, tc.want)
		}
	}
}

func TestErrorIs(t *testing.T) {
	var ErrCustom = New("custom sentinel error")

	tests := []struct {
		desc   string
		err    error
		target error
		want   bool
	}{
		{
			desc:   "should unwrap one level",
			err:    E(Op("rdb.DeleteTask"), ErrCustom),
			target: ErrCustom,
			want:   true,
		},
	}

	for _, tc := range tests {
		if got := Is(tc.err, tc.target); got != tc.want {
			t.Errorf("%s: got=%t, want=%t", tc.desc, got, tc.want)
		}
	}
}

func TestErrorAs(t *testing.T) {
	tests := []struct {
		desc   string
		err    error
		target interface{}
		want   bool
	}{
		{
			desc:   "should unwrap one level",
			err:    E(Op("rdb.DeleteTask"), NotFound, &QueueNotFoundError{Queue: "email"}),
			target: &QueueNotFoundError{},
			want:   true,
		},
	}

	for _, tc := range tests {
		if got := As(tc.err, &tc.target); got != tc.want {
			t.Errorf("%s: got=%t, want=%t", tc.desc, got, tc.want)
		}
	}
}

func TestErrorPredicates(t *testing.T) {
	tests := []struct {
		desc string
		fn   func(err error) bool
		err  error
		want bool
	}{
		{
			desc: "IsTaskNotFound should detect presence of TaskNotFoundError in err's chain",
			fn:   IsTaskNotFound,
			err:  E(Op("rdb.ArchiveTask"), NotFound, &TaskNotFoundError{Queue: "default", ID: "9876"}),
			want: true,
		},
		{
			desc: "IsTaskNotFound should detect absence of TaskNotFoundError in err's chain",
			fn:   IsTaskNotFound,
			err:  E(Op("rdb.ArchiveTask"), NotFound, &QueueNotFoundError{Queue: "default"}),
			want: false,
		},
		{
			desc: "IsQueueNotFound should detect presence of QueueNotFoundError in err's chain",
			fn:   IsQueueNotFound,
			err:  E(Op("rdb.ArchiveTask"), NotFound, &QueueNotFoundError{Queue: "default"}),
			want: true,
		},
		{
			desc: "IsPanicError should detect presence of PanicError in err's chain",
			fn:   IsPanicError,
			err:  E(Op("unknown"), Unknown, &PanicError{ErrMsg: "Something went wrong"}),
			want: true,
		},
	}

	for _, tc := range tests {
		if got := tc.fn(tc.err); got != tc.want {
			t.Errorf("%s: got=%t, want=%t", tc.desc, got, tc.want)
		}
	}
}

func TestCanonicalCode(t *testing.T) {
	tests := []struct {
		desc string
		err  error
		want Code
	}{
		{
			desc: "without nesting",
			err:  E(Op("rdb.DeleteTask"), NotFound, &TaskNotFoundError{Queue: "default", ID: "123"}),
			want: NotFound,
		},
		{
			desc: "with nesting",
			err:  E(FailedPrecondition, E(NotFound)),
			want: FailedPrecondition,
		},
		{
			desc: "returns Unspecified if err is not *Error",
			err:  New("some other error"),
			want: Unspecified,
		},
		{
			desc: "returns Unspecified if err is nil",
			err:  nil,
			want: Unspecified,
		},
	}

	for _, tc := range tests {
		if got := CanonicalCode(tc.err); got != tc.want {
			t.Errorf("%s: got=%s, want=%s", tc.desc, got, tc.want)
		}
	}
}
