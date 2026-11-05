package ddpackage

import (
	"fmt"
	"math"
	"strings"
)

// legacyNormalizationResult is the one internal result of compatibility
// recovery. YAML remains subject to yaml.v3 parsing after normalization.
type legacyNormalizationResult struct {
	YAML        []byte
	Changed     bool
	Diagnostics []Diagnostic
}

type legacyLineKind uint8

const (
	legacyLinePlain legacyLineKind = iota
	legacyLineBlank
	legacyLineComment
	legacyLineMapping
	legacyLineSequenceMapping
)

// legacyManifestLine retains source text and a separately normalized working
// value. Candidate fields are not structural facts until the context confirms
// the line is outside a literal scalar body.
type legacyManifestLine struct {
	Number          int
	Original        string
	Content         string
	LeadingSpaces   int
	Blank           bool
	Comment         bool
	Kind            legacyLineKind
	Field           legacyManifestField
	HasCandidateMap bool
	BlockHeader     bool
}

func scanLegacyManifest(data []byte) []legacyManifestLine {
	source := string(data)
	// Keep slices into one source string rather than a second full []string
	// split buffer. The record count and all retained text are therefore linear
	// in the manifest bytes already admitted by the loader.
	lines := make([]legacyManifestLine, 0)
	lineNumber := 1
	for start := 0; ; {
		end := strings.IndexByte(source[start:], '\n')
		if end < 0 {
			lines = append(lines, scanLegacyManifestLine(lineNumber, source[start:], source[start:]))
			return lines
		}
		end += start
		lines = append(lines, scanLegacyManifestLine(lineNumber, source[start:end], source[start:end]))
		lineNumber++
		start = end + 1
	}
}

func scanLegacyManifestLine(number int, original, content string) legacyManifestLine {
	trimmed := strings.TrimSpace(content)
	line := legacyManifestLine{
		Number:        number,
		Original:      original,
		Content:       content,
		LeadingSpaces: leadingSpaces(content),
	}
	switch {
	case trimmed == "":
		line.Blank, line.Kind = true, legacyLineBlank
	case strings.HasPrefix(trimmed, "#"):
		line.Comment, line.Kind = true, legacyLineComment
	case func() bool {
		field, ok := legacyField(content)
		line.Field, line.HasCandidateMap = field, ok
		line.BlockHeader = ok && !field.list && isLiteralHeader(field.value)
		return ok
	}():
		if line.Field.list {
			line.Kind = legacyLineSequenceMapping
		} else {
			line.Kind = legacyLineMapping
		}
	default:
		line.Kind = legacyLinePlain
	}
	return line
}

func (line legacyManifestLine) withContent(content string) legacyManifestLine {
	return scanLegacyManifestLine(line.Number, line.Original, content)
}

// legacyNormalizationContext records only the local structure needed by the
// current rules. It intentionally retains parent indentation instead of a
// global width so a future indentation rule can reason about actual parents.
type legacyNormalizationContext struct {
	rootJobIndent  int
	jobIndent      int
	jobFieldIndent int
	stepListIndent int
	stepIndent     int
	literalIndent  int
	markdown       *legacyMarkdownRecovery
}

type legacyMarkdownRecovery struct {
	headerIndent   int
	jobFieldIndent int
}

func newLegacyNormalizationContext() legacyNormalizationContext {
	return legacyNormalizationContext{
		rootJobIndent:  -1,
		jobIndent:      -1,
		jobFieldIndent: -1,
		stepListIndent: -1,
		stepIndent:     -1,
		literalIndent:  -1,
	}
}

type legacyNormalizationActions struct {
	markdownLines   []int
	colonLines      []int
	whitespaceLines []int
}

// legacyOutputEmitter is the sole normalized-buffer constructor. It enforces
// the two-times-manifest limit before every transformed line is allocated.
type legacyOutputEmitter struct {
	source      string
	limit       int64
	buffer      strings.Builder
	size        int64
	firstOutput bool
}

func newLegacyOutputEmitter(source string, inputSize int, limit int64) *legacyOutputEmitter {
	capacity := inputSize
	if int64(capacity) > limit {
		capacity = int(limit)
	}
	emitter := &legacyOutputEmitter{source: source, limit: limit, firstOutput: true}
	emitter.buffer.Grow(capacity)
	return emitter
}

