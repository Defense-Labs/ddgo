package ddpackage

import (
	"fmt"
	"strconv"
	"strings"
)

// legacyManifestCompatibility performs the deliberately small fallback used
// for historic DDCut manifests. It never changes archive bytes: the returned
// buffer is only passed to yaml.v3 after strict parsing has already failed.
func legacyManifestCompatibility(data []byte, source string) ([]byte, []Diagnostic, bool, error) {
	// At most two spaces are inserted for each input line. This is an explicit
	// bound on fallback memory growth, in addition to the validated manifest
	// byte limit enforced by the archive loader.
	if len(data) > (int(^uint(0)>>1)-2)/3 {
		return nil, nil, false, fmt.Errorf("legacy compatibility buffer would overflow")
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	var markdownLines, colonLines, whitespaceLines []int
	changed := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
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
				out = append(out, line)
				continue
			}
			headerLine := i + 1
			out = append(out, line)
			headerIndent := field.indent
			for i++; i < len(lines); i++ {
				content := lines[i]
				if strings.ContainsRune(content, '\t') {
					// Markdown is literal content; a tab here is not YAML
					// indentation and is intentionally preserved.
				}
				if boundary, ambiguous := legacyMarkdownBoundary(content, headerIndent); boundary {
					i-- // let normal field processing retain the boundary line.
					break
				} else if ambiguous {
					return nil, nil, false, fmt.Errorf("line %d is an ambiguous unindented Markdown boundary", i+1)
				}
				out = append(out, strings.Repeat(" ", headerIndent+2)+content)
			}
			changed = true
			markdownLines = append(markdownLines, headerLine)
			continue
		}

		if ok && legacyFreeTextField[field.key] {
			if rewritten, did := quoteLegacyFreeText(field); did {
				line = rewritten
				changed = true
				colonLines = append(colonLines, i+1)
			}
		}
		out = append(out, line)
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
	return []byte(strings.Join(out, "\n")), warnings, true, nil
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
	colon := strings.IndexByte(line[start:], ':')
	if colon < 1 {
		return legacyManifestField{}, false
	}
	colon += start
	key := strings.TrimSpace(line[start:colon])
	if key == "" || strings.ContainsAny(key, " \t") {
		return legacyManifestField{}, false
	}
	return legacyManifestField{indent: indent, list: list, key: key, head: line[:colon], value: line[colon+1:]}, true
}

func legacyMarkdownBoundary(line string, headerIndent int) (boundary, ambiguous bool) {
	field, ok := legacyField(line)
	if !ok || !legacyStepField[field.key] {
		return false, false
	}
	if !field.list && field.indent == headerIndent {
		return true, false
	}
	if field.indent < headerIndent {
		return false, true
	}
	return false, false
}

func quoteLegacyFreeText(field legacyManifestField) (string, bool) {
	value := strings.TrimLeft(field.value, " ")
	if value == "" || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") ||
		strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") || strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") {
		return "", false
	}
	comment := ""
	if index := strings.Index(value, " #"); index >= 0 {
		comment = value[index:]
		value = value[:index]
	}
	if !strings.Contains(value, ": ") {
		return "", false
	}
	return field.head + ": " + strconv.Quote(value) + comment, true
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

var legacyStepField = map[string]bool{
	"step_name": true, "step_text": true, "step_markdown": true, "step_image": true, "step_gcode": true,
	"pause": true, "reset": true, "timeout": true, "unskippable": true, "if": true, "step_goto": true,
	"popup_text": true, "popup_title": true, "popup_wait_time": true, "popup_yes_step": true,
	"popup_no_step": true, "manifest_file": true,
}
