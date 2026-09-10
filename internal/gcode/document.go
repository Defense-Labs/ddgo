package gcode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Document is the faithful, display-oriented representation of a G-code file.
// Unlike Program, its source is not parsed or normalized.
type Document struct {
	Path   string
	Name   string
	Source string
}

// LoadDocument reads a G-code document without changing its source text.
func LoadDocument(path string) (Document, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Document{}, errors.New("document path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("read document: %w", err)
	}
	return Document{Path: path, Name: filepath.Base(path), Source: string(data)}, nil
}
