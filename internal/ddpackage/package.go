// Package ddpackage loads Ghost Gunner .dd archives as read-only data.
//
// It intentionally knows nothing about Qt, controller connections, or program
// execution. Parsing a package is not evidence that it is safe to run.
package ddpackage

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const manifestName = "manifest.yml"

// Limits bounds untrusted archive and manifest processing. Defaults are sized
// for ordinary CNC packages while avoiding unbounded decompression or memory.
type Limits struct {
	MaxEntries                int
	MaxManifestBytes          int64
	MaxResourceBytes          int64
	MaxTotalUncompressedBytes int64
	MaxInclusionDepth         int
	MaxExpandedSteps          int
	MaxCompressionRatio       uint64
}

// DefaultLimits returns the limits used by Load.
func DefaultLimits() Limits {
	return Limits{
		MaxEntries:                4096,
		MaxManifestBytes:          1 << 20,  // 1 MiB
		MaxResourceBytes:          32 << 20, // 32 MiB per file
		MaxTotalUncompressedBytes: 128 << 20,
		MaxInclusionDepth:         16,
		MaxExpandedSteps:          10000,
		MaxCompressionRatio:       200,
	}
}

// Diagnostic records a non-fatal format compatibility observation.
type Diagnostic struct {
	Path    string
	Field   string
	Message string
}

// Asset describes a regular archive resource. Path lookup is exact and case
// sensitive, even on case-insensitive host filesystems.
type Asset struct {
	Path           string
	Size           int64
	AdditionalFile bool
}

// Package is a fully in-memory, read-only package snapshot. Resource bytes are
// copied from the archive at Load time, so later on-disk changes cannot affect
// it and no archive file handle remains open.
type Package struct {
	SourcePath      string
	DisplayName     string
	Jobs            []Job
	Assets          []Asset
	AdditionalFiles []Asset
	Warnings        []Diagnostic

	resources map[string][]byte
}

// Job is one ordered guided job declared in the root manifest.
type Job struct {
	Name                  string
	Text                  string
	Steps                 []Step
	GuideFiles            []string
	ModelFiles            []string
	MinDDCutVersion       string
	MinFirmwareVersion    string
	DisableWCSClearPrompt bool
	WCSValueCheck         string
	WCSCheckFailedMessage string
	ManifestPath          string
	Metadata              map[string]any
}

// Step is a data-only operation. Its future execution semantics are preserved
// as metadata and are deliberately not acted upon by this package.
type Step struct {
	ID             string
	Name           string
	Text           string
	Markdown       string
	ImagePath      string
	GCodePath      string
	Timeout        *int
	Pause          bool
	Reset          bool
	Unskippable    bool
	Condition      string
	JumpTarget     string
	PopupText      string
	PopupTitle     string
	PopupWaitTime  *int
	PopupYesTarget string
	PopupNoTarget  string
	ManifestPath   string
	Metadata       map[string]any
}

// ReadResource returns an independent copy of an archived resource. Archive
// paths are resolved exactly and are never passed to host filesystem APIs.
func (p *Package) ReadResource(name string) ([]byte, error) {
	if p == nil {
		return nil, errors.New("nil .dd package")
	}
	data, ok := p.resources[name]
	if !ok {
		return nil, fmt.Errorf("package resource %q not found", name)
	}
	return bytes.Clone(data), nil
}

// ReadStepGCode returns the original, unmodified source referenced by a step.
func (p *Package) ReadStepGCode(step Step) ([]byte, error) {
	if step.GCodePath == "" {
		return nil, fmt.Errorf("step %q has no G-code resource", step.ID)
	}
	return p.ReadResource(step.GCodePath)
}

// HasResource reports an exact archive-path match.
func (p *Package) HasResource(name string) bool {
	if p == nil {
		return false
	}
	_, ok := p.resources[name]
	return ok
}

// Load opens a .dd ZIP archive using DefaultLimits.
func Load(sourcePath string) (*Package, error) { return LoadWithLimits(sourcePath, DefaultLimits()) }

