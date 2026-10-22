package frontend

import (
	"strings"

	"github.com/ianbruene/ddgo/internal/ddpackage"
)

// FormatPackageDiagnostic preserves every useful DD-A warning component while
// keeping optional path and field separators readable in the package window.
func FormatPackageDiagnostic(d ddpackage.Diagnostic) string {
	prefix := strings.TrimSpace(d.Path)
	if field := strings.TrimSpace(d.Field); field != "" {
		if prefix != "" {
			prefix += " — " + field
		} else {
			prefix = field
		}
	}
	message := strings.TrimSpace(d.Message)
	if prefix == "" {
		return message
	}
	if message == "" {
		return prefix
	}
	return prefix + ": " + message
}
