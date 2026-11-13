package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// transcriptTester shows entries in a session's transcript, as the
// conversation pane draws them.
func transcriptTester(t *testing.T, name string, entries ...*pocket.Entry) (*App, *ui.Tester) {
	t.Helper()
	a, _ := teamApp(t)
	a.current = 7
	a.view = pocket.NewView()
	for _, e := range entries {
		a.view.Order = append(a.view.Order, e.ID)
		a.view.Entries[e.ID] = e
	}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.conversationPane(c) }, 900, 600, ui.TesterOptions{})
	tt.Frame()
	writePNG(t, tt, name)
	return a, tt
}

// countText says how many times the rendered transcript shows want.
func countText(tt *ui.Tester, want string) int {
	n := 0
	for _, s := range tt.Texts() {
		n += strings.Count(s, want)
	}
	return n
}

func TestUserRowShowsFilesWithoutText(t *testing.T) {
	a, tt := transcriptTester(t, "user-row-files", &pocket.Entry{ID: 77, Kind: "user", Files: []string{"/home/team/reports/report.pdf"}})
	if !tt.HasText("report.pdf") {
		t.Fatalf("files-only row shows no file: %q", tt.Texts())
	}
	if n := countText(tt, "report.pdf"); n != 1 {
		t.Errorf("report.pdf drawn %d times, want 1", n)
	}
	// What the row draws for a file is what Find finds.
	a.tr.find = sessionFind{open: true, query: "report.pdf"}
	if n := len(a.findMatches()); n < 1 {
		t.Errorf("find does not match the shown file name: %d matches", n)
	}
}

func TestUserRowShowsImageCount(t *testing.T) {
	_, tt := transcriptTester(t, "user-row-images",
		&pocket.Entry{ID: 77, Kind: "user", Images: 1},
		&pocket.Entry{ID: 78, Kind: "user", Images: 3})
	if !tt.HasText("1 image") || !tt.HasText("3 images") {
		t.Fatalf("images-only rows show no image count: %q", tt.Texts())
	}
}

func TestUserRowShowsTextFilesAndImages(t *testing.T) {
	_, tt := transcriptTester(t, "user-row-mixed",
		&pocket.Entry{ID: 77, Kind: "user", Text: "Look at these", Files: []string{"report.pdf", "/tmp/x/photo.png"}, Images: 2})
	for _, want := range []string{"Look at these", "report.pdf", "photo.png", "2 images"} {
		if !tt.HasText(want) {
			t.Errorf("mixed row lacks %q: %q", want, tt.Texts())
		}
	}
}

func TestUserRowShowsLegacyAttachmentsOnce(t *testing.T) {
	legacy := "Here you go" + attachedMarker + "- /home/team/a/notes.txt\n- /home/team/a/report.pdf"
	_, tt := transcriptTester(t, "user-row-legacy",
		&pocket.Entry{ID: 77, Kind: "user", Text: legacy, Files: []string{"/home/team/a/report.pdf", "/home/team/a/photo.png"}},
		&pocket.Entry{ID: 78, Kind: "user", Text: "Only the old form" + attachedMarker + "- /home/team/b/legacy.txt"})
	for _, want := range []string{"Here you go", "Only the old form", "notes.txt", "report.pdf", "photo.png", "legacy.txt"} {
		if !tt.HasText(want) {
			t.Errorf("legacy row lacks %q: %q", want, tt.Texts())
		}
	}
	for _, want := range []string{"notes.txt", "report.pdf", "photo.png", "legacy.txt"} {
		if n := countText(tt, want); n != 1 {
			t.Errorf("%s drawn %d times, want once: %q", want, n, tt.Texts())
		}
	}
	if tt.HasText("Attached files (saved on the server)") {
		t.Error("the legacy suffix is still drawn as text")
	}
}

// A recorded name repeats a legacy path by its base name, but 2 recorded files
// with the same base name in different folders are 2 files.
func TestUserRowAttachmentRepeats(t *testing.T) {
	legacy := "Two" + attachedMarker + "- /home/team/a/report.pdf"
	_, tt := transcriptTester(t, "user-row-repeats",
		&pocket.Entry{ID: 77, Kind: "user", Text: legacy, Files: []string{"report.pdf"}},
		&pocket.Entry{ID: 78, Kind: "user", Files: []string{"/x/data.csv", "/y/data.csv"}})
	if n := countText(tt, "report.pdf"); n != 1 {
		t.Errorf("report.pdf drawn %d times, want once: %q", n, tt.Texts())
	}
	if n := countText(tt, "data.csv"); n != 2 {
		t.Errorf("data.csv drawn %d times, want 2: %q", n, tt.Texts())
	}
}

// File names keep their case: Report.pdf and report.pdf are 2 files, while the
// exact same name twice is 1.
func TestUserRowKeepsCaseDistinctFiles(t *testing.T) {
	_, tt := transcriptTester(t, "user-row-case",
		&pocket.Entry{ID: 77, Kind: "user", Files: []string{"Report.pdf", "report.pdf", "report.pdf"}},
		&pocket.Entry{ID: 78, Kind: "user", Text: "Old" + attachedMarker + "- /a/Notes.txt\n- /a/notes.txt"})
	for _, want := range []string{"Report.pdf", "report.pdf", "Notes.txt", "notes.txt"} {
		if n := countText(tt, want); n != 1 {
			t.Errorf("%s drawn %d times, want once: %q", want, n, tt.Texts())
		}
	}
}

// A file named like the image pill's label does not share its identity.
func TestUserRowFileNamedImages(t *testing.T) {
	_, tt := transcriptTester(t, "user-row-images-file",
		&pocket.Entry{ID: 77, Kind: "user", Files: []string{"images"}, Images: 1})
	if countText(tt, "images") < 1 || !tt.HasText("1 image") {
		t.Fatalf("row lacks the file or the image count: %q", tt.Texts())
	}
}
