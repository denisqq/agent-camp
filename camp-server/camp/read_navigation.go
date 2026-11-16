package camp

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// A standalone read must fit within RunOutput's 16 KiB JSON limit, with room
// for its envelope. Multiple reads and stdout still share the run limit.
const readResultBudget = 14 * 1024

func readDocument(rec Record, rev Revision, snapshot string, start, limit, offset int) (map[string]any, error) {
	lines := strings.Split(rev.Content, "\n")
	if start < 1 || limit < 1 || limit > 1000 || offset < 0 {
		return nil, ErrInvalid
	}
	if start > len(lines) {
		if offset != 0 {
			return nil, ErrInvalid
		}
		result := map[string]any{"record": rec, "revision": rev.ID, "content": "", "truncated": false, "snapshot": snapshot, "start_line": start, "line_offset": 0, "end_line": start, "next_line": 0, "next_offset": 0, "next_separator": "", "truncation_reason": ""}
		raw, err := json.Marshal(RunOutput{Value: result})
		if err != nil || runnerJSONSize(raw) > readResultBudget {
			return nil, fmt.Errorf("%w: read metadata exceeds result budget", ErrInvalid)
		}
		return result, nil
	}
	first := lines[start-1]
	if offset > len(first) || (offset < len(first) && !utf8.RuneStart(first[offset])) {
		return nil, ErrInvalid
	}
	end := min(len(lines), start-1+limit)
	selected := strings.Join(lines[start-1:end], "\n")[offset:]
	boundaries := []int{0}
	for i := range selected {
		if i > 0 {
			boundaries = append(boundaries, i)
		}
	}
	if len(selected) > 0 {
		boundaries = append(boundaries, len(selected))
	}
	build := func(n int) map[string]any {
		content := selected[:n]
		newlineCount := strings.Count(content, "\n")
		lastLine := start + newlineCount
		truncated := n < len(selected) || end < len(lines)
		nextLine, nextOffset := 0, 0
		separator, reason := "", ""
		if n < len(selected) {
			nextLine = lastLine
			nextOffset = len(content) - strings.LastIndex(content, "\n") - 1
			if newlineCount == 0 {
				nextOffset += offset
			}
			reason = "byte_limit"
		} else if end < len(lines) {
			nextLine = end + 1
			separator = "\n"
			reason = "line_limit"
		}
		return map[string]any{"record": rec, "revision": rev.ID, "content": content, "truncated": truncated, "snapshot": snapshot, "start_line": start, "line_offset": offset, "end_line": lastLine, "next_line": nextLine, "next_offset": nextOffset, "next_separator": separator, "truncation_reason": reason}
	}
	fits := func(n int) bool {
		raw, err := json.Marshal(RunOutput{Value: build(n)})
		return err == nil && runnerJSONSize(raw) <= readResultBudget
	}
	if !fits(0) {
		return nil, fmt.Errorf("%w: read metadata exceeds result budget", ErrInvalid)
	}
	lo, hi := 0, len(boundaries)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fits(boundaries[mid]) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if boundaries[lo] == 0 && len(selected) > 0 {
		return nil, fmt.Errorf("%w: read result cannot fit content", ErrInvalid)
	}
	return build(boundaries[lo]), nil
}

func documentHeadings(content string) ([]map[string]any, bool) {
	headings := []map[string]any{}
	var fence byte
	width := 0
	for i, line := range strings.Split(content, "\n") {
		text := strings.TrimLeft(line, " ")
		if len(line)-len(text) > 3 {
			continue
		}
		if len(text) >= 3 && (text[0] == '`' || text[0] == '~') {
			marker := text[0]
			count := 0
			for count < len(text) && text[count] == marker {
				count++
			}
			if count >= 3 {
				if fence == 0 {
					fence = marker
					width = count
					continue
				}
				if marker == fence && count >= width && strings.TrimSpace(text[count:]) == "" {
					fence = 0
					continue
				}
			}
		}
		if fence != 0 || !strings.HasPrefix(text, "#") {
			continue
		}
		n := 0
		for n < len(text) && text[n] == '#' {
			n++
		}
		if n > 6 || (n < len(text) && text[n] != ' ' && text[n] != '\t') {
			continue
		}
		if len(headings) == 200 {
			return headings, true
		}
		headings = append(headings, map[string]any{"line": i + 1, "heading": line})
	}
	return headings, false
}

// Python's result envelope adds a type field and inserts a space after JSON
// separators. Count only separators outside strings, including escaped quotes.
func runnerJSONSize(raw []byte) int {
	size := len(raw) + 32 // type field and envelope separators
	inString, escaped := false, false
	inNumber, floating := false, false
	finishNumber := func() {
		if inNumber && floating {
			size += 2
		}
		inNumber = false
		floating = false
	}
	for _, c := range raw {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if inNumber {
			if (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E' {
				if c == '.' || c == 'e' || c == 'E' {
					floating = true
				}
				continue
			}
			finishNumber()
		}
		if c == '"' {
			inString = true
		} else if c == ',' || c == ':' {
			size++
		} else if c == '-' || (c >= '0' && c <= '9') {
			inNumber = true
		}
	}
	finishNumber()
	return size
}
