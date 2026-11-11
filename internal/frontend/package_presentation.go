package frontend

import (
	"fmt"
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

// PackageDiagnosticGroup is one distinct compatibility diagnostic and the
// number of times it occurred while expanding a package.
type PackageDiagnosticGroup struct {
	Diagnostic  ddpackage.Diagnostic
	Occurrences int
}

// PackageDiagnosticsPresentation groups diagnostics for display only. It
// retains first-occurrence order and never changes the package's raw warnings.
type PackageDiagnosticsPresentation struct {
	Total  int
	Groups []PackageDiagnosticGroup
}

// PresentPackageDiagnostics groups warnings by their complete diagnostic
// identity. Path, field, and message all participate so distinct source
// locations retain their own presentation entries.
func PresentPackageDiagnostics(warnings []ddpackage.Diagnostic) PackageDiagnosticsPresentation {
	presentation := PackageDiagnosticsPresentation{Total: len(warnings)}
	indices := make(map[ddpackage.Diagnostic]int, len(warnings))
	for _, warning := range warnings {
		if index, ok := indices[warning]; ok {
			presentation.Groups[index].Occurrences++
			continue
		}
		indices[warning] = len(presentation.Groups)
		presentation.Groups = append(presentation.Groups, PackageDiagnosticGroup{
			Diagnostic:  warning,
			Occurrences: 1,
		})
	}
	return presentation
}

// Text returns plain, selectable diagnostic text suitable for a read-only UI
// control. The caller supplies the visible section heading.
func (p PackageDiagnosticsPresentation) Text() string {
	if p.Total == 0 {
		return ""
	}
	lines := make([]string, 0, len(p.Groups)+1)
	noun := "warnings"
	if p.Total == 1 {
		noun = "warning"
	}
	lines = append(lines, fmt.Sprintf("%d %s — %d distinct", p.Total, noun, len(p.Groups)))
	for _, group := range p.Groups {
		line := FormatPackageDiagnostic(group.Diagnostic)
		if group.Occurrences > 1 {
			line += fmt.Sprintf(" (%d occurrences)", group.Occurrences)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n\n")
}
