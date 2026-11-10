package camp

import (
	"path"
	"strings"
)

// MapNode combines a visible branch profile with optional document identity.
// Derived text comes only from this authorized revision, never hidden children.
type MapNode struct {
	Path              string         `json:"path"`
	Count             int            `json:"count"`
	Kinds             map[string]int `json:"kinds"`
	EntryType         string         `json:"entry_type"`
	Actions           []string       `json:"actions"`
	ID                string         `json:"id,omitempty"`
	Revision          string         `json:"revision,omitempty"`
	Kind              string         `json:"kind,omitempty"`
	Title             string         `json:"title,omitempty"`
	Description       string         `json:"description,omitempty"`
	DescriptionSource string         `json:"description_source,omitempty"`
	Status            string         `json:"status,omitempty"`
	TextTruncated     bool           `json:"text_truncated,omitempty"`
}

func navigationCard(rec Record, rev Revision) MapNode {
	card := MapNode{Path: rec.Path, ID: rec.ID, Revision: rev.ID, Kind: rec.Kind}
	title, _ := rec.Metadata["title"].(string)
	if strings.TrimSpace(title) == "" {
		title = firstNavigationHeading(rev.Content)
	}
	if title == "" {
		title = path.Base(rec.Path)
	}
	card.Title, card.TextTruncated = boundNavigationText(title, 120)
	description, _ := rec.Metadata["description"].(string)
	if strings.TrimSpace(description) != "" {
		var cut bool
		card.Description, cut = boundNavigationText(description, 240)
		card.TextTruncated = card.TextTruncated || cut
		card.DescriptionSource = "metadata"
	}
	status, _ := rec.Metadata["status"].(string)
	var cut bool
	card.Status, cut = boundNavigationText(status, 64)
	card.TextTruncated = card.TextTruncated || cut
	return card
}

func boundNavigationText(s string, limit int) (string, bool) {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) > limit {
		return string(runes[:limit]), true
	}
	return string(runes), false
}

// Recognize ATX headings outside ordinary Markdown fences without parsing bodies
// into a second representation. Source reading and full outlines remain separate.
func firstNavigationHeading(content string) string {
	var fence byte
	width := 0
	for _, line := range strings.Split(content, "\n") {
		text := strings.TrimLeft(line, " ")
		if len(line)-len(text) > 3 {
			continue
		}
		if len(text) >= 3 && (text[0] == '`' || text[0] == '~') {
			marker := text[0]
			length := 0
			for length < len(text) && text[length] == marker {
				length++
			}
			if length >= 3 {
				if fence == 0 {
					fence = marker
					width = length
					continue
				}
				if fence == marker && length >= width && strings.TrimSpace(text[length:]) == "" {
					fence = 0
					continue
				}
			}
		}
		if fence != 0 {
			continue
		}
		hashes := 0
		for hashes < len(text) && text[hashes] == '#' {
			hashes++
		}
		if hashes < 1 || hashes > 6 || hashes == len(text) || (text[hashes] != ' ' && text[hashes] != '\t') {
			continue
		}
		heading := strings.TrimSpace(text[hashes:])
		if heading != "" {
			return heading
		}
	}
	return ""
}
