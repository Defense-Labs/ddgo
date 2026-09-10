package gcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDocumentPreservesSource(t *testing.T) {
	source := "(header)\n\n  G0 X1\n; comment\nG1 X2"
	path := filepath.Join(t.TempDir(), "part.nc")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadDocument("  " + path + "  ")
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if doc.Path != path || doc.Name != "part.nc" {
		t.Fatalf("identity = path %q, name %q", doc.Path, doc.Name)
	}
	if doc.Source != source {
		t.Fatalf("source changed:\nwant %q\n got %q", source, doc.Source)
	}
	if lines := strings.Split(doc.Source, "\n"); len(lines) != 5 {
		t.Fatalf("physical line count = %d, want 5", len(lines))
	}
}

func TestLoadDocumentRejectsEmptyPath(t *testing.T) {
	if _, err := LoadDocument(" \t\n"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestLoadDocumentReportsReadError(t *testing.T) {
	_, err := LoadDocument(filepath.Join(t.TempDir(), "missing.nc"))
	if err == nil || !strings.Contains(err.Error(), "read document") {
		t.Fatalf("error = %v, want wrapped read error", err)
	}
}
