package app

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/markdown/parser"
	"github.com/frostyard/pocket/internal/pocket"
)

type deskLinkCase struct{ name, in, want string }

func deskLinkCases() []deskLinkCase {
	const T12 = "[T12](desk:T12)"
	const T8a = "[T8a](desk:T8a)"
	return []deskLinkCase{
		{"none", "no ids here", "no ids here"},
		{"thread", "See T12 now", "See " + T12 + " now"},
		{"ask", "Needs you: T8a", "Needs you: " + T8a},
		{"several", "T12, T8a and T8", T12 + ", " + T8a + " and [T8](desk:T8)"},
		{"parens", "(T12)", "(" + T12 + ")"},
		{"period", "See T12.", "See " + T12 + "."},
		{"comma", "T12, then", T12 + ", then"},
		{"colon", "T12: done", T12 + ": done"},
		{"bold", "**T12**", "**" + T12 + "**"},
		{"span", "run `T12` now", "run `T12` now"},
		{"double span", "run ``a T12 b`` now", "run ``a T12 b`` now"},
		{"span over a line", "`a\nT12 b` T8a", "`a\nT12 b` " + T8a},
		{"span closed by another run", "``a ` T12`` T8a", "``a ` T12`` " + T8a},
		{"open backticks", "`a T12", "`a " + T12},
		{"fence", "a\n```\nT12\n```\nT8a", "a\n```\nT12\n```\n" + T8a},
		{"tilde fence", "~~~go\nT12\n~~~\nT8a", "~~~go\nT12\n~~~\n" + T8a},
		{"long fence", "````\n```\nT12\n```\n````\nT8a", "````\n```\nT12\n```\n````\n" + T8a},
		{"open fence", "```\nT12", "```\nT12"},
		{"indented code", "text\n\n    T12\n\tT8a\n\nT12", "text\n\n    T12\n\tT8a\n\n" + T12},
		{"indent in paragraph", "text\n    T12", "text\n    " + T12},
		{"url", "https://example.org/T123", "https://example.org/T123"},
		{"url in text", "see (https://example.org/x?a=T12) T8a", "see (https://example.org/x?a=T12) " + T8a},
		{"autolink", "<https://example.org/T123> T8a", "<https://example.org/T123> " + T8a},
		{"www", "www.example.org/T1", "www.example.org/T1"},
		{"mailto", "mailto:T1@example.org", "mailto:T1@example.org"},
		{"link", "[T12](https://x)", "[T12](https://x)"},
		{"link with spaces", "[see T12 here](a/b) T8a", "[see T12 here](a/b) " + T8a},
		{"link to desk", "[see](desk:T12)", "[see](desk:T12)"},
		{"parens in destination", "[a](b_(T12)) T8a", "[a](b_(T12)) " + T8a},
		// Changed with the parser: with no definition, a reference is prose.
		{"undefined reference", "[a T12][ref] T8a", "[a " + T12 + "][ref] " + T8a},
		// Changed with the parser: "x T12" is no destination and title, so this is no definition.
		{"not a definition", "[ref]: x T12\nT8a", "[ref]: x " + T12 + "\n" + T8a},
		{"definition", "[ref]: https://example.org \"T12\"\nT8a", "[ref]: https://example.org \"T12\"\n" + T8a},
		{"brackets only", "[T12]", "[" + T12 + "]"},
		{"quoted tilde fence", "> ~~~\n> T12\n> ~~~", "> ~~~\n> T12\n> ~~~"},
		{"quoted backtick fence", "> ```\n> T12\n> ```", "> ```\n> T12\n> ```"},
		{"fence in a list item", "- a\n\n  ```\n  T12\n  ```", "- a\n\n  ```\n  T12\n  ```"},
		{"indented code in a list item", "- a\n\n      T12", "- a\n\n      T12"},
		{"quoted prose", "> see T12", "> see " + T12},
		{"shortcut reference", "[T12]\n\n[T12]: https://example.org", "[T12]\n\n[T12]: https://example.org"},
		{"full reference", "[x T12][r]\n\n[r]: https://example.org", "[x T12][r]\n\n[r]: https://example.org"},
		{"collapsed reference", "[T12][]\n\n[T12]: https://example.org", "[T12][]\n\n[T12]: https://example.org"},
		{"display math", "$$x + T13$$", "$$x + T13$$"},
		// MyGo reads single-dollar math only with ExtDollarMath, which Default lacks:
		// the view shows this as plain text, so the id is linked.
		{"single dollars", "$x T13$", "$x " + "[T13](desk:T13)$"},
		{"inline math", "$$x T13$$ and T12", "$$x T13$$ and " + T12},
		{"math block", "$$\nT13\n$$", "$$\nT13\n$$"},
		{"html attribute", `<span title="T13">x</span>`, `<span title="T13">x</span>`},
		// The parser reads an HTML block as raw HTML up to the blank line.
		{"html block", "<div>\nT13\n</div>", "<div>\nT13\n</div>"},
		{"between tags", "<b>T13</b>", "<b>[T13](desk:T13)</b>"},
		{"table cell", "| a | b |\n|---|---|\n| T12 | x \\| T8a |", "| a | b |\n|---|---|\n| " + T12 + " | x \\| " + T8a + " |"},
		{"heading", "## T12 done", "## " + T12 + " done"},
		{"mention", "@T12", "@T12"},
		{"after bracket text", "[a] T12", "[a] " + T12},
		{"too long", "T12345abc", "T12345abc"},
		{"7 digits", "T1234567", "T1234567"},
		{"lower", "t12", "t12"},
		{"prefix letter", "XT12", "XT12"},
		{"underscore", "T12_x", "T12_x"},
		{"two letters", "T12ab", "T12ab"},
		{"file", "reports/T12.md", "reports/T12.md"},
		{"file bare", "T12.md and T12-x", "T12.md and T12-x"},
		{"branch", "sheeana/T12", "sheeana/T12"},
		{"slash after", "T12/plan", "T12/plan"},
		{"anchor", "#T12", "#T12"},
		{"mention", "@T12 =T12 ?T12 &T12 %T12 +T12 ~T12 \\T12 -T12 .T12 :T12", "@T12 =T12 ?T12 &T12 %T12 +T12 ~T12 \\T12 -T12 .T12 :T12"},
		{"math", "$T12$ and $$T12$$", "$T12$ and $$T12$$"},
		{"german", "Siehe T12 für", "Siehe " + T12 + " für"},
		{"arrows", "→T12a←", "→[T12a](desk:T12a)←"},
		{"accent before", "éT12", "é" + T12},
		{"cjk", "见T12。", "见" + T12 + "。"},
		{"escaped", `\T12`, `\T12`},
		{"escape at end", "T12 \\", T12 + " \\"},
		{"lone bytes", "T12 \xff T8a \xc3", T12 + " \xff " + T8a + " \xc3"},
		{"blank line ends a span", "`a\n\nT12`", "`a\n\n" + T12 + "`"},
		{"tab before", "a\tT12", "a\t" + T12},
		{"empty", "", ""},
		{"crlf", "T12\r\nT8a\r\n", T12 + "\r\n" + T8a + "\r\n"},
		// A bang would turn the link into an image.
		{"bang", "!T12", "!T12"},
		// The link's ")" would make the stars emphasis: the id stays plain.
		{"stars after", "T12*\"x\"*", "T12*\"x\"*"},
		{"strong", "**T12**", "**" + T12 + "**"},
		{"strong in a sentence", "a **T12** b", "a **" + T12 + "** b"},
		// Inside a word the link changes how the stars flank: the id stays plain.
		{"strong in a word", "a**T12**b", "a**T12**b"},
		{"emphasis", "*T12*", "*" + T12 + "*"},
		{"strike", "~~T12~~", "~~T12~~"},
		// The risky id stays plain; the safe one is linked.
		{"risky and safe", "T12*\"x\"* and T8a", "T12*\"x\"* and " + T8a},
		{"strong then id", "T12**x**", T12 + "**x**"},
	}
}

