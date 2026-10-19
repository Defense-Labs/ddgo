package ddpackage

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type zipEntry struct {
	name string
	data string
	mode os.FileMode
}

func writePackage(t *testing.T, entries ...zipEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.dd")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		out, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write([]byte(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadStructuredPackageAndResources(t *testing.T) {
	manifest := `# root manifest
- job_name: Receiver
  job_text: "Install\\nfixture"
  guide_files:
    - Guides/guide.pdf
  model_files:
    - Models/jig.stl
  min_fw_version: 20210609
  min_ddcut_version: 5.2.2
  disable_wcs_clear_prompt: true
  wcs_value_check: G54 X1 Y2 Z-3
  wcs_check_failed_message: "WCS\\nwrong"
  future_job_field: retained
  job_steps:
    - step_name: Explain
      step_text: "Read\\nthis"
      step_markdown: |
        ## Heading
        * preserve markdown
      step_image: Images/step.gif
    - step_name: Cut
      step_gcode: Code/no-extension
      pause: true
      reset: true
      timeout: 80
      unskippable: true
      if: fixture == 1
      step_goto: Explain
      popup_text: "Continue\\ncarefully"
      popup_title: Confirm
      popup_wait_time: 12
      popup_yes_step: Cut
      popup_no_step: Explain
      future_step_field: retained
`
	path := writePackage(t,
		zipEntry{name: manifestName, data: manifest},
		zipEntry{name: "Guides/guide.pdf", data: "guide"},
		zipEntry{name: "Models/jig.stl", data: "model"},
		zipEntry{name: "Images/step.gif", data: "image"},
		zipEntry{name: "Code/no-extension", data: "G0 X1\nM5\n"},
		zipEntry{name: "Additional Files/readme.txt", data: "extract me"},
	)
	pkg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if pkg.DisplayName != "fixture" || len(pkg.Jobs) != 1 || len(pkg.Assets) != 6 || len(pkg.AdditionalFiles) != 1 {
		t.Fatalf("package summary = %+v jobs=%d assets=%d additional=%d", pkg, len(pkg.Jobs), len(pkg.Assets), len(pkg.AdditionalFiles))
	}
	job := pkg.Jobs[0]
	if job.Name != "Receiver" || job.Text != "Install\nfixture" || job.MinFirmwareVersion != "20210609" || job.MinDDCutVersion != "5.2.2" || !job.DisableWCSClearPrompt || job.WCSValueCheck != "G54 X1 Y2 Z-3" || job.WCSCheckFailedMessage != "WCS\nwrong" {
		t.Fatalf("job metadata = %+v", job)
	}
	if got := job.Metadata["future_job_field"]; got != "retained" || len(job.GuideFiles) != 1 || len(job.ModelFiles) != 1 {
		t.Fatalf("job preserved metadata/resources = %+v", job)
	}
	if len(job.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(job.Steps))
	}
	explain, cut := job.Steps[0], job.Steps[1]
	if explain.ID != "job-1-step-1" || explain.Text != "Read\nthis" || !strings.Contains(explain.Markdown, "## Heading\n") || explain.ImagePath != "Images/step.gif" {
		t.Fatalf("text step = %+v", explain)
	}
	if cut.ID != "job-1-step-2" || cut.GCodePath != "Code/no-extension" || !cut.Pause || !cut.Reset || cut.Timeout == nil || *cut.Timeout != 80 || !cut.Unskippable || cut.Condition != "fixture == 1" || cut.JumpTarget != "Explain" || cut.PopupText != "Continue\ncarefully" || cut.PopupWaitTime == nil || *cut.PopupWaitTime != 12 || cut.PopupYesTarget != "Cut" || cut.PopupNoTarget != "Explain" {
		t.Fatalf("cut step = %+v", cut)
	}
	if got := cut.Metadata["future_step_field"]; got != "retained" || len(pkg.Warnings) != 2 {
		t.Fatalf("step metadata/warnings = %+v / %+v", cut.Metadata, pkg.Warnings)
	}
	gcode, err := pkg.ReadStepGCode(cut)
	if err != nil || string(gcode) != "G0 X1\nM5\n" {
		t.Fatalf("ReadStepGCode() = %q, %v", gcode, err)
	}
	gcode[0] = 'X'
	again, err := pkg.ReadResource("Code/no-extension")
	if err != nil || string(again) != "G0 X1\nM5\n" {
		t.Fatalf("resource contents were mutable: %q, %v", again, err)
	}
}

