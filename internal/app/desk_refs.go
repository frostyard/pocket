package app

import (
	"regexp"
	"strings"
)

// deskIDRef is a whole desk id: a thread (T1903) or an ask (T1903a).
var deskIDRef = regexp.MustCompile(`^T[0-9]{1,6}[a-z]?$`)

// bracketReach is how far a link is looked for: it keeps a text of unmatched
// brackets from taking quadratic time.
const bracketReach = 2048

// linkDeskIDs makes each desk id of a Markdown text a link, "[T1903a](desk:T1903a)":
// a thread id is T and 1 to 6 digits, an ask id adds 1 lower-case letter.
// Only the rendered text changes; callers keep the original for everything else.
//
// An id needs a word boundary on both sides (a byte of 0x80 or more is not a
// word byte). It is not linked after one of / \ . - : # @ = & ? % + ~ $, before
// "/", or before "." or "-" and a letter or digit, so paths, branches, files and
// URLs stay as they are. Never linked: fenced code (``` or ~~~), indented code
// after a blank line, code spans (they can cross a line break, never a blank
// line), escaped bytes, links and their destinations, reference definitions,
// autolinks, and a word that holds "://" or starts with "www." or "mailto:".
// Linking twice gives the same text as linking once.
func linkDeskIDs(text string) string {
	if !hasDeskCandidate(text) {
		return text
	}
	var out, para strings.Builder
	flush := func() {
		if para.Len() > 0 {
			out.WriteString(linkParagraph(para.String()))
			para.Reset()
		}
	}
	var fence byte
	fenceLen := 0
	blank, code := true, false // the line before was blank; was indented code
	for _, line := range strings.SplitAfter(text, "\n") {
		mark, n := fenceRun(line)
		switch {
		case fence != 0:
			out.WriteString(line)
			if mark == fence && n >= fenceLen && strings.TrimSpace(strings.TrimLeft(line, " ")[n:]) == "" {
				fence = 0
			}
		case mark != 0 && (mark == '~' || !strings.Contains(strings.TrimLeft(line, " ")[n:], "`")):
			flush()
			out.WriteString(line)
			fence, fenceLen, blank, code = mark, n, false, false
		case strings.TrimSpace(line) == "":
			flush()
			out.WriteString(line)
			blank, code = true, false
		case (blank || code) && (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")):
			flush()
			out.WriteString(line)
			blank, code = false, true
		default:
			para.WriteString(line)
			blank, code = false, false
		}
	}
	flush()
	return out.String()
}

// hasDeskCandidate reports whether text has a T and a digit after it: most
// rows have none, and the transcript builds its rows again on each revision.
func hasDeskCandidate(text string) bool {
	for i := strings.IndexByte(text, 'T'); i >= 0; {
		if i+1 < len(text) && text[i+1] >= '0' && text[i+1] <= '9' {
			return true
		}
		next := strings.IndexByte(text[i+1:], 'T')
		if next < 0 {
			return false
		}
		i += 1 + next
	}
	return false
}

// fenceRun returns the character and length of the run of ` or ~ that starts
// a line (after spaces), when it has 3 or more; else 0.
func fenceRun(line string) (byte, int) {
	s := strings.TrimLeft(line, " ")
	if s == "" || (s[0] != '`' && s[0] != '~') {
		return 0, 0
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 {
		return 0, 0
	}
	return s[0], n
}

// deskScan walks a paragraph.
type deskScan struct {
	p string
	// open is a backtick run length with no closing run after the point that
	// was searched: no later run of it closes either.
	open map[int]bool
}

func linkParagraph(p string) string {
	if !hasDeskCandidate(p) {
		return p
	}
	s := &deskScan{p: p, open: map[int]bool{}}
	var b strings.Builder
	last := 0
	for i := 0; i < len(p); {
		if i == 0 || isSpaceByte(p[i-1]) {
			if end := s.skipToken(i); end > i {
				i = end
				continue
			}
		}
		switch p[i] {
		case '\\':
			i += 2
		case '`':
			i = s.skipCode(i)
		case '[':
			if end := s.skipBracket(i); end > i {
				i = end
			} else {
				i++
			}
		case '<':
			if end := s.skipAutolink(i); end > i {
				i = end
			} else {
				i++
			}
		case 'T':
			end := s.idEnd(i)
			if end == 0 {
				i++
				continue
			}
			id := p[i:end]
			b.WriteString(p[last:i])
			b.WriteString("[" + id + "](desk:" + id + ")")
			last, i = end, end
		default:
			i++
		}
	}
	if last == 0 {
		return p
	}
	b.WriteString(p[last:])
	return b.String()
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// isWordByte is the ASCII \w: a byte of 0x80 or more is a boundary.
func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// idEnd returns where the desk id at i ends, or 0 when none starts there.
func (s *deskScan) idEnd(i int) int {
	p := s.p
	if i > 0 && (isWordByte(p[i-1]) || strings.IndexByte(`/\.-:#@=&?%+~$`, p[i-1]) >= 0) {
		return 0
	}
	j := i + 1
	for j < len(p) && p[j] >= '0' && p[j] <= '9' {
		j++
	}
	if n := j - i - 1; n < 1 || n > 6 {
		return 0
	}
	if j < len(p) && p[j] >= 'a' && p[j] <= 'z' {
		j++
	}
	if j < len(p) {
		c := p[j]
		switch {
		case isWordByte(c), c == '/':
			return 0
		case (c == '.' || c == '-') && j+1 < len(p) && isWordByte(p[j+1]) && p[j+1] != '_':
			return 0
		}
	}
	return j
}

// skipToken returns the end of the word at i when it is a URL, or i.
func (s *deskScan) skipToken(i int) int {
	end := i
	for end < len(s.p) && !isSpaceByte(s.p[end]) && s.p[end] != '`' {
		end++
	}
	word := strings.TrimLeft(s.p[i:end], "(<[\"'*_")
	if strings.Contains(word, "://") || strings.HasPrefix(word, "www.") || strings.HasPrefix(word, "mailto:") {
		return end
	}
	return i
}

// skipCode returns the end of the code span that opens at i, or the end of
// the backtick run when nothing closes it.
func (s *deskScan) skipCode(i int) int {
	p := s.p
	n := 0
	for i+n < len(p) && p[i+n] == '`' {
		n++
	}
	if s.open[n] {
		return i + n
	}
	for j := i + n; j < len(p); {
		if p[j] != '`' {
			j++
			continue
		}
		m := 0
		for j+m < len(p) && p[j+m] == '`' {
			m++
		}
		if m == n {
			return j + m
		}
		j += m
	}
	s.open[n] = true
	return i + n
}

// skipBracket returns the end of the link, reference or definition that
// starts at the "[" at i, or -1 when it is none.
func (s *deskScan) skipBracket(i int) int {
	p := s.p
	depth := 0
	k := i
	for ; k < len(p) && k-i < bracketReach; k++ {
		switch p[k] {
		case '\\':
			k++
		case '`':
			k = s.skipCode(k) - 1
		case '[':
			depth++
		case ']':
			depth--
		}
		if depth == 0 {
			break
		}
	}
	if depth != 0 || k >= len(p) || p[k] != ']' || k+1 >= len(p) {
		return -1
	}
	switch p[k+1] {
	case '(':
		return s.skipParens(k + 1)
	case '[':
		for j := k + 2; j < len(p) && j-k < bracketReach; j++ {
			switch p[j] {
			case '\\':
				j++
			case ']':
				return j + 1
			}
		}
	case ':':
		if strings.TrimLeft(p[strings.LastIndexByte(p[:i], '\n')+1:i], " \t") == "" {
			if nl := strings.IndexByte(p[k:], '\n'); nl >= 0 {
				return k + nl
			}
			return len(p)
		}
	}
	return -1
}

// skipParens returns the end of the balanced parentheses at i, or -1.
func (s *deskScan) skipParens(i int) int {
	p := s.p
	depth := 0
	for k := i; k < len(p) && k-i < bracketReach; k++ {
		switch p[k] {
		case '\\':
			k++
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return k + 1
			}
		}
	}
	return -1
}

// skipAutolink returns the end of an autolink "<scheme:x>" or "<a@b>" at i.
func (s *deskScan) skipAutolink(i int) int {
	p := s.p
	for k := i + 1; k < len(p) && !isSpaceByte(p[k]); k++ {
		if p[k] == '>' {
			if strings.ContainsAny(p[i+1:k], ":@") {
				return k + 1
			}
			break
		}
	}
	return -1
}

// openDeskID follows a desk id: an open ask opens in the Inbox, any other
// ask or thread id opens its thread in In motion.
func (a *App) openDeskID(id string) {
	if !deskIDRef.MatchString(id) {
		a.toast("Not a desk id: " + id)
		return
	}
	threadID := id
	if last := id[len(id)-1]; last >= 'a' && last <= 'z' {
		threadID = id[:len(id)-1]
		for _, it := range a.inboxItems() {
			if it.key == "a:"+id {
				a.goPlace("x:inbox")
				a.inboxSel = "a:" + id
				return
			}
		}
	}
	a.openNumberThread(threadID)
}
