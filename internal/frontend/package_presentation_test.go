package frontend

import (
	"reflect"
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

func TestPresentPackageDiagnostics(t *testing.T) {
	shared := ddpackage.Diagnostic{Path: "intro.yml", Field: "line 2", Message: "normalized legacy step-field indentation"}
	differentPath := ddpackage.Diagnostic{Path: "other.yml", Field: "line 2", Message: shared.Message}
	differentField := ddpackage.Diagnostic{Path: "intro.yml", Field: "line 3", Message: shared.Message}
	tests := []struct {
		name       string
		warnings   []ddpackage.Diagnostic
		wantCounts []int
		wantText   string
	}{
		{
			name:       "empty",
			wantCounts: nil,
			wantText:   "",
		},
		{
			name:       "one warning",
			warnings:   []ddpackage.Diagnostic{shared},
			wantCounts: []int{1},
			wantText:   "1 warning — 1 distinct\n\nintro.yml — line 2: normalized legacy step-field indentation",
		},
		{
			name:       "preserves distinct path and field diagnostics",
			warnings:   []ddpackage.Diagnostic{shared, differentPath, differentField},
			wantCounts: []int{1, 1, 1},
			wantText: "3 warnings — 3 distinct\n\n" +
				"intro.yml — line 2: normalized legacy step-field indentation\n\n" +
				"other.yml — line 2: normalized legacy step-field indentation\n\n" +
				"intro.yml — line 3: normalized legacy step-field indentation",
		},
		{
			name:       "groups identical diagnostics in first occurrence order",
			warnings:   []ddpackage.Diagnostic{differentPath, shared, differentPath, shared, shared},
			wantCounts: []int{2, 3},
			wantText: "5 warnings — 2 distinct\n\n" +
				"other.yml — line 2: normalized legacy step-field indentation (2 occurrences)\n\n" +
				"intro.yml — line 2: normalized legacy step-field indentation (3 occurrences)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := append([]ddpackage.Diagnostic(nil), test.warnings...)
			got := PresentPackageDiagnostics(test.warnings)
			if got.Total != len(test.warnings) || len(got.Groups) != len(test.wantCounts) {
				t.Fatalf("presentation = %+v", got)
			}
			for index, count := range test.wantCounts {
				if got.Groups[index].Occurrences != count {
					t.Fatalf("group %d occurrences = %d, want %d", index, got.Groups[index].Occurrences, count)
				}
			}
			if got.Text() != test.wantText {
				t.Fatalf("Text() = %q, want %q", got.Text(), test.wantText)
			}
			if !reflect.DeepEqual(test.warnings, original) {
				t.Fatalf("warnings were modified: got %#v, want %#v", test.warnings, original)
			}
		})
	}
}
