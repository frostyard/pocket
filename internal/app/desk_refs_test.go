package app

import (
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/frostyard/pocket/internal/pocket"
)

func TestLinkDeskIDs(t *testing.T) {
	const T12 = "[T12](desk:T12)"
	const T8a = "[T8a](desk:T8a)"
	for _, c := range []struct{ name, in, want string }{
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
	} {
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
	parts := []string{"T1", "T12a", "[", "]", "(", ")", "`", "``", "\\", "<", ">", "\n", "\n\n", " ", "    ", "```\n", "~~~\n", "x", "://", "é", ":", "/", "."}
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