// LoadWithLimits opens, validates, and snapshots a .dd archive. It returns no
// partial package on failure.
func LoadWithLimits(sourcePath string, limits Limits) (*Package, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(strings.TrimSpace(sourcePath))
	if err != nil {
		return nil, fmt.Errorf("resolve .dd package path: %w", err)
	}
	zr, err := zip.OpenReader(abs)
	if err != nil {
		return nil, fmt.Errorf("open .dd ZIP archive: %w", err)
	}
	defer zr.Close()
	if len(zr.File) > limits.MaxEntries {
		return nil, fmt.Errorf("archive has %d entries; limit is %d", len(zr.File), limits.MaxEntries)
	}

	pkg := &Package{
		SourcePath:  abs,
		DisplayName: strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)),
		resources:   make(map[string][]byte),
	}
	seenEntries := make(map[string]struct{}, len(zr.File))
	var total int64
	for _, file := range zr.File {
		isDirectory, err := validateArchiveEntry(file)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seenEntries[file.Name]; duplicate {
			return nil, fmt.Errorf("duplicate archive path %q", file.Name)
		}
		seenEntries[file.Name] = struct{}{}
		if isDirectory {
			continue
		}
		limit := limits.MaxResourceBytes
		if file.Name == manifestName {
			limit = limits.MaxManifestBytes
		}
		if file.UncompressedSize64 > uint64(limit) {
			if file.Name == manifestName {
				return nil, fmt.Errorf("manifest %q declared size exceeds manifest limit", file.Name)
			}
			return nil, fmt.Errorf("resource %q declared size exceeds per-resource limit", file.Name)
		}
		if exceedsCompressionRatio(file.UncompressedSize64, file.CompressedSize64, limits.MaxCompressionRatio) {
			return nil, fmt.Errorf("resource %q declared compression ratio exceeds limit", file.Name)
		}
		data, err := readZipFile(file, limit)
		if err != nil {
			return nil, fmt.Errorf("read archive resource %q: %w", file.Name, err)
		}
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("resource %q exceeds size limit", file.Name)
		}
		if exceedsCompressionRatio(uint64(len(data)), file.CompressedSize64, limits.MaxCompressionRatio) {
			return nil, fmt.Errorf("resource %q decompressed compression ratio exceeds limit", file.Name)
		}
		total += int64(len(data))
		if total > limits.MaxTotalUncompressedBytes {
			return nil, fmt.Errorf("archive decompressed data exceeds total limit")
		}
		pkg.resources[file.Name] = data
		asset := Asset{Path: file.Name, Size: int64(len(data)), AdditionalFile: strings.HasPrefix(file.Name, "Additional Files/")}
		pkg.Assets = append(pkg.Assets, asset)
		if asset.AdditionalFile {
			pkg.AdditionalFiles = append(pkg.AdditionalFiles, asset)
		}
	}
	manifest, ok := pkg.resources[manifestName]
	if !ok {
		return nil, fmt.Errorf("required root %s is missing", manifestName)
	}
	jobs, warnings, err := parseManifest(manifest, manifestName, pkg.resources, limits)
	if err != nil {
		return nil, err
	}
	pkg.Jobs = jobs
	pkg.Warnings = warnings
	return pkg, nil
}

func validateLimits(l Limits) error {
	if l.MaxEntries <= 0 || l.MaxManifestBytes <= 0 || l.MaxResourceBytes <= 0 || l.MaxTotalUncompressedBytes <= 0 || l.MaxInclusionDepth <= 0 || l.MaxExpandedSteps <= 0 || l.MaxCompressionRatio == 0 {
		return errors.New("all .dd package limits must be positive")
	}
	if l.MaxManifestBytes > l.MaxResourceBytes {
		return errors.New("manifest limit cannot exceed resource limit")
	}
	return nil
}

func readZipFile(file *zip.File, limit int64) ([]byte, error) {
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("decompressed data exceeds %d-byte limit", limit)
	}
	return data, nil
}

// exceedsCompressionRatio reports whether uncompressed is greater than the
// exact allowed multiple of compressed without overflowing multiplication.
func exceedsCompressionRatio(uncompressed, compressed, maximum uint64) bool {
	if compressed == 0 {
		return uncompressed != 0
	}
	quotient, remainder := uncompressed/maximum, uncompressed%maximum
	return quotient > compressed || (quotient == compressed && remainder != 0)
}

var drivePath = regexp.MustCompile(`^[A-Za-z]:`)