func TestLoadExpandsNestedAndRepeatedSubmanifestsInOrder(t *testing.T) {
	root := `- job_name: Ordered
  job_steps:
    - step_name: first
    - manifest_file: shared.yml
    - step_name: middle
    - manifest_file: shared.yml
    - step_name: last
`
	shared := `- step_name: shared-one
- manifest_file: nested.yml
`
	nested := `- step_name: nested
`
	pkg, err := Load(writePackage(t,
		zipEntry{name: manifestName, data: root},
		zipEntry{name: "shared.yml", data: shared},
		zipEntry{name: "nested.yml", data: nested},
	))
	if err != nil {
		t.Fatal(err)
	}
	steps := pkg.Jobs[0].Steps
	wantNames := []string{"first", "shared-one", "nested", "middle", "shared-one", "nested", "last"}
	if len(steps) != len(wantNames) {
		t.Fatalf("expanded steps = %d, want %d", len(steps), len(wantNames))
	}
	for i, want := range wantNames {
		if steps[i].Name != want || steps[i].ID != "job-1-step-"+strconv.Itoa(i+1) {
			t.Errorf("step %d = %+v, want %q", i, steps[i], want)
		}
	}
	if steps[1].ManifestPath != "shared.yml" || steps[2].ManifestPath != "nested.yml" || steps[4].ManifestPath != "shared.yml" {
		t.Fatalf("submanifest origins = %q / %q / %q", steps[1].ManifestPath, steps[2].ManifestPath, steps[4].ManifestPath)
	}
}

