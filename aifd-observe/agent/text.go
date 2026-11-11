package agent

import (
	"strconv"
	"strings"
)

// parseTextFields reads the key=value format emitted by slog.TextHandler. Quoted
// strings use Go escapes; splitting on spaces would corrupt messages and fields.
// Unrecognized or truncated lines remain available as raw TailEntry.Text.
func parseTextFields(text string) map[string]any {
	if !strings.HasPrefix(text, "time=") && !strings.HasPrefix(text, "level=") {
		return nil
	}
	fields := make(map[string]any)
	for text = strings.TrimSpace(text); text != ""; text = strings.TrimLeft(text, " \t") {
		key, rest, ok := textToken(text, true)
		if !ok || len(rest) == 0 || rest[0] != '=' {
			return nil
		}
		value, rest, ok := textToken(rest[1:], false)
		if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
			return nil
		}
		fields[key], text = value, rest
	}
	return fields
}

func textToken(text string, key bool) (string, string, bool) {
	if text == "" {
		return "", "", false
	}
	if text[0] == '"' {
		for i := 1; i < len(text); i++ {
			if text[i] == '\\' {
				i++
			} else if text[i] == '"' {
				value, err := strconv.Unquote(text[:i+1])
				return value, text[i+1:], err == nil
			}
		}
		return "", "", false
	}
	end := strings.IndexAny(text, " \t")
	if key {
		end = strings.IndexAny(text, "= \t")
	}
	if end < 0 {
		end = len(text)
	}
	return text[:end], text[end:], end > 0
}
