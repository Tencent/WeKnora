package types

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// declaredContextKeys walks the source of internal/ and returns every declared
// context key by its underlying string value, mapped to where it was found.
//
// The keys are collected from the source rather than from a hand-written list
// because a hand-written list has the very failure mode this test exists to
// close: whoever forgets to register a new key in the clone table is exactly
// the person who would forget to add it to the list guarding the table.
func declaredContextKeys(t *testing.T) map[ContextKey]string {
	t.Helper()

	found := make(map[ContextKey]string)
	fset := token.NewFileSet()

	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		// Test files are skipped: a key invented by a test never reaches a
		// detached production context, so forcing a decision for it would be
		// noise.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		for _, decl := range file.Decls {
			generic, ok := decl.(*ast.GenDecl)
			if !ok || generic.Tok != token.CONST {
				continue
			}
			for _, spec := range generic.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || !isContextKeyType(value.Type) {
					continue
				}
				for _, expr := range value.Values {
					literal, ok := expr.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					unquoted, unquoteErr := strconv.Unquote(literal.Value)
					if unquoteErr != nil {
						return unquoteErr
					}
					found[ContextKey(unquoted)] = path
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/ for context key declarations: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("found no context key declarations, so this test is not guarding anything")
	}
	return found
}

// isContextKeyType reports whether a const is typed as this package's
// ContextKey, written either bare (inside types) or qualified (outside it).
func isContextKeyType(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name == "ContextKey"
	case *ast.SelectorExpr:
		return typed.Sel.Name == "ContextKey"
	}
	return false
}

// A context key that reaches logger.CloneContext without a recorded decision
// opts out of surviving the detach by accident, and nothing reports it: the
// background work simply runs without the value. That is how an agent's
// long-term memory opt-out came to stop recall while leaving the write path
// running, and how the session→sandbox binding was once re-keyed onto a
// borrowed tenant. Either answer is fine here; silence is not.
func TestEveryContextKeyDeclaresACloneDecision(t *testing.T) {
	t.Parallel()

	for key, path := range declaredContextKeys(t) {
		if _, declared := ContextCloneDecision(key); !declared {
			t.Errorf(
				"context key %q (declared in %s) has no entry in contextCloneAcrossDetach; "+
					"add one saying whether it should survive logger.CloneContext",
				key, path,
			)
		}
	}
}

// The reverse direction: an entry left behind by a deleted key turns the table
// into folklore, and the next reader cannot tell which entries still describe
// something real.
func TestTheCloneTableHasNoEntriesForKeysThatNoLongerExist(t *testing.T) {
	t.Parallel()

	declared := declaredContextKeys(t)
	for key := range contextCloneAcrossDetach {
		if _, ok := declared[key]; !ok {
			t.Errorf(
				"contextCloneAcrossDetach has an entry for %q, but no context key with that value is declared in internal/",
				key,
			)
		}
	}
}

// The table is what CloneContext iterates, so a key marked as surviving has to
// come back out of the accessor, and one marked as dropped must not.
func TestClonedKeysAreExactlyTheOnesMarkedToSurvive(t *testing.T) {
	t.Parallel()

	surviving := make(map[ContextKey]bool)
	for _, key := range ContextKeysClonedAcrossDetach() {
		if surviving[key] {
			t.Errorf("key %q is returned twice", key)
		}
		surviving[key] = true
	}

	for key, clone := range contextCloneAcrossDetach {
		if clone != surviving[key] {
			t.Errorf("key %q: table says clone=%v, accessor says %v", key, clone, surviving[key])
		}
	}
}