func (e *legacyOutputEmitter) check(length int, sourceLine int) error {
	contribution := int64(length)
	if !e.firstOutput {
		contribution++
	}
	if contribution < 0 || e.size > e.limit-contribution {
		return fmt.Errorf("%s:%d: legacy normalization exceeded its %d-byte expansion limit", e.source, sourceLine, e.limit)
	}
	return nil
}

func (e *legacyOutputEmitter) emit(line legacyManifestLine, content string) error {
	if err := e.check(len(content), line.Number); err != nil {
		return err
	}
	if !e.firstOutput {
		e.buffer.WriteByte('\n')
		e.size++
	}
	e.buffer.WriteString(content)
	e.size += int64(len(content))
	e.firstOutput = false
	return nil
}

func (e *legacyOutputEmitter) emitIndented(line legacyManifestLine, prefixLength int) error {
	if prefixLength < 0 || len(line.Content) > math.MaxInt-prefixLength {
		return fmt.Errorf("%s:%d: legacy normalization exceeded its %d-byte expansion limit", e.source, line.Number, e.limit)
	}
	lineLength := prefixLength + len(line.Content)
	if err := e.check(lineLength, line.Number); err != nil {
		return err
	}
	return e.emit(line, strings.Repeat(" ", prefixLength)+line.Content)
}

func (e *legacyOutputEmitter) bytes() []byte { return []byte(e.buffer.String()) }

type legacyManifestNormalizer struct {
	lines   []legacyManifestLine
	context legacyNormalizationContext
	emitter *legacyOutputEmitter
	actions legacyNormalizationActions
	changed bool
}

// normalizeLegacyManifest is the single compatibility normalization entry
// point. Strict YAML parsing calls it only after yaml.v3 has rejected source.
func normalizeLegacyManifest(data []byte, source string, manifestLimit int64) (legacyNormalizationResult, error) {
	outputLimit, err := legacyCompatibilityOutputLimit(manifestLimit)
	if err != nil {
		return legacyNormalizationResult{}, err
	}
	normalizer := legacyManifestNormalizer{
		lines:   scanLegacyManifest(data),
		context: newLegacyNormalizationContext(),
		emitter: newLegacyOutputEmitter(source, len(data), outputLimit),
	}
	if err := normalizer.normalize(); err != nil {
		return legacyNormalizationResult{}, err
	}
	return legacyNormalizationResult{
		YAML:        normalizer.emitter.bytes(),
		Changed:     normalizer.changed,
		Diagnostics: normalizer.diagnostics(source),
	}, nil
}

func (n *legacyManifestNormalizer) normalize() error {
	for index := 0; index < len(n.lines); {
		line := n.lines[index]
		if n.context.markdown != nil {
			boundary, ambiguous := legacyMarkdownBoundary(line.Content, n.context.markdown.headerIndent, n.context.markdown.jobFieldIndent)
			if ambiguous {
				return fmt.Errorf("line %d is an ambiguous unindented Markdown boundary", line.Number)
			}
			if boundary {
				n.context.markdown = nil
				continue // Reconsider this line as document structure.
			}
			if err := n.emitter.emitIndented(line, n.context.markdown.headerIndent+2); err != nil {
				return err
			}
			index++
			continue
		}
		if n.context.literalIndent >= 0 {
			if line.Blank || line.LeadingSpaces >= n.context.literalIndent {
				if err := n.emitter.emit(line, line.Content); err != nil {
					return err
				}
				index++
				continue
			}
			n.context.literalIndent = -1
		}

		var err error
		if line, err = n.normalizeWhitespace(line); err != nil {
			return err
		}
		line = n.normalizeSequenceMarker(line)
		if line.HasCandidateMap && !line.Field.list && line.Field.key == "step_markdown" && strings.TrimSpace(line.Field.value) == "|" {
			if legacyMarkdownNeedsIndent(n.lines, index+1, line.Field.indent) {
				if err := n.emitter.emit(line, line.Content); err != nil {
					return err
				}
				n.context.markdown = &legacyMarkdownRecovery{headerIndent: line.Field.indent, jobFieldIndent: n.context.jobFieldIndent}
				n.changed = true
				n.actions.markdownLines = append(n.actions.markdownLines, line.Number)
				index++
				continue
			}
			if err := n.emitter.emit(line, line.Content); err != nil {
				return err
			}
			n.context.literalIndent = literalContentIndent(n.lines, index+1, line.Field.indent)
			index++
			continue
		}
		if line, err = n.normalizeFreeText(line); err != nil {
			return err
		}
		n.context.observe(line)
		if line.BlockHeader {
			n.context.literalIndent = literalContentIndent(n.lines, index+1, line.Field.indent)
		}
		if err := n.emitter.emit(line, line.Content); err != nil {
			return err
		}
		index++
	}
	return nil
}