func validateArchiveEntry(file *zip.File) (bool, error) {
	name := file.Name
	if strings.ContainsRune(name, 0) || strings.ContainsRune(name, '\\') || path.IsAbs(name) || strings.HasPrefix(name, "//") || drivePath.MatchString(name) {
		return false, fmt.Errorf("unsafe archive path %q", name)
	}
	isDirectory := file.FileInfo().IsDir() || strings.HasSuffix(name, "/")
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" || trimmed == "." || path.Clean(trimmed) != trimmed || strings.HasPrefix(trimmed, "../") || trimmed == ".." {
		return false, fmt.Errorf("unsafe archive path %q", name)
	}
	mode := file.Mode()
	if mode&os.ModeSymlink != 0 {
		return false, fmt.Errorf("symbolic-link archive entry %q is not supported", name)
	}
	if !isDirectory && mode&os.ModeType != 0 {
		return false, fmt.Errorf("special archive entry %q is not supported", name)
	}
	return isDirectory, nil
}

func parseManifest(data []byte, manifestPath string, resources map[string][]byte, limits Limits) ([]Job, []Diagnostic, error) {
	root, compatibilityWarnings, err := decodeYAML(data, manifestPath)
	if err != nil {
		return nil, nil, err
	}
	if root.Kind != yaml.SequenceNode {
		return nil, nil, fmt.Errorf("manifest %q must be a YAML sequence of jobs", manifestPath)
	}
	if len(root.Content) == 0 {
		return nil, nil, fmt.Errorf("manifest %q contains no jobs", manifestPath)
	}
	warnings := append([]Diagnostic(nil), compatibilityWarnings...)
	jobs := make([]Job, 0, len(root.Content))
	expandedCount := 0
	for index, jobNode := range root.Content {
		job, ws, err := parseJob(jobNode, manifestPath, resources, limits, index, &expandedCount)
		if err != nil {
			return nil, nil, err
		}
		jobs = append(jobs, job)
		warnings = append(warnings, ws...)
	}
	return jobs, warnings, nil
}

func decodeYAML(data []byte, source string) (*yaml.Node, []Diagnostic, error) {
	root, err := decodeYAMLStrict(data, source)
	if err == nil {
		return root, nil, nil
	}
	normalized, warnings, changed, compatibilityErr := legacyManifestCompatibility(data, source)
	if compatibilityErr != nil {
		return nil, nil, fmt.Errorf("%v; legacy compatibility in %q failed: %w", err, source, compatibilityErr)
	}
	if !changed {
		return nil, nil, err
	}
	root, fallbackErr := decodeYAMLStrict(normalized, source)
	if fallbackErr != nil {
		return nil, nil, fmt.Errorf("%v; legacy compatibility in %q did not produce valid YAML: %w", err, source, fallbackErr)
	}
	return root, warnings, nil
}

func decodeYAMLStrict(data []byte, source string) (*yaml.Node, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("parse YAML %q: %w", source, err)
	}
	var additional yaml.Node
	if err := decoder.Decode(&additional); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("YAML %q must contain one document", source)
		}
		return nil, fmt.Errorf("parse YAML %q: %w", source, err)
	}
	if len(document.Content) != 1 {
		return nil, fmt.Errorf("YAML %q must contain one document", source)
	}
	return document.Content[0], nil
}

func parseJob(node *yaml.Node, manifestPath string, resources map[string][]byte, limits Limits, jobIndex int, expandedCount *int) (Job, []Diagnostic, error) {
	fields, err := mapping(node, "job", manifestPath)
	if err != nil {
		return Job{}, nil, err
	}
	job := Job{ManifestPath: manifestPath, Metadata: nodeMetadata(fields)}
	warnings := []Diagnostic{}
	var steps []Step
	for _, field := range fields {
		switch field.key {
		case "job_name":
			job.Name, err = textValue(field.value, manifestPath, field.key)
		case "job_text":
			job.Text, err = legacyText(field.value, manifestPath, field.key)
		case "guide_files":
			job.GuideFiles, err = resourceList(field.value, manifestPath, field.key, resources)
		case "model_files":
			job.ModelFiles, err = resourceList(field.value, manifestPath, field.key, resources)
		case "min_fw_version":
			job.MinFirmwareVersion, err = textValue(field.value, manifestPath, field.key)
		case "min_ddcut_version":
			job.MinDDCutVersion, err = textValue(field.value, manifestPath, field.key)
		case "disable_wcs_clear_prompt":
			job.DisableWCSClearPrompt, err = boolValue(field.value, manifestPath, field.key)
		case "wcs_value_check":
			job.WCSValueCheck, err = textValue(field.value, manifestPath, field.key)
		case "wcs_check_failed_message":
			job.WCSCheckFailedMessage, err = legacyText(field.value, manifestPath, field.key)
		case "job_steps":
			var added []Step
			added, err = expandStepList(field.value, manifestPath, resources, limits, nil, 0, &warnings, expandedCount)
			steps = append(steps, added...)
		case "manifest_file":
			var include string
			include, err = resourcePath(field.value, manifestPath, field.key, resources)
			if err == nil {
				var added []Step
				added, err = expandSubmanifest(include, resources, limits, nil, 0, &warnings, expandedCount)
				steps = append(steps, added...)
			}
		default:
			warnings = append(warnings, Diagnostic{Path: manifestPath, Field: field.key, Message: "unknown job metadata preserved"})
		}
		if err != nil {
			return Job{}, nil, err
		}
	}
	if job.Name == "" {
		return Job{}, nil, fmt.Errorf("job %d in %q has no job_name", jobIndex+1, manifestPath)
	}
	job.Steps = steps
	for i := range job.Steps {
		job.Steps[i].ID = fmt.Sprintf("job-%d-step-%d", jobIndex+1, i+1)
	}
	return job, warnings, nil
}

