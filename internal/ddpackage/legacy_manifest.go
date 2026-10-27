package ddpackage

import (
	"fmt"
	"math"
	"strings"
)

// legacyManifestCompatibility performs the deliberately small fallback used
// for historic DDCut manifests. It never changes archive bytes: the returned
// buffer is only passed to yaml.v3 after strict parsing has already failed.
func legacyManifestCompatibility(data []byte, source string, manifestLimit int64) ([]byte, []Diagnostic, bool, error) {
	outputLimit, err := legacyCompatibilityOutputLimit(manifestLimit)
	if err != nil {
		return nil, nil, false, err
	}
	lines := strings.Split(string(data), "\n")
	var out strings.Builder
	initialCapacity := len(data)
	if int64(initialCapacity) > outputLimit {
		initialCapacity = int(outputLimit)
	}
	out.Grow(initialCapacity)
	var outputSize int64
	firstOutputLine := true
	appendLine := func(line string, sourceLine int) error {
		contribution := int64(len(line))
		if !firstOutputLine {
			contribution++
		}
		if contribution < 0 || outputSize > outputLimit-contribution {
			return fmt.Errorf("%s:%d: legacy normalization exceeded its %d-byte expansion limit", source, sourceLine, outputLimit)
		}
		if !firstOutputLine {
			out.WriteByte('\n')
		}
		out.WriteString(line)
		firstOutputLine = false
		outputSize += contribution
		return nil
	}
	var markdownLines, colonLines, whitespaceLines []int
	changed := false
	literalIndent := -1
	jobFieldIndent := -1

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if literalIndent >= 0 {
			if strings.TrimSpace(line) == "" || leadingSpaces(line) >= literalIndent {
				if err := appendLine(line, i+1); err != nil {
					return nil, nil, false, err
				}
				continue
			}
			literalIndent = -1
		}
		if strings.ContainsRune(line, '\t') {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				line = ""
				changed = true
				whitespaceLines = append(whitespaceLines, i+1)
			} else if strings.HasPrefix(trimmed, "#") {
				line = trimmed
				changed = true
				whitespaceLines = append(whitespaceLines, i+1)
			} else {
				return nil, nil, false, fmt.Errorf("line %d uses a tab in YAML content; only blank or comment-only tab lines are supported", i+1)
			}
		}

		field, ok := legacyField(line)
		if ok && field.list {
			if normalized, did := normalizeLegacySequenceMarker(line, field.indent); did {
				line = normalized
				field, _ = legacyField(line)
				changed = true
			}
		}
		if ok && !field.list && field.key == "step_markdown" && strings.TrimSpace(field.value) == "|" {
			if !legacyMarkdownNeedsIndent(lines, i+1, field.indent) {
				if err := appendLine(line, i+1); err != nil {
					return nil, nil, false, err
				}
				literalIndent = literalContentIndent(lines, i+1, field.indent)
				continue
			}
			headerLine := i + 1
			if err := appendLine(line, i+1); err != nil {
				return nil, nil, false, err
			}
			headerIndent := field.indent
			for i++; i < len(lines); i++ {
				content := lines[i]
				if strings.ContainsRune(content, '\t') {
					// Markdown is literal content; a tab here is not YAML
					// indentation and is intentionally preserved.
				}
				if boundary, ambiguous := legacyMarkdownBoundary(content, headerIndent, jobFieldIndent); boundary {
					i-- // let normal field processing retain the boundary line.
					break
				} else if ambiguous {
					return nil, nil, false, fmt.Errorf("line %d is an ambiguous unindented Markdown boundary", i+1)
				}
				prefixLength := headerIndent + 2
				if prefixLength < 0 || len(content) > math.MaxInt-prefixLength {
					return nil, nil, false, fmt.Errorf("%s:%d: legacy normalization exceeded its %d-byte expansion limit", source, i+1, outputLimit)
				}
				lineSize := int64(prefixLength)
				if int64(len(content)) > math.MaxInt64-lineSize-1 {
					return nil, nil, false, fmt.Errorf("%s:%d: legacy normalization exceeded its %d-byte expansion limit", source, i+1, outputLimit)
				}
				lineSize += int64(len(content)) + 1
				if lineSize > outputLimit-outputSize {
					return nil, nil, false, fmt.Errorf("%s:%d: legacy normalization exceeded its %d-byte expansion limit", source, i+1, outputLimit)
				}
				if err := appendLine(strings.Repeat(" ", prefixLength)+content, i+1); err != nil {
					return nil, nil, false, err
				}
			}
			changed = true
			markdownLines = append(markdownLines, headerLine)
			continue
		}

		if ok && legacyFreeTextField[field.key] {
			if text, did := legacyFreeText(field); did {
				rewrittenLength, lengthErr := text.rewrittenLength()
				proposed := int64(rewrittenLength)
				if !firstOutputLine {
					proposed++
				}
				if lengthErr != nil || proposed < 0 || proposed > outputLimit-outputSize {
					return nil, nil, false, fmt.Errorf("%s:%d: legacy normalization exceeded its %d-byte expansion limit", source, i+1, outputLimit)
				}
				line = text.rewrite()
				changed = true
				colonLines = append(colonLines, i+1)
			}
		}
		if ok && !field.list && field.key == "job_steps" {
			jobFieldIndent = field.indent
		}
		if ok && !field.list && isLiteralHeader(field.value) {
			literalIndent = literalContentIndent(lines, i+1, field.indent)
		}
		if err := appendLine(line, i+1); err != nil {
			return nil, nil, false, err
		}
	}
	if !changed {
		return nil, nil, false, nil
	}
	warnings := []Diagnostic{{Path: source, Message: "legacy manifest formatting was accepted"}}
	if len(markdownLines) != 0 {
		warnings = append(warnings, Diagnostic{Path: source, Field: fmt.Sprintf("line %d", markdownLines[0]), Message: "accepted legacy unindented Markdown block"})
	}
	if len(colonLines) != 0 {
		warnings = append(warnings, Diagnostic{Path: source, Field: fmt.Sprintf("line %d", colonLines[0]), Message: "accepted unquoted free-text value containing a colon"})
	}
	if len(whitespaceLines) != 0 {
		warnings = append(warnings, Diagnostic{Path: source, Field: fmt.Sprintf("line %d", whitespaceLines[0]), Message: "accepted tabs on blank or comment-only lines"})
	}
	return []byte(out.String()), warnings, true, nil
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

func literalContentIndent(lines []string, start, headerIndent int) int {
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return leadingSpaces(lines[i])
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
func legacyMarkdownNeedsIndent(lines []string, start, headerIndent int) bool {
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		indent := 0
		for indent < len(lines[i]) && lines[i][indent] == ' ' {
			indent++
		}
		return indent < headerIndent+2
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
