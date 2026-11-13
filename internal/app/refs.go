package app

import (
	"regexp"
	"strings"
)

// The Desk's text names artifacts as `artifact:<id>` (pi-pocket-team's
// desk-web/ask-text.js): a reference at a word boundary, or the whole of an
// inline code span. Pocket finds them to link them and to give each a
// button that opens the artifact.

// artifactIDMax is the longest artifact id that is a reference.
const artifactIDMax = 80

var (
	artifactID = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	// trailing is the punctuation of the sentence around a bare reference.
	trailing = regexp.MustCompile(`[.,;:!?)\]"'>]+$`)
)

// artifactRef is a reference in a text: the id, and the bytes it takes.
type artifactRef struct {
	id         string
	start, end int
	// linked is a reference that is already a link's destination.
	linked bool
}

// refID returns the id of a whole `artifact:<id>`, or "".
func refID(word string) string {
	id, ok := strings.CutPrefix(word, "artifact:")
	if !ok || len(id) > artifactIDMax || !artifactID.MatchString(id) {
		return ""
	}
	return id
}

// boundaryAt reports whether a reference may start at i: at the start, or
// after a space or an opening mark.
func boundaryAt(text string, i int) bool {
	return i == 0 || strings.ContainsRune(" \t\n\r([\"'<", rune(text[i-1]))
}

// artifactRefs finds the references of a Markdown text, in order: none in a
// fenced code block, and inline code only when all of it is a reference.
func artifactRefs(text string) []artifactRef {
	return textRefs(text, refID)
}

func textRefs(text string, match func(string) string) []artifactRef {
	var refs []artifactRef
	fenced := false
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			offset += len(line)
			continue
		}
		if !fenced {
			refs = append(refs, matchingLineRefs(line, offset, match)...)
		}
		offset += len(line)
	}
	return refs
}

func matchingLineRefs(line string, offset int, match func(string) string) []artifactRef {
	var refs []artifactRef
	for i := 0; i < len(line); {
		switch {
		case line[i] == '`':
			end := strings.IndexByte(line[i+1:], '`')
			if end < 0 {
				return refs
			}
			if id := match(strings.TrimSpace(line[i+1 : i+1+end])); id != "" {
				refs = append(refs, artifactRef{id: id, start: offset + i, end: offset + i + end + 2})
			}
			i += end + 2
		case boundaryAt(line, i):
			word := line[i:]
			if sp := strings.IndexAny(word, " \t\r\n"); sp >= 0 {
				word = word[:sp]
			}
			word = trailing.ReplaceAllString(word, "")
			if id := match(word); id != "" {
				linked := i >= 2 && line[i-2:i] == "]("
				refs = append(refs, artifactRef{id: id, start: offset + i, end: offset + i + len(word), linked: linked})
				i += len(word)
				continue
			}
			i++
		default:
			i++
		}
	}
	return refs
}

// linkArtifacts makes each reference of a text a Markdown link to it,
// "artifact:<id>#<hint>", whose fragment names the session to look in
// first (empty for none).
func linkArtifacts(text, hint string) string {
	refs := artifactRefs(text)
	if len(refs) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, r := range refs {
		if r.linked {
			continue
		}
		b.WriteString(text[last:r.start])
		label := text[r.start:r.end]
		b.WriteString("[" + label + "](artifact:" + r.id)
		if hint != "" {
			b.WriteString("#" + hint)
		}
		b.WriteString(")")
		last = r.end
	}
	b.WriteString(text[last:])
	return b.String()
}

// refIDs lists the ids a text references, each once, in order.
func refIDs(text string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, r := range artifactRefs(text) {
		if !seen[r.id] {
			seen[r.id] = true
			ids = append(ids, r.id)
		}
	}
	return ids
}
