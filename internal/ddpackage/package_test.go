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

func TestLoadLegacyManifestFormattingCompatibility(t *testing.T) {
	manifest := "- job_name: Legacy package\n" +
		"  job_text: Important note: use the supplied fixture.\n" +
		"\t  # legacy separator\n" +
		"  job_steps:\n" +
		"\t \n" +
		"    -  step_name: Instructions\n" +
		"      step_markdown: |\n" +
		"First paragraph.\n\n" +
		"Second paragraph with **formatting**.\n" +
		"      step_text: Note: verify that the workspace is clear.\n" +
		"      step_image: Images/example.png\n" +
		"    - manifest_file: steps.yml\n" +
		"    - step_name: Final\n" +
		"      step_gcode: Code/final.nc\n" +
		"      timeout: 120 # ordinary YAML comment\n"
	submanifest := "- step_name: Sub step\n  step_text: Reminder: inspect the clamp.\n"
	pkg, err := Load(writePackage(t,
		zipEntry{name: manifestName, data: manifest},
		zipEntry{name: "steps.yml", data: submanifest},
		zipEntry{name: "Images/example.png", data: "image"},
		zipEntry{name: "Code/final.nc", data: "G0 X1\n"},
	))
	if err != nil {
		t.Fatalf("Load(legacy manifest) error: %v", err)
	}
	if len(pkg.Jobs) != 1 || pkg.Jobs[0].Name != "Legacy package" || pkg.Jobs[0].Text != "Important note: use the supplied fixture." {
		t.Fatalf("legacy job = %+v", pkg.Jobs)
	}
	steps := pkg.Jobs[0].Steps
	if len(steps) != 3 || steps[0].Name != "Instructions" || steps[1].Name != "Sub step" || steps[2].Name != "Final" {
		t.Fatalf("legacy step order = %+v", steps)
	}
	if steps[0].Markdown != "First paragraph.\n\nSecond paragraph with **formatting**.\n" || steps[0].Text != "Note: verify that the workspace is clear." || steps[0].ImagePath != "Images/example.png" {
		t.Fatalf("legacy Markdown/text = %+v", steps[0])
	}
	if steps[1].Text != "Reminder: inspect the clamp." || steps[1].ManifestPath != "steps.yml" || steps[2].GCodePath != "Code/final.nc" || steps[2].Timeout == nil || *steps[2].Timeout != 120 {
		t.Fatalf("legacy submanifest/resource metadata = %+v / %+v", steps[1], steps[2])
	}
	if len(pkg.Warnings) < 4 {
		t.Fatalf("legacy compatibility diagnostics = %+v", pkg.Warnings)
	}
}

