package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/icons"
	"github.com/frostyard/pocket/internal/pocket"
)

// TestLiveTerminal runs a shell through the terminal module of a demo
// server started with POCKET_DEMO_MODULES (scripts/pocket-demo.ts): it
// types a command into the Terminal place and reads its output back.
func TestLiveTerminal(t *testing.T) {
	server, token := os.Getenv("POCKET_DEMO_SERVER"), os.Getenv("POCKET_DEMO_TOKEN")
	if server == "" || token == "" {
		t.Skip("POCKET_DEMO_SERVER and POCKET_DEMO_TOKEN are not set")
	}
	if err := terminal.Load(); err != nil {
		t.Skipf("the terminal library does not load: %v", err)
	}
	t.Setenv("FROSTUI_ICON_THEME", "Adwaita")
	frostui.SetIcons(frostui.NewIcons(icons.Embedded()))
	store, _ := config.Open(t.TempDir())
	loop := frostuitest.NewLoop()
	a, err := New(Options{Post: loop.Post, Store: store, Creds: config.Credentials{Dir: store.Dir()}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTesterWith(a.View, 1280, 820, ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	frames := func(n int) {
		for range n {
			tt.Advance(50 * time.Millisecond)
			loop.Flush()
			tt.Frame()
		}
	}
	until := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(20 * time.Millisecond)
			frames(1)
		}
	}
	loop.Post(func() { a.connect(server, token) })
	until("the terminal module", func() bool { return a.terms.on })
	loop.Post(func() { a.newTerminal(pocket.TerminalPlace{}) })
	until("a shell", func() bool { return a.terms.sel != "" && a.terms.open[a.terms.sel] != nil })
	ot := a.terms.open[a.terms.sel]
	until("a prompt", func() bool { return strings.TrimSpace(ot.term.Text()) != "" })
	tt.Type("echo pocket-$((40+2))")
	tt.Key(0, ui.KeyEnter)
	until("the command's output", func() bool { return strings.Contains(ot.term.Text(), "pocket-42") })
	if !a.terms.focused {
		t.Errorf("the terminal does not have the focus")
	}
	if cmd, _ := a.table.Command("switcher"); cmd.Enabled != nil {
		if ok, _ := cmd.Enabled(); ok && !strings.Contains(strings.ToLower(os.Getenv("GOOS")), "darwin") {
			t.Errorf("Ctrl P is the window's while the terminal has the focus")
		}
	}
	frames(10)
	writePNG(t, tt, "terminal")
	id := a.terms.sel
	loop.Post(func() { a.endTerminal(id) })
	until("the shell to end", func() bool { return a.liveTerminals() == 0 })
	loop.Post(a.disconnect)
	loop.Flush()
}