func expandSubmanifest(name string, resources map[string][]byte, limits Limits, chain []string, depth int, warnings *[]Diagnostic, expandedCount *int) ([]Step, error) {
	if depth >= limits.MaxInclusionDepth {
		return nil, fmt.Errorf("submanifest %q exceeds inclusion depth limit", name)
	}
	for _, ancestor := range chain {
		if ancestor == name {
			return nil, fmt.Errorf("circular submanifest inclusion: %s -> %s", strings.Join(append(chain, name), " -> "), name)
		}
	}
	data, ok := resources[name]
	if !ok {
		return nil, fmt.Errorf("submanifest %q is missing", name)
	}
	if int64(len(data)) > limits.MaxManifestBytes {
		return nil, fmt.Errorf("submanifest %q exceeds manifest size limit of %d bytes", name, limits.MaxManifestBytes)
	}
	root, compatibilityWarnings, err := decodeYAML(data, name)
	if err != nil {
		return nil, err
	}
	*warnings = append(*warnings, compatibilityWarnings...)
	return expandStepList(root, name, resources, limits, append(chain, name), depth+1, warnings, expandedCount)
}

func expandStepList(node *yaml.Node, manifestPath string, resources map[string][]byte, limits Limits, chain []string, depth int, warnings *[]Diagnostic, expandedCount *int) ([]Step, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("step list in %q must be a YAML sequence", manifestPath)
	}
	steps := []Step{}
	for _, item := range node.Content {
		fields, err := mapping(item, "step", manifestPath)
		if err != nil {
			return nil, err
		}
		if include, ok := onlyManifestInclude(fields); ok {
			name, err := resourcePath(include, manifestPath, "manifest_file", resources)
			if err != nil {
				return nil, err
			}
			expanded, err := expandSubmanifest(name, resources, limits, chain, depth, warnings, expandedCount)
			if err != nil {
				return nil, err
			}
			steps = append(steps, expanded...)
		} else {
			step, err := parseStep(fields, manifestPath, resources, warnings)
			if err != nil {
				return nil, err
			}
			*expandedCount = *expandedCount + 1
			if *expandedCount > limits.MaxExpandedSteps {
				return nil, fmt.Errorf("expanded steps in %q exceed limit of %d", manifestPath, limits.MaxExpandedSteps)
			}
			steps = append(steps, step)
		}
	}
	return steps, nil
}