// observe centralizes the small amount of parent/child structure this phase
// needs. It records observed indentation only; it deliberately does not try
// to repair inconsistent indentation. A later rule can use these parent
// relationships without adding another preprocessing pass.
func (context *legacyNormalizationContext) observe(line legacyManifestLine) {
	if !line.HasCandidateMap {
		return
	}
	field := line.Field
	if field.list && field.key == "job_name" {
		context.rootJobIndent = field.indent
		context.jobIndent = field.indent
		context.stepListIndent = -1
		context.stepIndent = -1
		return
	}
	if !field.list && field.key == "job_steps" {
		context.jobFieldIndent = field.indent
		context.stepListIndent = -1
		context.stepIndent = -1
		return
	}
	if field.list && context.jobFieldIndent >= 0 && field.indent > context.jobFieldIndent {
		context.stepListIndent = field.indent
		context.stepIndent = field.indent
		return
	}
	// Included manifests are root step sequences, so they have no enclosing
	// job_steps field from which to infer their parent.
	if field.list && field.key == "step_name" && context.jobFieldIndent < 0 {
		context.stepListIndent = field.indent
		context.stepIndent = field.indent
	}
}

func (n *legacyManifestNormalizer) normalizeWhitespace(line legacyManifestLine) (legacyManifestLine, error) {
	if !strings.ContainsRune(line.Content, '\t') {
		return line, nil
	}
	trimmed := strings.TrimSpace(line.Content)
	if trimmed == "" {
		n.changed = true
		n.actions.whitespaceLines = append(n.actions.whitespaceLines, line.Number)
		return line.withContent(""), nil
	}
	if strings.HasPrefix(trimmed, "#") {
		n.changed = true
		n.actions.whitespaceLines = append(n.actions.whitespaceLines, line.Number)
		return line.withContent(trimmed), nil
	}
	return legacyManifestLine{}, fmt.Errorf("line %d uses a tab in YAML content; only blank or comment-only tab lines are supported", line.Number)
}

func (n *legacyManifestNormalizer) normalizeSequenceMarker(line legacyManifestLine) legacyManifestLine {
	if !line.HasCandidateMap || !line.Field.list {
		return line
	}
	if normalized, did := normalizeLegacySequenceMarker(line.Content, line.Field.indent); did {
		n.changed = true
		return line.withContent(normalized)
	}
	return line
}

func (n *legacyManifestNormalizer) normalizeFreeText(line legacyManifestLine) (legacyManifestLine, error) {
	if !line.HasCandidateMap || !legacyFreeTextField[line.Field.key] {
		return line, nil
	}
	text, did := legacyFreeText(line.Field)
	if !did {
		return line, nil
	}
	length, err := text.rewrittenLength()
	if err != nil {
		return legacyManifestLine{}, err
	}
	if err := n.emitter.check(length, line.Number); err != nil {
		return legacyManifestLine{}, err
	}
	n.changed = true
	n.actions.colonLines = append(n.actions.colonLines, line.Number)
	return line.withContent(text.rewrite()), nil
}