func TestLinkDeskIDs(t *testing.T) {
	for _, c := range deskLinkCases() {
		t.Run(c.name, func(t *testing.T) {
			got := linkDeskIDs(c.in)
			if got != c.want {
				t.Errorf("linkDeskIDs(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
			if again := linkDeskIDs(got); again != got {
				t.Errorf("not idempotent: %q then %q", got, again)
			}
		})
	}
}

func TestLinkDeskIDsComposesWithDeskPaths(t *testing.T) {
	got := linkDeskIDs(linkDeskPaths("Read reports/T12.md and T12a"))
	want := "Read [reports/T12.md](deskfile:reports/T12.md) and [T12a](desk:T12a)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLinkDeskIDsIsStableOnOddText(t *testing.T) {
	parts := []string{"T1", "T12a", "[", "]", "(", ")", "`", "``", "\\", "<", ">", "\n", "\n\n", " ", "    ", "```\n", "~~~\n", "x", "://", "é", ":", "/", ".", "*", "**", "~~", "!", "_", "- ", "> "}
	seed := uint32(7)
	for range 4000 {
		var b strings.Builder
		for range 1 + int(seed%24) {
			seed = seed*1664525 + 1013904223
			b.WriteString(parts[int(seed>>8)%len(parts)])
		}
		seed = seed*1664525 + 1013904223
		in := b.String()
		got := linkDeskIDs(in)
		if again := linkDeskIDs(got); again != got {
			t.Fatalf("not idempotent on %q: %q then %q", in, got, again)
		}
		if !utf8.ValidString(got) && utf8.ValidString(in) {
			t.Fatalf("broke UTF-8 on %q: %q", in, got)
		}
		if !keepsShape(parseDesk(in), got) {
			t.Fatalf("changed the parse of %q: %q", in, got)
		}
	}
}

func TestLinkDeskIDsKeepsLongLinks(t *testing.T) {
	text := "[" + strings.Repeat("a T12 ", 600) + "](https://example.org/" + strings.Repeat("x", 3000) + ")"
	if got := linkDeskIDs(text); got != text {
		t.Fatal("a long link was changed")
	}
}

func TestLinkDeskIDsScansLongRunsQuickly(t *testing.T) {
	for _, unit := range []string{"[", "<", "`"} {
		// A line of backticks alone would open a fence: start with a word.
		text := "x " + strings.Repeat(unit, 50000) + " T12"
		if got := linkDeskIDs(text); !strings.HasSuffix(got, "[T12](desk:T12)") {
			t.Errorf("id after 50000 %q was not linked", unit)
		}
	}
}

func deskRefApp(t *testing.T) *App {
	t.Helper()
	a, _ := teamApp(t)
	a.desk.view = &pocket.DeskView{Threads: []pocket.DeskThread{{ID: "T8", Title: "x", State: "open", Asks: []pocket.DeskAsk{{ID: "T8a", Text: "y"}}}}}
	a.invalidateDerived()
	return a
}

func TestFollowDeskAskOpensInbox(t *testing.T) {
	a := deskRefApp(t)
	a.followLink("desk:T8a")
	if a.selected != "x:inbox" || a.inboxSel != "a:T8a" {
		t.Fatalf("selected %q, inbox %q", a.selected, a.inboxSel)
	}
}

func TestFollowDeskThreadOpensInMotion(t *testing.T) {
	for _, id := range []string{"T8", "T8b"} {
		a := deskRefApp(t)
		a.followLink("desk:" + id)
		if a.selected != "x:motion" || a.threadSel != "T8" || a.desk.recordThread != nil {
			t.Fatalf("%s: selected %q, thread %q", id, a.selected, a.threadSel)
		}
	}
}

func TestFollowDeskUnknownThreadShowsTheError(t *testing.T) {
	a, loop := teamApp(t)
	a.link.client.HTTP = &http.Client{Transport: deskFileTransport(func(r *http.Request) (*http.Response, error) {
		resp := reply(r, `{"error":"No such thread"}`)
		resp.StatusCode = http.StatusNotFound
		return resp, nil
	})}
	a.followLink("desk:T99")
	if a.selected != "x:motion" || a.threadSel != "T99" || a.desk.recordThread == nil {
		t.Fatalf("selected %q, thread %q", a.selected, a.threadSel)
	}
	loop.RunUntil(t, func() bool { return !a.desk.recordThread.loading })
	if a.desk.recordThread.err == "" {
		t.Fatal("the missing thread gave no error")
	}
}

func TestFollowDeskRejectsOtherText(t *testing.T) {
	a := deskRefApp(t)
	place := a.selected
	before := len(a.pendingToasts)
	a.followLink("desk:nope")
	if len(a.pendingToasts) != before+1 || a.selected != place {
		t.Fatalf("toasts %v, place %q", a.pendingToasts, a.selected)
	}
}

func TestBuildItemsLinksDeskIDsOnlyInTheRenderedText(t *testing.T) {
	v := pocket.NewView()
	v.Order = []int{1, 2}
	v.Entries[1] = &pocket.Entry{ID: 1, Kind: "user", Text: "Is T8 done?"}
	v.Entries[2] = &pocket.Entry{ID: 2, Kind: "assistant", Blocks: []pocket.Block{{Type: "text", Text: "Needs you: T8a"}}}
	items := buildItems(v)
	var texts int
	for _, it := range items {
		switch it.kind {
		case itemText:
			texts++
			if it.text != "Needs you: T8a" || it.md != "Needs you: [T8a](desk:T8a)" {
				t.Errorf("text %q, md %q", it.text, it.md)
			}
		case itemUser:
			if it.text != "Is T8 done?" || it.md != "Is [T8](desk:T8) done?" {
				t.Errorf("text %q, md %q", it.text, it.md)
			}
		}
	}
	if texts != 1 {
		t.Fatalf("%d text rows", texts)
	}
}

func parseDesk(text string) *parser.Document {
	d := parser.New(parser.Default)
	d.Set(text)
	return d
}

// countInlines counts the inlines of a kind in a document, and the desk
// links whose content is not exactly their id.
func countInlines(d *parser.Document, kind parser.InlineKind) (n, badDesk int) {
	for i := 0; i < d.Len(); i++ {
		ins := d.Inlines(i)
		for k, in := range ins {
			if in.Kind == kind {
				n++
			}
			if id, ok := strings.CutPrefix(in.Dest, "desk:"); ok && in.Kind == parser.Link &&
				(in.Next != k+2 || ins[k+1].Kind != parser.Text || ins[k+1].Text != id) {
				badDesk++
			}
		}
	}
	return n, badDesk
}

func plainTexts(d *parser.Document) []string {
	var out []string
	for i := 0; i < d.Len(); i++ {
		out = append(out, parser.PlainText(d.Inlines(i)))
	}
	return out
}

func TestLinkDeskIDsKeepsTheParse(t *testing.T) {
	var inputs []string
	for _, c := range deskLinkCases() {
		inputs = append(inputs, c.in)
	}
	inputs = append(inputs, "!T12 and T8a", "T12*x*", "*T12 *x*", "a ~~T12~~ b T8a", "**T12**T8a", "_T12_", "__T12__", "x T12**", "> **T12** and T8a", "- *T12*\n- T8a")
	for _, in := range inputs {
		out := linkDeskIDs(in)
		a, b := parseDesk(in), parseDesk(out)
		before, _ := countInlines(a, parser.Image)
		after, _ := countInlines(b, parser.Image)
		if after != before {
			t.Errorf("%q: images %d -> %d in %q", in, before, after, out)
		}
		_, badBefore := countInlines(a, parser.Link)
		if _, bad := countInlines(b, parser.Link); bad > badBefore {
			t.Errorf("%q: a desk link holds more than its id in %q", in, out)
		}
		if !sameShape(a, b) {
			t.Errorf("%q: the parse changed in %q", in, out)
		}
		if x, y := plainTexts(a), plainTexts(b); !slices.Equal(x, y) {
			t.Errorf("%q: plain text %q -> %q", in, x, y)
		}
	}
}

func TestSameShapeSeesAChangedParse(t *testing.T) {
	for _, pair := range [][2]string{
		{"!T12", "![T12](desk:T12)"},
		{"T12*\"x\"*", "[T12](desk:T12)*\"x\"*"},
		{"a **T12** b", "a **T12 b"},
		{"T12", "[T12](desk:T13)"},
	} {
		if sameShape(parseDesk(pair[0]), parseDesk(pair[1])) {
			t.Errorf("%q and %q have the same shape", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{
		{"see T12 now", "see [T12](desk:T12) now"},
		{"**T12**", "**[T12](desk:T12)**"},
		{"a\n\nb", "a\n\nb"},
	} {
		if !sameShape(parseDesk(pair[0]), parseDesk(pair[1])) {
			t.Errorf("%q and %q have another shape", pair[0], pair[1])
		}
	}
}
