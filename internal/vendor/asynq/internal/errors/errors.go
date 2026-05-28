// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

// Package errors defines the error type and functions used by
// asynq and its internal packages.
package errors

// Note: This package is inspired by a blog post about error handling in project Upspin
// https://commandcenter.blogspot.com/2017/12/error-handling-in-upspin.html.

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"strings"
)

// Error is the type that implements the error interface.
// It contains a number of fields, each of different type.
// An Error value may leave some values unset.
type Error struct {
	Code Code
	Op   Op
	Err  error
}

func (e *Error) DebugString() string {
	var b strings.Builder
	if e.Op != "" {
		b.WriteString(string(e.Op))
	}
	if e.Code != Unspecified {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(e.Code.String())
	}
	if e.Err != nil {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.Code != Unspecified {
		b.WriteString(e.Code.String())
	}
	if e.Err != nil {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

func (e *Error) Unwrap() error {
	return e.Err
}

// Code defines the canonical error code.
type Code uint8

// List of canonical error codes.
const (
	Unspecified Code = iota
	NotFound
	FailedPrecondition
	Internal
	AlreadyExists
	Unknown
	// Note: If you add a new value here, make sure to update String method.
)

func (c Code) String() string {
	switch c {
	case Unspecified:
		return "ERROR_CODE_UNSPECIFIED"
	case NotFound:
		return "NOT_FOUND"
	case FailedPrecondition:
		return "FAILED_PRECONDITION"
	case Internal:
		return "INTERNAL_ERROR"
	case AlreadyExists:
		return "ALREADY_EXISTS"
	case Unknown:
		return "UNKNOWN"
	}
	panic(fmt.Sprintf("unknown error code %d", c))
}

// Op describes an operation, usually as the package and method,
// such as "rdb.Enqueue".
type Op string

// E builds an error value from its arguments.
// There must be at least one argument or E panics.
// The type of each argument determines its meaning.
// If more than one argument of a given type is presented,
// only the last one is recorded.
//
// The types are:
//
//	errors.Op
//		The operation being performed, usually the method
//		being invoked (Get, Put, etc.).
//	errors.Code
//		The canonical error code, such as NOT_FOUND.
//	string
//		Treated as an error message and assigned to the
//		Err field after a call to errors.New.
//	error
//		The underlying error that triggered this one.
//
// If the error is printed, only those items that have been
// set to non-zero values will appear in the result.
func E(args ...interface{}) error {
	if len(args) == 0 {
		panic("call to errors.E with no arguments")
	}
	e := &Error{}
	for _, arg := range args {
		switch arg := arg.(type) {
		case Op:
			e.Op = arg
		case Code:
			e.Code = arg
		case error:
			e.Err = arg
		case string:
			e.Err = errors.New(arg)
		default:
			_, file, line, _ := runtime.Caller(1)
			log.Printf("errors.E: bad call from %s:%d: %v", file, line, args)
			return fmt.Errorf("unknown type %T, value %v in error call", arg, arg)
		}
	}
	return e
}

// CanonicalCode returns the canonical code of the given error if one is present.
// Otherwise it returns Unspecified.
func CanonicalCode(err error) Code {
	if err == nil {
		return Unspecified
	}
	e, ok := err.(*Error)
	if !ok {
		return Unspecified
	}
	if e.Code == Unspecified {
		return CanonicalCode(e.Err)
	}
	return e.Code
}

/******************************************
    Domain Specific Error Types & Values
*******************************************/

var (
	// ErrNoProcessableTask indicates that there are no tasks ready to be processed.
	ErrNoProcessableTask = errors.New("no tasks are ready for processing")

	// ErrDuplicateTask indicates that another task with the same unique key holds the uniqueness lock.
	ErrDuplicateTask = errors.New("task already exists")

	// ErrTaskIdConflict indicates that another task with the same task ID already exist
	ErrTaskIdConflict = errors.New("task id conflicts with another task")
)

// TaskNotFoundError indicates that a task with the given ID does not exist
// in the given queue.
type TaskNotFoundError struct {
	Queue string // queue name
	ID    string // task id
}

func (e *TaskNotFoundError) Error() string {
	return fmt.Sprintf("cannot find task with id=%s in queue %q", e.ID, e.Queue)
}

// IsTaskNotFound reports whether any error in err's chain is of type TaskNotFoundError.
func IsTaskNotFound(err error) bool {
	var target *TaskNotFoundError
	return As(err, &target)
}

// QueueNotFoundError indicates that a queue with the given name does not exist.
type QueueNotFoundError struct {
	Queue string // queue name
}

func (e *QueueNotFoundError) Error() string {
	return fmt.Sprintf("queue %q does not exist", e.Queue)
}

// IsQueueNotFound reports whether any error in err's chain is of type QueueNotFoundError.
func IsQueueNotFound(err error) bool {
	var target *QueueNotFoundError
	return As(err, &target)
}

// QueueNotEmptyError indicates that the given queue is not empty.
type QueueNotEmptyError struct {
	Queue string // queue name
}

func (e *QueueNotEmptyError) Error() string {
	return fmt.Sprintf("queue %q is not empty", e.Queue)
}

// IsQueueNotEmpty reports whether any error in err's chain is of type QueueNotEmptyError.
func IsQueueNotEmpty(err error) bool {
	var target *QueueNotEmptyError
	return As(err, &target)
}

// TaskAlreadyArchivedError indicates that the task in question is already archived.
type TaskAlreadyArchivedError struct {
	Queue string // queue name
	ID    string // task id
}

func (e *TaskAlreadyArchivedError) Error() string {
	return fmt.Sprintf("task is already archived: id=%s, queue=%s", e.ID, e.Queue)
}

// IsTaskAlreadyArchived reports whether any error in err's chain is of type TaskAlreadyArchivedError.
func IsTaskAlreadyArchived(err error) bool {
	var target *TaskAlreadyArchivedError
	return As(err, &target)
}

// RedisCommandError indicates that the given redis command returned error.
type RedisCommandError struct {
	Command string // redis command (e.g. LRANGE, ZADD, etc)
	Err     error  // underlying error
}

func (e *RedisCommandError) Error() string {
	return fmt.Sprintf("redis command error: %s failed: %v", strings.ToUpper(e.Command), e.Err)
}

func (e *RedisCommandError) Unwrap() error { return e.Err }

// IsRedisCommandError reports whether any error in err's chain is of type RedisCommandError.
func IsRedisCommandError(err error) bool {
	var target *RedisCommandError
	return As(err, &target)
}

// PanicError defines an error when occurred a panic error.
type PanicError struct {
	ErrMsg string
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("panic error cause by: %s", e.ErrMsg)
}

// IsPanicError reports whether any error in err's chain is of type PanicError.
func IsPanicError(err error) bool {
	var target *PanicError
	return As(err, &target)
}

/*************************************************
    Standard Library errors package functions
*************************************************/

// New returns an error that formats as the given text.
// Each call to New returns a distinct error value even if the text is identical.
//
// This function is the errors.New function from the standard library (https://golang.org/pkg/errors/#New).
// It is exported from this package for import convenience.
func New(text string) error { return errors.New(text) }

// Is reports whether any error in err's chain matches target.
//
// This function is the errors.Is function from the standard library (https://golang.org/pkg/errors/#Is).
// It is exported from this package for import convenience.
func Is(err, target error) bool { return errors.Is(err, target) }

// As finds the first error in err's chain that matches target, and if so, sets target to that error value and returns true.
// Otherwise, it returns false.
//
// This function is the errors.As function from the standard library (https://golang.org/pkg/errors/#As).
// It is exported from this package for import convenience.
func As(err error, target interface{}) bool { return errors.As(err, target) }

// Unwrap returns the result of calling the Unwrap method on err, if err's type contains an Unwrap method returning error.
// Otherwise, Unwrap returns nil.
//
// This function is the errors.Unwrap function from the standard library (https://golang.org/pkg/errors/#Unwrap).
// It is exported from this package for import convenience.
func Unwrap(err error) error { return errors.Unwrap(err) }
