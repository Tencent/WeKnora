// cli/acceptance/contract/errorcodes_test.go
package contract_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// TestAllReferencedCodesAreRegistered scans cli/cmd/ for every literal use of
// cmdutil.NewError(codeXxx, ...) and cmdutil.Wrapf(codeXxx, ...) and verifies
// that codeXxx is registered in cmdutil.AllCodes().
//
// ClassifyHTTPError is dynamic: callers pass cmdutil.ClassifyHTTPError(err)
// as the first arg. We bridge that via cmdutil.ClassifyHTTPErrorOutputs(),
// which returns every code that the switch can return — those are added to
// the registered set so the AST scanner doesn't false-positive on them.
//
// Limitations (documented in spec §4.3):
//   - Only literal cmdutil.CodeXxx idents are detected; codes assigned to
//     a local variable then passed are NOT scanned (rare pattern).
//   - cmdutil.ClassifyHTTPError(...) call expressions are skipped — bridge
//     covers them.
//   - v0.x does not enforce a baseline diff (spec ADR-6b); v0.9 will.
func TestAllReferencedCodesAreRegistered(t *testing.T) {
	registered := make(map[cmdutil.ErrorCode]struct{})
	for _, c := range cmdutil.AllCodes() {
		registered[c] = struct{}{}
	}
	// Bridge: ClassifyHTTPError returns these dynamically; treat them as
	// "registered" for the purposes of the AST scan since callers pass the
	// function call (not a literal ident) as arg0.
	for _, c := range cmdutil.ClassifyHTTPErrorOutputs() {
		registered[c] = struct{}{}
	}

	referenced := scanCmdDir(t, "../../cmd")

	for code, locs := range referenced {
		if _, ok := registered[code]; !ok {
			for _, loc := range locs {
				t.Errorf("%s: error code %q referenced but not registered in cmdutil.AllCodes()", loc, code)
			}
		}
	}
}

// scanCmdDir walks cli/cmd/** for *.go files (excluding tests) and collects
// every literal cmdutil.CodeXxx ident passed as the first arg to
// cmdutil.NewError / cmdutil.Wrapf.
//
// Returns map keyed by ErrorCode value (the const's underlying string), with
// a slice of source positions for nicer error messages.
func scanCmdDir(t *testing.T, dir string) map[cmdutil.ErrorCode][]token.Position {
	t.Helper()
	out := make(map[cmdutil.ErrorCode][]token.Position)
	fset := token.NewFileSet()
	walkAndScan(t, fset, dir, out)
	return out
}

func walkAndScan(t *testing.T, fset *token.FileSet, root string, out map[cmdutil.ErrorCode][]token.Position) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		collectErrorCodes(fset, f, out)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func collectErrorCodes(fset *token.FileSet, f *ast.File, out map[cmdutil.ErrorCode][]token.Position) {
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok || x.Name != "cmdutil" {
			return true
		}
		if sel.Sel.Name != "NewError" && sel.Sel.Name != "Wrapf" {
			return true
		}
		// First arg should be cmdutil.CodeXxx (SelectorExpr ident).
		// If it's a function call (e.g. cmdutil.ClassifyHTTPError(err)), skip
		// — bridge handles those.
		arg0Sel, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		argX, ok := arg0Sel.X.(*ast.Ident)
		if !ok || argX.Name != "cmdutil" {
			return true
		}
		code, ok := identToErrorCode(arg0Sel.Sel.Name)
		if !ok {
			// Unknown ident name — record as bogus so the test fails with
			// a clear "code referenced but not registered" message.
			code = cmdutil.ErrorCode("UNKNOWN_REF:" + arg0Sel.Sel.Name)
		}
		pos := fset.Position(call.Pos())
		out[code] = append(out[code], pos)
		return true
	})
}

// identToErrorCode maps an ident name like "CodeAuthUnauthenticated" to its
// underlying ErrorCode value via a simple switch. Avoids reflect.
// Keep in sync with cmdutil.AllCodes() — adding a new const here is the same
// bookkeeping as adding it to AllCodes().
func identToErrorCode(name string) (cmdutil.ErrorCode, bool) {
	switch name {
	case "CodeAuthUnauthenticated":
		return cmdutil.CodeAuthUnauthenticated, true
	case "CodeAuthTokenExpired":
		return cmdutil.CodeAuthTokenExpired, true
	case "CodeAuthBadCredential":
		return cmdutil.CodeAuthBadCredential, true
	case "CodeAuthForbidden":
		return cmdutil.CodeAuthForbidden, true
	case "CodeAuthCrossTenantBlocked":
		return cmdutil.CodeAuthCrossTenantBlocked, true
	case "CodeAuthTenantMismatch":
		return cmdutil.CodeAuthTenantMismatch, true
	case "CodeResourceNotFound":
		return cmdutil.CodeResourceNotFound, true
	case "CodeResourceAlreadyExists":
		return cmdutil.CodeResourceAlreadyExists, true
	case "CodeResourceLocked":
		return cmdutil.CodeResourceLocked, true
	case "CodeInputInvalidArgument":
		return cmdutil.CodeInputInvalidArgument, true
	case "CodeInputMissingFlag":
		return cmdutil.CodeInputMissingFlag, true
	case "CodeServerError":
		return cmdutil.CodeServerError, true
	case "CodeServerTimeout":
		return cmdutil.CodeServerTimeout, true
	case "CodeServerRateLimited":
		return cmdutil.CodeServerRateLimited, true
	case "CodeServerIncompatibleVersion":
		return cmdutil.CodeServerIncompatibleVersion, true
	case "CodeNetworkError":
		return cmdutil.CodeNetworkError, true
	case "CodeLocalConfigCorrupt":
		return cmdutil.CodeLocalConfigCorrupt, true
	case "CodeLocalKeychainDenied":
		return cmdutil.CodeLocalKeychainDenied, true
	case "CodeLocalFileIO":
		return cmdutil.CodeLocalFileIO, true
	case "CodeLocalUnimplemented":
		return cmdutil.CodeLocalUnimplemented, true
	case "CodeLocalContextNotFound":
		return cmdutil.CodeLocalContextNotFound, true
	case "CodeKBIDRequired":
		return cmdutil.CodeKBIDRequired, true
	case "CodeKBNotFound":
		return cmdutil.CodeKBNotFound, true
	case "CodeProjectLinkCorrupt":
		return cmdutil.CodeProjectLinkCorrupt, true
	case "CodeUserAborted":
		return cmdutil.CodeUserAborted, true
	case "CodeUploadFileNotFound":
		return cmdutil.CodeUploadFileNotFound, true
	case "CodeSSEStreamAborted":
		return cmdutil.CodeSSEStreamAborted, true
	case "CodeSessionCreateFailed":
		return cmdutil.CodeSessionCreateFailed, true
	case "CodeMCPReadonlyMode":
		return cmdutil.CodeMCPReadonlyMode, true
	case "CodeMCPToolNotAllowed":
		return cmdutil.CodeMCPToolNotAllowed, true
	case "CodeMCPSchemaUnknown":
		return cmdutil.CodeMCPSchemaUnknown, true
	}
	return "", false
}
