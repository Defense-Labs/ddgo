package gcode

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	src := "\ufeff(heading)\n; full line comment\nG21\nG90 ; abs\nG0 X1 Y2 (move)\nM3 (spindle) S1000\n\n"
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []Line{{Number: 3, Raw: "G21", Text: "G21"}, {Number: 4, Raw: "G90 ; abs", Text: "G90"}, {Number: 5, Raw: "G0 X1 Y2 (move)", Text: "G0 X1 Y2"}, {Number: 6, Raw: "M3 (spindle) S1000", Text: "M3 S1000"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
	}
}

func TestParsePreservesRawAndCleanedCompatibility(t *testing.T) {
	t.Parallel()

	src := "  G1   X1   Y2   (move); trailing comment  \nM999X1Y2 ; macro comment\n"
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got[0].Raw != "G1   X1   Y2   (move); trailing comment" {
		t.Fatalf("Raw = %q", got[0].Raw)
	}
	if got[0].Text != "G1 X1 Y2" {
		t.Fatalf("Text = %q, want sanitized output", got[0].Text)
	}
	if got[1].Raw != "M999X1Y2 ; macro comment" {
		t.Fatalf("Raw = %q", got[1].Raw)
	}
	if got[1].Text != "M999X1Y2" {
		t.Fatalf("Text = %q, want compact sanitized output", got[1].Text)
	}
}

func TestParseRawPreservesParenthesesSemicolonsAndLineNumbers(t *testing.T) {
	t.Parallel()

	src := "\n(comment only)\nG90 (absolute)\n; comment only\nG0 X1 ; semicolon comment\n"
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []Line{{Number: 3, Raw: "G90 (absolute)", Text: "G90"}, {Number: 5, Raw: "G0 X1 ; semicolon comment", Text: "G0 X1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
	}
}

func TestParse_EmptyProgram(t *testing.T) {
	t.Parallel()

	if _, err := Parse("(comment only)\n; another\n\n"); err == nil {
		t.Fatal("Parse() error = nil, want non-nil")
	}
}

func TestLoadFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "demo.gcode")
	if err := os.WriteFile(path, []byte("G0 X1\nG0 Y2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prog, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if got, want := prog.Name, "demo.gcode"; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	if got, want := len(prog.Lines), 2; got != want {
		t.Fatalf("len(Lines) = %d, want %d", got, want)
	}
}

func TestProgramFromDocumentUsesRetainedSource(t *testing.T) {
	t.Parallel()

	document := Document{
		Name:   "memory.gcode",
		Source: "(heading)\nG0 X1\n\nG1 Y2 ; move\n",
	}
	program, err := ProgramFromDocument(document)
	if err != nil {
		t.Fatalf("ProgramFromDocument() error = %v", err)
	}
	if program.Path != "" || program.Name != document.Name {
		t.Fatalf("program metadata = path %q name %q", program.Path, program.Name)
	}
	want := []Line{
		{Number: 2, Raw: "G0 X1", Text: "G0 X1"},
		{Number: 4, Raw: "G1 Y2 ; move", Text: "G1 Y2"},
	}
	if !reflect.DeepEqual(program.Lines, want) {
		t.Fatalf("program lines = %#v, want %#v", program.Lines, want)
	}
}

func TestProgramSnapshotOwnsLineSlice(t *testing.T) {
	t.Parallel()

	original := Program{
		Name:  "memory.gcode",
		Lines: []Line{{Number: 7, Raw: "G0 X1", Text: "G0 X1"}},
	}
	snapshot, err := original.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	original.Lines[0] = Line{Number: 8, Raw: "G0 X9", Text: "G0 X9"}
	original.Lines = append(original.Lines, Line{Number: 9, Raw: "M5", Text: "M5"})

	want := []Line{{Number: 7, Raw: "G0 X1", Text: "G0 X1"}}
	if !reflect.DeepEqual(snapshot.Lines, want) {
		t.Fatalf("snapshot lines = %#v, want %#v", snapshot.Lines, want)
	}
}

func TestProgramSnapshotRejectsInvalidPrograms(t *testing.T) {
	t.Parallel()

	tests := []Program{
		{},
		{Lines: []Line{{Number: 1, Text: "   "}}},
		{Lines: []Line{{Number: 1, Text: "G0 X1\nG0 X2"}}},
	}
	for _, program := range tests {
		if _, err := program.Snapshot(); err == nil {
			t.Fatalf("Snapshot(%#v) error = nil, want non-nil", program)
		}
	}
}
