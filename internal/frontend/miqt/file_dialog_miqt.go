//go:build miqt

package miqt

import (
	"path/filepath"
	"strings"

	qt "github.com/mappu/miqt/qt6"
)

const (
	allFilesFilter     = "All Files (*)"
	programFileFilters = "G-code Files (*.gcode *.gc *.nc *.tap *.ngc);;DD Package Files (*.dd);;" + allFilesFilter
)

func chooseProgramFile(parent *qt.QWidget) string {
	dialog := qt.NewQFileDialog(parent)
	dialog.SetWindowTitle("Open File")
	dialog.SetFileMode(qt.QFileDialog__ExistingFile)
	dialog.SetNameFilter(programFileFilters)
	dialog.SelectNameFilter(allFilesFilter)
	dialog.Exec()
	files := dialog.SelectedFiles()
	if len(files) == 0 {
		return ""
	}
	path := strings.TrimSpace(files[0])
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}