func (n *legacyManifestNormalizer) diagnostics(source string) []Diagnostic {
	if !n.changed {
		return nil
	}
	warnings := []Diagnostic{{Path: source, Message: "legacy manifest formatting was accepted"}}
	if len(n.actions.markdownLines) != 0 {
		warnings = append(warnings, Diagnostic{Path: source, Field: fmt.Sprintf("line %d", n.actions.markdownLines[0]), Message: "accepted legacy unindented Markdown block"})
	}
	if len(n.actions.colonLines) != 0 {
		warnings = append(warnings, Diagnostic{Path: source, Field: fmt.Sprintf("line %d", n.actions.colonLines[0]), Message: "accepted unquoted free-text value containing a colon"})
	}
	if len(n.actions.whitespaceLines) != 0 {
		warnings = append(warnings, Diagnostic{Path: source, Field: fmt.Sprintf("line %d", n.actions.whitespaceLines[0]), Message: "accepted tabs on blank or comment-only lines"})
	}
	return warnings
}

// legacyManifestCompatibility remains a narrow compatibility wrapper for
// callers/tests that predate the unified result type.
func legacyManifestCompatibility(data []byte, source string, manifestLimit int64) ([]byte, []Diagnostic, bool, error) {
	result, err := normalizeLegacyManifest(data, source, manifestLimit)
	if err != nil {
		return nil, nil, false, err
	}
	if !result.Changed {
		return nil, nil, false, nil
	}
	return result.YAML, result.Diagnostics, true, nil
}

// legacyCompatibilityOutputLimit bounds the normalized in-memory buffer to
// twice the caller-configured manifest limit. The source bytes have already
// been limited independently; this permits ordinary indentation/quoting while
// preventing hostile indentation from multiplying memory use.
func legacyCompatibilityOutputLimit(manifestLimit int64) (int64, error) {
	if manifestLimit <= 0 || manifestLimit > math.MaxInt64/2 {
		return 0, fmt.Errorf("legacy compatibility output limit is invalid")
	}
	return manifestLimit * 2, nil
}

func leadingSpaces(line string) int {
	count := 0
	for count < len(line) && line[count] == ' ' {
		count++
	}
	return count
}

func literalContentIndent(lines []legacyManifestLine, start, headerIndent int) int {
	for i := start; i < len(lines); i++ {
		if !lines[i].Blank {
			return lines[i].LeadingSpaces
		}
	}
	return headerIndent + 1
}

func isLiteralHeader(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">")
}

// legacyMarkdownNeedsIndent distinguishes the observed zero-column legacy
// body from a normal YAML literal block. A fallback triggered by some other
// legacy convention must leave correctly indented Markdown byte-for-byte in
// its YAML representation.
func legacyMarkdownNeedsIndent(lines []legacyManifestLine, start, headerIndent int) bool {
	for i := start; i < len(lines); i++ {
		if lines[i].Blank {
			continue
		}
		return lines[i].LeadingSpaces < headerIndent+2
	}
	return false
}

// normalizeLegacySequenceMarker accepts the historic "-  key: value" form
// only for a recognized mapping item. It leaves scalar sequence content and
// ordinary strict-YAML input untouched.
func normalizeLegacySequenceMarker(line string, indent int) (string, bool) {
	start := indent + 1 // byte after '-'
	spaces := start
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	if spaces-start <= 1 {
		return "", false
	}
	return line[:start] + " " + line[spaces:], true
}

type legacyManifestField struct {
	indent int
	list   bool
	key    string
	head   string // through, but excluding, the field colon
	value  string // after the field colon
}

// legacyField recognizes only ordinary one-line mapping fields. It is not a
// YAML parser and is used solely to identify the compatibility allowlist.
func legacyField(line string) (legacyManifestField, bool) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	if indent == len(line) || line[indent] == '\t' || line[indent] == '#' {
		return legacyManifestField{}, false
	}
	start := indent
	list := false
	if line[start] == '-' {
		if start+1 >= len(line) || line[start+1] != ' ' {
			return legacyManifestField{}, false
		}
		list = true
		start += 1
		for start < len(line) && line[start] == ' ' {
			start++
		}
	}
	colon := legacyMappingColon(line, start)
	if colon < start+1 {
		return legacyManifestField{}, false
	}
	key := strings.TrimSpace(line[start:colon])
	if key == "" || (!legacyQuotedKey(key) && strings.ContainsAny(key, " \t")) {
		return legacyManifestField{}, false
	}
	return legacyManifestField{indent: indent, list: list, key: key, head: line[:colon], value: line[colon+1:]}, true
}