func TestLoadStrictManifestDoesNotEmitCompatibilityWarning(t *testing.T) {
	manifest := `- job_name: "Strict: package"
  job_text: "Note: quoted"
  job_steps:
    - step_name: Instructions
      step_markdown: |
        First paragraph.

        Second paragraph.
      step_text: "Note: quoted text"
`
	pkg, err := Load(writePackage(t, zipEntry{name: manifestName, data: manifest}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Warnings) != 0 || pkg.Jobs[0].Steps[0].Markdown != "First paragraph.\n\nSecond paragraph.\n" || pkg.Jobs[0].Text != "Note: quoted" {
		t.Fatalf("strict manifest changed or warned: %+v / %+v", pkg, pkg.Warnings)
	}
}

func TestLegacyColonFallbackDoesNotRewriteIndentedMarkdown(t *testing.T) {
	manifest := `- job_name: Example
  job_steps:
    - step_name: Instructions
      step_markdown: |
        First paragraph.

        Second paragraph.
      step_text: Note: this triggers only the colon compatibility fallback.
`
	pkg, err := Load(writePackage(t, zipEntry{name: manifestName, data: manifest}))
	if err != nil {
		t.Fatal(err)
	}
	step := pkg.Jobs[0].Steps[0]
	if step.Markdown != "First paragraph.\n\nSecond paragraph.\n" || step.Text != "Note: this triggers only the colon compatibility fallback." {
		t.Fatalf("fallback rewrote standard Markdown: %+v", step)
	}
}

func TestLegacyCompatibilityRejectsUnsupportedTabsAndMalformedYAML(t *testing.T) {
	tests := []struct {
		name, manifest, want string
	}{
		{"tab indentation", "- job_name: x\n\tjob_steps: []\n", "tab in YAML content"},
		{"unterminated quote", "- job_name: \"unterminated\n", "parse YAML"},
		{"unindented structural content remains invalid", "- job_name: x\n  job_steps:\n    - step_name: x\n      step_markdown: |\n- step_name: not safely distinguishable\n", "has no job_name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg, err := Load(writePackage(t, zipEntry{name: manifestName, data: tt.manifest}))
			if err == nil || pkg != nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() = pkg=%v err=%v, want %q", pkg, err, tt.want)
			}
		})
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
		{"one backslash", []zipEntry{{name: manifestName, data: valid}, {name: "Code\\file.nc", data: "x"}}, "unsafe archive path"},
		{"two backslashes", []zipEntry{{name: manifestName, data: valid}, {name: "Code\\\\file.nc", data: "x"}}, "unsafe archive path"},
		{"Windows absolute path", []zipEntry{{name: manifestName, data: valid}, {name: "C:\\file.nc", data: "x"}}, "unsafe archive path"},
		{"UNC path", []zipEntry{{name: manifestName, data: valid}, {name: "\\\\server\\share\\file.nc", data: "x"}}, "unsafe archive path"},
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
	if pkg, err := Load(writePackage(t, zipEntry{name: manifestName, data: valid}, zipEntry{name: "Code/file.nc", data: "G0"})); err != nil || pkg == nil {
		t.Fatalf("forward-slash path Load() = pkg=%v err=%v", pkg, err)
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

func TestLoadExpandedStepLimitAppliesToEntirePackage(t *testing.T) {
	loadWithStepLimit := func(t *testing.T, manifest string, entries []zipEntry, maximum int) (*Package, error) {
		t.Helper()
		limits := DefaultLimits()
		limits.MaxExpandedSteps = maximum
		return LoadWithLimits(writePackage(t, append([]zipEntry{{name: manifestName, data: manifest}}, entries...)...), limits)
	}
	job := func(name string, count int) string {
		var text strings.Builder
		text.WriteString("- job_name: " + name + "\n  job_steps:\n")
		for i := 0; i < count; i++ {
			text.WriteString("    - step_name: step\n")
		}
		return text.String()
	}
	assertResult := func(t *testing.T, pkg *Package, err error, succeed bool) {
		t.Helper()
		if succeed && (err != nil || pkg == nil) {
			t.Fatalf("LoadWithLimits() = pkg=%v err=%v, want success", pkg, err)
		}
		if !succeed && (err == nil || pkg != nil || !strings.Contains(err.Error(), "expanded steps")) {
			t.Fatalf("LoadWithLimits() = pkg=%v err=%v, want shared step-limit error and nil package", pkg, err)
		}
	}

	pkg, err := loadWithStepLimit(t, job("one", 3), nil, 3)
	assertResult(t, pkg, err, true)
	pkg, err = loadWithStepLimit(t, job("one", 4), nil, 3)
	assertResult(t, pkg, err, false)

	pkg, err = loadWithStepLimit(t, job("one", 2)+job("two", 2), nil, 4)
	assertResult(t, pkg, err, true)
	if pkg.Jobs[1].Steps[0].ID != "job-2-step-1" {
		t.Fatalf("second-job step ID changed: %+v", pkg.Jobs[1].Steps[0])
	}
	pkg, err = loadWithStepLimit(t, job("one", 2)+job("two", 2), nil, 3)
	assertResult(t, pkg, err, false)

	repeated := `- job_name: one
  job_steps:
    - manifest_file: shared.yml
- job_name: two
  job_steps:
    - manifest_file: shared.yml
`
	pkg, err = loadWithStepLimit(t, repeated, []zipEntry{{name: "shared.yml", data: "- step_name: imported\n"}}, 2)
	assertResult(t, pkg, err, true)
	pkg, err = loadWithStepLimit(t, repeated, []zipEntry{{name: "shared.yml", data: "- step_name: imported\n"}}, 1)
	assertResult(t, pkg, err, false)

	nested := `- job_name: nested
  job_steps:
    - manifest_file: outer.yml
    - manifest_file: outer.yml
`
	entries := []zipEntry{{name: "outer.yml", data: "- manifest_file: inner.yml\n"}, {name: "inner.yml", data: "- step_name: nested-step\n"}}
	pkg, err = loadWithStepLimit(t, nested, entries, 2)
	assertResult(t, pkg, err, true)
	pkg, err = loadWithStepLimit(t, nested, entries, 1)
	assertResult(t, pkg, err, false)
}

func TestLoadManifestSizeLimitsApplyToReferencedContent(t *testing.T) {
	loadWithLimits := func(t *testing.T, manifest string, entries []zipEntry, manifestLimit, resourceLimit int64) (*Package, error) {
		t.Helper()
		limits := DefaultLimits()
		limits.MaxManifestBytes = manifestLimit
		limits.MaxResourceBytes = resourceLimit
		limits.MaxTotalUncompressedBytes = 4096
		return LoadWithLimits(writePackage(t, append([]zipEntry{{name: manifestName, data: manifest}}, entries...)...), limits)
	}
	assertFailure := func(t *testing.T, pkg *Package, err error, path string) {
		t.Helper()
		if err == nil || pkg != nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "manifest") {
			t.Fatalf("LoadWithLimits() = pkg=%v err=%v, want manifest-limit error for %q", pkg, err, path)
		}
	}

	rootOversized := "- job_name: x\n  job_steps: []\n#" + strings.Repeat("x", 80)
	pkg, err := loadWithLimits(t, rootOversized, nil, 64, 512)
	assertFailure(t, pkg, err, manifestName)

	directRoot := "- job_name: x\n  job_steps:\n    - manifest_file: Code/steps.txt\n"
	pkg, err = loadWithLimits(t, directRoot, []zipEntry{{name: "Code/steps.txt", data: "- step_name: x #" + strings.Repeat("x", 100)}}, int64(len(directRoot)+1), 512)
	assertFailure(t, pkg, err, "Code/steps.txt")

	nestedRoot := "- job_name: x\n  job_steps:\n    - manifest_file: first\n"
	first := "- manifest_file: second\n"
	pkg, err = loadWithLimits(t, nestedRoot, []zipEntry{{name: "first", data: first}, {name: "second", data: "- step_name: x #" + strings.Repeat("x", 100)}}, int64(len(nestedRoot)+1), 512)
	assertFailure(t, pkg, err, "second")

	jobLevelRoot := "- job_name: x\n  manifest_file: job-steps\n"
	pkg, err = loadWithLimits(t, jobLevelRoot, []zipEntry{{name: "job-steps", data: "- step_name: x #" + strings.Repeat("x", 100)}}, int64(len(jobLevelRoot)+1), 512)
	assertFailure(t, pkg, err, "job-steps")

	exactRoot := "- job_name: x\n  job_steps:\n    - manifest_file: exact\n"
	exactLimit := int64(128)
	exactSubmanifest := "- step_name: exact #" + strings.Repeat("x", int(exactLimit)-len("- step_name: exact #"))
	pkg, err = loadWithLimits(t, exactRoot, []zipEntry{{name: "exact", data: exactSubmanifest}}, exactLimit, 512)
	if err != nil || pkg == nil || len(pkg.Jobs[0].Steps) != 1 {
		t.Fatalf("exact manifest limit = pkg=%v err=%v", pkg, err)
	}

	ordinaryRoot := "- job_name: x\n  job_steps:\n    - step_gcode: Code/large\n"
	pkg, err = loadWithLimits(t, ordinaryRoot, []zipEntry{{name: "Code/large", data: strings.Repeat("G", 150)}}, 100, 200)
	if err != nil || pkg == nil {
		t.Fatalf("ordinary resource above manifest limit = pkg=%v err=%v", pkg, err)
	}
}

func TestLoadTotalAndCompressionRatioLimits(t *testing.T) {
	manifest := "- job_name: x\n  job_steps: []\n"
	first, second := strings.Repeat("a", 80), strings.Repeat("b", 80)
	exactTotal := int64(len(manifest) + len(first) + len(second))
	limits := DefaultLimits()
	limits.MaxManifestBytes = 100
	limits.MaxResourceBytes = 100
	limits.MaxTotalUncompressedBytes = exactTotal
	path := writePackage(t, zipEntry{name: manifestName, data: manifest}, zipEntry{name: "one", data: first}, zipEntry{name: "two", data: second})
	pkg, err := LoadWithLimits(path, limits)
	if err != nil || pkg == nil {
		t.Fatalf("exact total limit = pkg=%v err=%v", pkg, err)
	}
	limits.MaxTotalUncompressedBytes--
	pkg, err = LoadWithLimits(path, limits)
	if err == nil || pkg != nil || !strings.Contains(err.Error(), "total limit") {
		t.Fatalf("total limit = pkg=%v err=%v", pkg, err)
	}

	ratioPath := writePackage(t, zipEntry{name: manifestName, data: manifest}, zipEntry{name: "arbitrary-resource", data: strings.Repeat("z", 16<<10)})
	zr, err := zip.OpenReader(ratioPath)
	if err != nil {
		t.Fatal(err)
	}
	var resource *zip.File
	for _, file := range zr.File {
		if file.Name == "arbitrary-resource" {
			resource = file
			break
		}
	}
	if resource == nil || resource.CompressedSize64 == 0 {
		t.Fatal("missing compressed fixture resource")
	}
	requiredRatio := (resource.UncompressedSize64 + resource.CompressedSize64 - 1) / resource.CompressedSize64
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}
	if requiredRatio < 2 {
		t.Fatalf("fixture was not sufficiently compressible: %d", requiredRatio)
	}
	limits = DefaultLimits()
	limits.MaxManifestBytes = 1024
	limits.MaxResourceBytes = 32 << 10
	limits.MaxTotalUncompressedBytes = 64 << 10
	limits.MaxCompressionRatio = requiredRatio
	pkg, err = LoadWithLimits(ratioPath, limits)
	if err != nil || pkg == nil {
		t.Fatalf("ratio boundary acceptance = pkg=%v err=%v", pkg, err)
	}
	limits.MaxCompressionRatio = requiredRatio - 1
	pkg, err = LoadWithLimits(ratioPath, limits)
	if err == nil || pkg != nil || !strings.Contains(err.Error(), "compression ratio") {
		t.Fatalf("ratio rejection = pkg=%v err=%v", pkg, err)
	}
}