func TestLoadSupportsJobLevelSubmanifest(t *testing.T) {
	root := `- job_name: Imported
  manifest_file: steps.yml
  job_steps:
    - step_name: local
`
	pkg, err := Load(writePackage(t, zipEntry{name: manifestName, data: root}, zipEntry{name: "steps.yml", data: "- step_name: imported\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{pkg.Jobs[0].Steps[0].Name, pkg.Jobs[0].Steps[1].Name}; strings.Join(got, ",") != "imported,local" {
		t.Fatalf("job-level include order = %v", got)
	}
}

func TestLoadPreservesMultipleJobs(t *testing.T) {
	pkg, err := Load(writePackage(t, zipEntry{name: manifestName, data: `- job_name: First
  job_steps:
    - step_name: first-step
- job_name: Second
  job_text: second job
  job_steps:
    - step_name: second-step
`}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Jobs) != 2 || pkg.Jobs[0].Name != "First" || pkg.Jobs[1].Name != "Second" || pkg.Jobs[0].Steps[0].ID != "job-1-step-1" || pkg.Jobs[1].Steps[0].ID != "job-2-step-1" {
		t.Fatalf("multiple jobs = %+v", pkg.Jobs)
	}
}

func TestLoadRejectsMissingAndMalformedRequiredStructure(t *testing.T) {
	tests := []struct {
		name    string
		entries []zipEntry
		want    string
	}{
		{"missing manifest", []zipEntry{{name: "Code/a", data: "G0"}}, "required root manifest.yml"},
		{"invalid YAML", []zipEntry{{name: manifestName, data: "- job_name: ["}}, "parse YAML"},
		{"multiple YAML documents", []zipEntry{{name: manifestName, data: "- job_name: one\n---\n- job_name: two\n"}}, "must contain one document"},
		{"wrong root kind", []zipEntry{{name: manifestName, data: "job_name: no-list"}}, "must be a YAML sequence"},
		{"missing gcode", []zipEntry{{name: manifestName, data: "- job_name: x\n  job_steps:\n    - step_gcode: Code/missing\n"}}, "missing resource"},
		{"case sensitive resource", []zipEntry{{name: manifestName, data: "- job_name: x\n  job_steps:\n    - step_gcode: code/a\n"}, {name: "Code/a", data: "G0"}}, "missing resource"},
		{"missing submanifest", []zipEntry{{name: manifestName, data: "- job_name: x\n  job_steps:\n    - manifest_file: missing.yml\n"}}, "missing resource"},
		{"malformed submanifest", []zipEntry{{name: manifestName, data: "- job_name: x\n  job_steps:\n    - manifest_file: child.yml\n"}, {name: "child.yml", data: "- step_name: ["}}, "child.yml"},
		{"step mixes include", []zipEntry{{name: manifestName, data: "- job_name: x\n  job_steps:\n    - manifest_file: child.yml\n      step_name: invalid\n"}, {name: "child.yml", data: "- step_name: child\n"}}, "mixes manifest_file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg, err := Load(writePackage(t, tt.entries...))
			if err == nil || !strings.Contains(err.Error(), tt.want) || pkg != nil {
				t.Fatalf("Load() = pkg=%v err=%v, want error containing %q and no package", pkg, err, tt.want)
			}
		})
	}
}

func TestLoadRejectsCircularSubmanifest(t *testing.T) {
	pkg, err := Load(writePackage(t,
		zipEntry{name: manifestName, data: "- job_name: x\n  job_steps:\n    - manifest_file: a.yml\n"},
		zipEntry{name: "a.yml", data: "- manifest_file: b.yml\n"},
		zipEntry{name: "b.yml", data: "- manifest_file: a.yml\n"},
	))
	if err == nil || !strings.Contains(err.Error(), "circular submanifest") || pkg != nil {
		t.Fatalf("Load() = pkg=%v err=%v", pkg, err)
	}
}

func TestLoadRejectsUnsafeAndDuplicateArchiveEntries(t *testing.T) {
	valid := "- job_name: x\n  job_steps: []\n"
	tests := []struct {
		name    string
		entries []zipEntry
		want    string
	}{
		{"parent traversal", []zipEntry{{name: manifestName, data: valid}, {name: "../escape", data: "x"}}, "unsafe archive path"},
		{"absolute path", []zipEntry{{name: manifestName, data: valid}, {name: "/escape", data: "x"}}, "unsafe archive path"},
		{"drive path", []zipEntry{{name: manifestName, data: valid}, {name: "C:escape", data: "x"}}, "unsafe archive path"},
		{"backslash path", []zipEntry{{name: manifestName, data: valid}, {name: `Code\\escape`, data: "x"}}, "unsafe archive path"},
		{"duplicate", []zipEntry{{name: manifestName, data: valid}, {name: "Code/a", data: "x"}, {name: "Code/a", data: "y"}}, "duplicate archive path"},
		{"symlink", []zipEntry{{name: manifestName, data: valid}, {name: "link", data: "target", mode: os.ModeSymlink | 0o777}}, "symbolic-link"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg, err := Load(writePackage(t, tt.entries...))
			if err == nil || !strings.Contains(err.Error(), tt.want) || pkg != nil {
				t.Fatalf("Load() = pkg=%v err=%v", pkg, err)
			}
		})
	}
}

func TestLoadEnforcesArchiveLimitsAndDoesNotDependOnDiskAfterLoad(t *testing.T) {
	manifest := "- job_name: x\n  job_steps:\n    - step_gcode: Code/cut\n"
	path := writePackage(t, zipEntry{name: manifestName, data: manifest}, zipEntry{name: "Code/cut", data: "G0 X1"}, zipEntry{name: "large.bin", data: strings.Repeat("x", 128)})
	limits := DefaultLimits()
	limits.MaxManifestBytes = 96
	limits.MaxResourceBytes = 96
	pkg, err := LoadWithLimits(path, limits)
	if err == nil || pkg != nil || !strings.Contains(err.Error(), "per-resource limit") {
		t.Fatalf("limited Load() = pkg=%v err=%v", pkg, err)
	}

	pkg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// Replacing the backing file cannot alter the already-loaded resource map.
	if err := os.WriteFile(path, []byte("not a ZIP"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := pkg.ReadResource("Code/cut")
	if err != nil || !bytes.Equal(data, []byte("G0 X1")) {
		t.Fatalf("snapshot resource = %q, %v", data, err)
	}
}

func TestLoadEnforcesEntryAndExpansionLimits(t *testing.T) {
	manifest := "- job_name: x\n  job_steps:\n    - step_name: one\n    - step_name: two\n"
	path := writePackage(t, zipEntry{name: manifestName, data: manifest}, zipEntry{name: "unused", data: "x"})
	limits := DefaultLimits()
	limits.MaxEntries = 1
	if pkg, err := LoadWithLimits(path, limits); err == nil || pkg != nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("entry limit Load() = pkg=%v err=%v", pkg, err)
	}

	limits = DefaultLimits()
	limits.MaxExpandedSteps = 1
	if pkg, err := LoadWithLimits(path, limits); err == nil || pkg != nil || !strings.Contains(err.Error(), "expanded steps") {
		t.Fatalf("step limit Load() = pkg=%v err=%v", pkg, err)
	}

	depthPath := writePackage(t,
		zipEntry{name: manifestName, data: "- job_name: x\n  job_steps:\n    - manifest_file: one.yml\n"},
		zipEntry{name: "one.yml", data: "- manifest_file: two.yml\n"},
		zipEntry{name: "two.yml", data: "- step_name: nested\n"},
	)
	limits = DefaultLimits()
	limits.MaxInclusionDepth = 1
	if pkg, err := LoadWithLimits(depthPath, limits); err == nil || pkg != nil || !strings.Contains(err.Error(), "inclusion depth") {
		t.Fatalf("depth limit Load() = pkg=%v err=%v", pkg, err)
	}
}