// legacyMappingColon recognizes the small scalar-key subset needed to find a
// structural boundary. Quoted unknown keys are accepted for that purpose, but
// this remains deliberately narrower than a YAML parser.
func legacyMappingColon(line string, start int) int {
	if start >= len(line) {
		return -1
	}
	if quote := line[start]; quote == '\'' || quote == '"' {
		for i := start + 1; i < len(line); i++ {
			if quote == '"' && line[i] == '\\' {
				i++
				continue
			}
			if quote == '\'' && line[i] == '\'' && i+1 < len(line) && line[i+1] == '\'' {
				i++
				continue
			}
			if line[i] != quote {
				continue
			}
			for i++; i < len(line) && line[i] == ' '; i++ {
			}
			if i < len(line) && line[i] == ':' {
				return i
			}
			return -1
		}
		return -1
	}
	for i := start; i < len(line); i++ {
		if line[i] == ':' {
			return i
		}
	}
	return -1
}

func legacyQuotedKey(key string) bool {
	if len(key) < 2 {
		return false
	}
	return (key[0] == '\'' && key[len(key)-1] == '\'') || (key[0] == '"' && key[len(key)-1] == '"')
}

func legacyMarkdownBoundary(line string, headerIndent, jobFieldIndent int) (boundary, ambiguous bool) {
	field, ok := legacyField(line)
	if !ok {
		return false, false
	}
	// Any mapping key aligned with the Markdown field belongs to the same
	// step, including unknown metadata retained by DDGo.
	if !field.list && field.indent == headerIndent {
		return true, false
	}
	// A new item at the enclosing step-list indentation is a later step.
	if field.list && field.indent == headerIndent-2 {
		return true, false
	}
	// Root job entries have a stable structural key, even when the current
	// step's exact indentation differs from the conventional two spaces.
	if field.list && field.key == "job_name" && field.indent < headerIndent {
		return true, false
	}
	// A job-level mapping after job_steps can be unknown metadata. Its
	// indentation was observed from the actual job_steps field, not guessed.
	if !field.list && jobFieldIndent >= 0 && field.indent == jobFieldIndent {
		return true, false
	}
	if field.indent < headerIndent {
		return false, true
	}
	return false, false
}

type legacyFreeTextValue struct {
	head    string
	value   string
	comment string
}

func legacyFreeText(field legacyManifestField) (legacyFreeTextValue, bool) {
	value := strings.TrimLeft(field.value, " ")
	if value == "" || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") ||
		strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") || strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") {
		return legacyFreeTextValue{}, false
	}
	comment := ""
	if index := strings.Index(value, " #"); index >= 0 {
		comment = value[index:]
		value = value[:index]
	}
	if !strings.Contains(value, ": ") {
		return legacyFreeTextValue{}, false
	}
	return legacyFreeTextValue{head: field.head, value: value, comment: comment}, true
}

// rewrittenLength is exact for the YAML single-quoted representation used by
// rewrite. It is calculated before allocating the expanded line.
func (value legacyFreeTextValue) rewrittenLength() (int, error) {
	length := len(value.head)
	for _, part := range []string{": ", "'", value.value, "'", value.comment} {
		if len(part) > math.MaxInt-length {
			return 0, fmt.Errorf("legacy free-text value is too long")
		}
		length += len(part)
	}
	for i := 0; i < len(value.value); i++ {
		if value.value[i] == '\'' {
			if length == math.MaxInt {
				return 0, fmt.Errorf("legacy free-text value is too long")
			}
			length++
		}
	}
	return length, nil
}

func (value legacyFreeTextValue) rewrite() string {
	return value.head + ": '" + strings.ReplaceAll(value.value, "'", "''") + "'" + value.comment
}

var legacyFreeTextField = map[string]bool{
	"job_name":                 true,
	"job_text":                 true,
	"step_name":                true,
	"step_text":                true,
	"popup_text":               true,
	"popup_title":              true,
	"wcs_check_failed_message": true,
}
