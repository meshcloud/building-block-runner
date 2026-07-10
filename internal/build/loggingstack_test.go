package build

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoStdlibLogInInternal enforces the single logging stack (PLAN_DETAIL_07 §8.5 / D15):
// every package under internal/* logs through log/slog, never the stdlib "log" package.
//
// This Go test stands in for the depguard "log" deny the plan sketched. depguard's deny is
// prefix-based, so denying "log" would also ban "log/slog" (the whole stack); it cannot
// express "the stdlib log package but not its slog subpackage". An AST-level import check can,
// so the invariant is enforced here instead. The run-log byte sink (internal/report, §8.2) is
// process-output plumbing on its own io.Writer -- it never used stdlib log either, so it is
// covered by this rule with no exception.
//
// The test lives in internal/build so its parent directory ("..") is the internal/ tree root.
func TestNoStdlibLogInInternal(t *testing.T) {
	const internalRoot = ".."

	fset := token.NewFileSet()
	err := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == `"log"` {
				t.Errorf("%s imports the stdlib \"log\" package; use log/slog (single logging stack, PLAN_DETAIL_07 §8.5)", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/ tree: %v", err)
	}
}
