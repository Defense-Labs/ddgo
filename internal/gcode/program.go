package gcode

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Line struct {
	Number int
	Raw    string
	Text   string
}

type Program struct {
	Path  string
	Name  string
	Lines []Line
}

// ProgramFromDocument parses the source already retained by a document. It
// does not reread Document.Path, so pathless documents and future packaged
// documents can provide executable programs from their in-memory source.
func ProgramFromDocument(document Document) (Program, error) {
	lines, err := Parse(document.Source)
	if err != nil {
		return Program{}, err
	}
	return Program{
		Path:  document.Path,
		Name:  document.Name,
		Lines: lines,
	}, nil
}

// Snapshot returns an independently owned, validated execution snapshot.
// Line strings are immutable; copying the slice prevents callers from
// replacing or reordering instructions after a run has been admitted.
func (p Program) Snapshot() (Program, error) {
	if len(p.Lines) == 0 {
		return Program{}, errors.New("program contains no runnable gcode lines")
	}
	lines := make([]Line, len(p.Lines))
	copy(lines, p.Lines)
	for _, line := range lines {
		if strings.TrimSpace(line.Text) == "" || strings.ContainsAny(line.Text, "\r\n") {
			return Program{}, errors.New("program contains an invalid executable line")
		}
	}
	return Program{Path: p.Path, Name: p.Name, Lines: lines}, nil
}

func LoadFile(path string) (Program, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Program{}, errors.New("program path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Program{}, fmt.Errorf("read program: %w", err)
	}
	lines, err := Parse(string(data))
	if err != nil {
		return Program{}, err
	}
	return Program{
		Path:  path,
		Name:  filepath.Base(path),
		Lines: lines,
	}, nil
}

func Parse(src string) ([]Line, error) {
	scanner := bufio.NewScanner(strings.NewReader(src))
	out := make([]Line, 0, 128)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := normalizeRawLine(scanner.Text())
		line := sanitizeLine(raw)
		if line == "" {
			continue
		}
		out = append(out, Line{Number: lineNo, Raw: raw, Text: line})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan program: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("program contains no runnable gcode lines")
	}
	return out, nil
}

func normalizeRawLine(line string) string {
	line = strings.TrimPrefix(line, "\ufeff")
	return strings.TrimSpace(line)
}

func sanitizeLine(line string) string {
	line = stripParenComments(line)
	if idx := strings.IndexByte(line, ';'); idx >= 0 {
		line = line[:idx]
	}
	return strings.Join(strings.Fields(strings.TrimSpace(line)), " ")
}

func stripParenComments(line string) string {
	var b strings.Builder
	depth := 0
	for _, r := range line {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
				continue
			}
			b.WriteRune(r)
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
