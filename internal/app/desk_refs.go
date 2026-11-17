package app

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui/markdown/parser"
)

// deskIDRef is a whole desk id: a thread (T1903) or an ask (T1903a).
var deskIDRef = regexp.MustCompile(`^T[0-9]{1,6}[a-z]?$`)

// linkDeskIDs makes each desk id of a Markdown text a link, "[T1903a](desk:T1903a)":
// a thread id is T and 1 to 6 digits, an ask id adds 1 lower-case letter.
// Only the rendered text changes; callers keep the original for everything else.
//
// MyGo's parser (the one markdown.View reads, with its default extensions)
// decides what is prose: only plain text outside links and images is linked,
// so code, HTML, math, mentions, references and every link, whatever it
// holds, stay as they are, in quotes, lists and tables too. In that text, an
// id needs a word boundary on both sides (a byte of 0x80 or more is not a
// word byte). It is not linked after one of / \ . - : # @ = & ? % + ~ $ !,
// before "/", or before "." or "-" and a letter or digit, so paths, branches
// and files stay as they are. Linking twice gives the same text as linking
// once.
//
// A link can change how its neighbours parse (a star beside it may start
// emphasis). So the result is parsed too and must have the shape of the
// original with only the new links added: else the ids beside a "*" or "~"
// stay plain, and if that is not enough, the text is returned as it is.
func linkDeskIDs(text string) string {
	if !hasDeskCandidate(text) {
		return text
	}
	d := parser.New(parser.Default)
	d.Set(text)
	var spans [][2]int
	for i := 0; i < d.Len(); i++ {
		ins := d.Inlines(i)
		for k := 0; k < len(ins); {
			in := &ins[k]
			switch in.Kind {
			case parser.Link, parser.Image:
				k = max(in.Next, k+1)
				continue
			case parser.Text:
				if in.Start >= 0 && in.Start <= in.End && in.End <= len(text) {
					spans = deskIDSpans(spans, text, in.Start, in.End)
				}
			}
			k++
		}
	}
	if len(spans) == 0 {
		return text
	}
	out := insertDeskLinks(text, spans)
	if keepsShape(d, out) {
		return out
	}
	var safe [][2]int
	for _, sp := range spans {
		beside := sp[0] > 0 && strings.IndexByte("*~", text[sp[0]-1]) >= 0 ||
			sp[1] < len(text) && strings.IndexByte("*~", text[sp[1]]) >= 0
		if !beside {
			safe = append(safe, sp)
		}
	}
	if len(safe) == 0 || len(safe) == len(spans) {
		return text
	}
	if out = insertDeskLinks(text, safe); keepsShape(d, out) {
		return out
	}
	return text
}

// insertDeskLinks writes text with each span a link.
func insertDeskLinks(text string, spans [][2]int) string {
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		if sp[0] < last {
			continue
		}
		id := text[sp[0]:sp[1]]
		b.WriteString(text[last:sp[0]])
		b.WriteString("[" + id + "](desk:" + id + ")")
		last = sp[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// keepsShape reports whether out parses as the document orig does.
func keepsShape(orig *parser.Document, out string) bool {
	d := parser.New(parser.Default)
	d.Set(out)
	return sameShape(orig, d)
}

// sameShape reports whether 2 documents have the same blocks and the same
// inlines, not counting the desk links of one that the other has as plain
// text.
func sameShape(a, b *parser.Document) bool {
	if a.Len() != b.Len() {
		return false
	}
	for i := 0; i < a.Len(); i++ {
		if a.Block(i).Kind != b.Block(i).Kind || inlineShape(a.Inlines(i)) != inlineShape(b.Inlines(i)) {
			return false
		}
	}
	return true
}

// inlineShape is a signature of the inlines of a block: a token for each,
// the text of the leaves, adjacent text merged, and a desk link that holds
// only its id as that text.
func inlineShape(ins []parser.Inline) string {
	var sig, text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			sig.WriteString("t" + strconv.Quote(text.String()) + ";")
			text.Reset()
		}
	}
	var ends []int // where each open container ends
	for k := 0; k < len(ins); {
		for len(ends) > 0 && ends[len(ends)-1] <= k {
			flush()
			sig.WriteString(">")
			ends = ends[:len(ends)-1]
		}
		in := &ins[k]
		switch in.Kind {
		case parser.Text:
			text.WriteString(in.Text)
		case parser.Emphasis, parser.Strong, parser.Strikethrough, parser.Link, parser.Image:
			if id, ok := strings.CutPrefix(in.Dest, "desk:"); ok && in.Kind == parser.Link &&
				in.Next == k+2 && k+1 < len(ins) && ins[k+1].Kind == parser.Text && ins[k+1].Text == id {
				text.WriteString(id)
				k += 2
				continue
			}
			flush()
			sig.WriteString(in.Kind.String() + "(")
			if in.Kind == parser.Link || in.Kind == parser.Image {
				sig.WriteString(strconv.Quote(in.Dest) + strconv.Quote(in.Title) + strconv.FormatBool(in.Auto))
			}
			sig.WriteString(":")
			ends = append(ends, max(in.Next, k+1))
		default:
			flush()
			sig.WriteString(in.Kind.String() + strconv.Quote(in.Text) + strconv.FormatBool(in.Display) + ";")
		}
		k++
	}
	flush()
	for range ends {
		sig.WriteString(">")
	}
	return sig.String()
}

// deskIDSpans adds the desk ids of text[from:to] to spans.
func deskIDSpans(spans [][2]int, text string, from, to int) [][2]int {
	for i := from; i < to; i++ {
		if text[i] != 'T' {
			continue
		}
		if end := deskIDEnd(text, i); end > i && end <= to {
			spans = append(spans, [2]int{i, end})
			i = end - 1
		}
	}
	return spans
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

// isWordByte is the ASCII \w: a byte of 0x80 or more is a boundary.
func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// deskIDEnd returns where the desk id at p[i] ends, or 0 when none starts there.
func deskIDEnd(p string, i int) int {
	if i > 0 && (isWordByte(p[i-1]) || strings.IndexByte(`/\.-:#@=&?%+~$!`, p[i-1]) >= 0) {
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
