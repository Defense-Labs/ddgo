package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/ddpackage"
)

func TestFormatPackageDiagnostic(t *testing.T) {
	for _, test := range []struct {
		name string
		d    ddpackage.Diagnostic
		want string
	}{
		{"complete", ddpackage.Diagnostic{Path: "manifest.yml", Field: "unknown_property", Message: "unknown job metadata preserved"}, "manifest.yml — unknown_property: unknown job metadata preserved"},
		{"no field", ddpackage.Diagnostic{Path: "steps.yml", Message: "compatibility note"}, "steps.yml: compatibility note"},
		{"no path", ddpackage.Diagnostic{Field: "strange.field/name", Message: "retained"}, "strange.field/name: retained"},
		{"message only", ddpackage.Diagnostic{Message: "note"}, "note"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := FormatPackageDiagnostic(test.d); got != test.want {
				t.Fatalf("FormatPackageDiagnostic(%+v) = %q, want %q", test.d, got, test.want)
			}
		})
	}
}