func parseStep(fields []yamlField, manifestPath string, resources map[string][]byte, warnings *[]Diagnostic) (Step, error) {
	step := Step{ManifestPath: manifestPath, Metadata: nodeMetadata(fields)}
	var err error
	for _, field := range fields {
		switch field.key {
		case "step_name":
			step.Name, err = textValue(field.value, manifestPath, field.key)
		case "step_text":
			step.Text, err = legacyText(field.value, manifestPath, field.key)
		case "step_markdown":
			step.Markdown, err = textValue(field.value, manifestPath, field.key)
		case "step_image":
			step.ImagePath, err = resourcePath(field.value, manifestPath, field.key, resources)
		case "step_gcode":
			step.GCodePath, err = resourcePath(field.value, manifestPath, field.key, resources)
		case "pause":
			step.Pause, err = boolValue(field.value, manifestPath, field.key)
		case "reset":
			step.Reset, err = boolValue(field.value, manifestPath, field.key)
		case "timeout":
			step.Timeout, err = intValue(field.value, manifestPath, field.key)
		case "unskippable":
			step.Unskippable, err = boolValue(field.value, manifestPath, field.key)
		case "if":
			step.Condition, err = textValue(field.value, manifestPath, field.key)
		case "step_goto":
			step.JumpTarget, err = textValue(field.value, manifestPath, field.key)
		case "popup_text":
			step.PopupText, err = legacyText(field.value, manifestPath, field.key)
		case "popup_title":
			step.PopupTitle, err = legacyText(field.value, manifestPath, field.key)
		case "popup_wait_time":
			step.PopupWaitTime, err = intValue(field.value, manifestPath, field.key)
		case "popup_yes_step":
			step.PopupYesTarget, err = textValue(field.value, manifestPath, field.key)
		case "popup_no_step":
			step.PopupNoTarget, err = textValue(field.value, manifestPath, field.key)
		case "manifest_file":
			return Step{}, fmt.Errorf("step in %q mixes manifest_file with step metadata", manifestPath)
		default:
			*warnings = append(*warnings, Diagnostic{Path: manifestPath, Field: field.key, Message: "unknown step metadata preserved"})
		}
		if err != nil {
			return Step{}, err
		}
	}
	return step, nil
}

type yamlField struct {
	key   string
	value *yaml.Node
}

func mapping(node *yaml.Node, kind, source string) ([]yamlField, error) {
	if node.Kind != yaml.MappingNode || len(node.Content)%2 != 0 {
		return nil, fmt.Errorf("%s in %q must be a YAML mapping", kind, source)
	}
	fields := make([]yamlField, 0, len(node.Content)/2)
	seen := map[string]bool{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("%s in %q has a non-string field name", kind, source)
		}
		if seen[key.Value] {
			return nil, fmt.Errorf("%s in %q repeats field %q", kind, source, key.Value)
		}
		seen[key.Value] = true
		fields = append(fields, yamlField{key: key.Value, value: node.Content[i+1]})
	}
	return fields, nil
}

func onlyManifestInclude(fields []yamlField) (*yaml.Node, bool) {
	returnValue := (*yaml.Node)(nil)
	for _, field := range fields {
		if field.key != "manifest_file" {
			return nil, false
		}
		returnValue = field.value
	}
	return returnValue, returnValue != nil
}

func nodeMetadata(fields []yamlField) map[string]any {
	metadata := make(map[string]any, len(fields))
	for _, field := range fields {
		metadata[field.key] = yamlValue(field.value)
	}
	return metadata
}

func yamlValue(node *yaml.Node) any {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Value
	case yaml.SequenceNode:
		values := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			values = append(values, yamlValue(child))
		}
		return values
	case yaml.MappingNode:
		values := map[string]any{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			values[node.Content[i].Value] = yamlValue(node.Content[i+1])
		}
		return values
	default:
		return nil
	}
}

func textValue(node *yaml.Node, source, field string) (string, error) {
	if node.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("field %q in %q must be scalar text", field, source)
	}
	return node.Value, nil
}

func legacyText(node *yaml.Node, source, field string) (string, error) {
	value, err := textValue(node, source, field)
	if err != nil {
		return "", err
	}
	// Older manifests document literal \n as a text line-break convention.
	return strings.ReplaceAll(value, `\n`, "\n"), nil
}

func boolValue(node *yaml.Node, source, field string) (bool, error) {
	value, err := textValue(node, source, field)
	if err != nil {
		return false, err
	}
	parsed, err := strconv.ParseBool(strings.ToLower(value))
	if err != nil {
		return false, fmt.Errorf("field %q in %q must be boolean: %w", field, source, err)
	}
	return parsed, nil
}

func intValue(node *yaml.Node, source, field string) (*int, error) {
	value, err := textValue(node, source, field)
	if err != nil {
		return nil, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return nil, fmt.Errorf("field %q in %q must be a non-negative integer", field, source)
	}
	return &parsed, nil
}

func resourcePath(node *yaml.Node, source, field string, resources map[string][]byte) (string, error) {
	value, err := textValue(node, source, field)
	if err != nil {
		return "", err
	}
	if _, ok := resources[value]; !ok {
		return "", fmt.Errorf("field %q in %q references missing resource %q", field, source, value)
	}
	return value, nil
}

func resourceList(node *yaml.Node, source, field string, resources map[string][]byte) ([]string, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("field %q in %q must be a sequence", field, source)
	}
	values := make([]string, 0, len(node.Content))
	for _, child := range node.Content {
		value, err := resourcePath(child, source, field, resources)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}
