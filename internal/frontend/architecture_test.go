// This file provides intentionally test-only enforcement of DDGo's frontend
// toolkit boundary. Parsing source files is appropriate here because the guard
// runs only during tests and adds no filesystem walking, parsing, or dependency
// inspection to the shipped application.
//
// The guard preserves the boundary established before the Qt6 migration:
// internal/frontend remains toolkit-independent, direct MIQT/Qt binding imports
// live under internal/frontend/miqt, and cmd/ddgo is the composition root allowed
// to select that concrete frontend. Standalone experiments are deliberately
// outside this policy, so the experiments tree is not scanned.
//
// This is a narrow regression tripwire for those specific relationships, not a
// general-purpose dependency-layer framework for the rest of the repository.
package frontend_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const (
	miqtBindingImport  = "github.com/mappu/miqt"
	miqtFrontendImport = "github.com/ianbruene/ddgo/internal/frontend/miqt"
)

func TestFrontendArchitecture(t *testing.T) {
	_, testFilename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine architecture test file location")
	}

	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(testFilename), "..", ".."))
	fileSet := token.NewFileSet()

	// Only the root module's production trees are in scope. In particular,
	// standalone experiments may exercise other toolkit versions directly.
	for _, tree := range []string{"cmd", "internal"} {
		treeRoot := filepath.Join(repositoryRoot, tree)
		err := filepath.WalkDir(treeRoot, func(filename string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(filename) != ".go" {
				return nil
			}

			relativeFilename, err := filepath.Rel(repositoryRoot, filename)
			if err != nil {
				return fmt.Errorf("make %q relative to repository root: %w", filename, err)
			}
			relativeFilename = filepath.ToSlash(relativeFilename)

			parsed, err := parser.ParseFile(fileSet, filename, nil, parser.ImportsOnly)
			if err != nil {
				return fmt.Errorf("parse %s: %w", relativeFilename, err)
			}

			for _, spec := range parsed.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return fmt.Errorf("unquote import %s in %s: %w", spec.Path.Value, relativeFilename, err)
				}

				// MIQT binding imports are confined to the concrete toolkit
				// implementation, keeping frontend core and backend code Qt-free.
				if isMIQTBindingImport(importPath) &&
					!pathWithin(relativeFilename, "internal/frontend/miqt") {
					t.Errorf(
						"%s imports MIQT binding %q; direct MIQT imports are allowed only under internal/frontend/miqt",
						relativeFilename,
						importPath,
					)
				}

				// The concrete MIQT frontend may be selected only by the executable
				// composition root or referenced from within its own implementation.
				if isMIQTFrontendImport(importPath) &&
					!pathWithin(relativeFilename, "cmd/ddgo") &&
					!pathWithin(relativeFilename, "internal/frontend/miqt") {
					t.Errorf(
						"%s imports concrete MIQT frontend %q; it may be imported only under cmd/ddgo or internal/frontend/miqt",
						relativeFilename,
						importPath,
					)
				}
			}

			return nil
		})
		if err != nil {
			t.Fatalf("inspect %s: %v", treeRoot, err)
		}
	}
}

func isMIQTBindingImport(importPath string) bool {
	return importPath == miqtBindingImport || strings.HasPrefix(importPath, miqtBindingImport+"/")
}

func isMIQTFrontendImport(importPath string) bool {
	return importPath == miqtFrontendImport || strings.HasPrefix(importPath, miqtFrontendImport+"/")
}

func pathWithin(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+"/")
}
