//go:build miqt

package ui

import (
	"path/filepath"
	"strings"

	qt "github.com/mappu/miqt/qt"
)

const (
	allFilesFilter     = "All Files (*)"
	programFileFilters = allFilesFilter + ";;G-code Files (*.gcode *.gc *.nc *.tap *.ngc)"
)

func chooseProgramFile(parent *qt.QWidget) string {
	dialog := qt.NewQFileDialog(parent)
	dialog.SetWindowTitle("Open G-code File")
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
