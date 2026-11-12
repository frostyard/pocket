// Command pocket is a desktop client of a Pi Pocket server: a native
// window on MyGo and frostui over the server's API and event stream.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/app"
	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/icons"
)

// version is stamped at build time.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pocket:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := frostui.SetAppID(config.AppID, "Pocket"); err != nil {
		slog.Warn("setting the app ID", "err", err)
	}
	// On Linux the name is the desktop's app ID, which matches the window
	// to its desktop entry. On macOS and Windows a packaged app takes the
	// name it was built with ("Pocket"), which its menus show.
	if runtime.GOOS == "linux" {
		mygo.App.SetName(config.AppID)
	}
	frostui.SetIcons(frostui.NewIcons(icons.Embedded()))

	store, err := config.Open(os.Getenv("POCKET_CONFIG_DIR"))
	if err != nil {
		return err
	}
	p := &poster{}
	var win *mygo.Window
	a, err := app.New(app.Options{
		Version: version,
		Post:    p.post,
		Store:   store,
		Creds:   creds(store.Dir()),
		Notify:  notifier{win: func() *mygo.Window { return win }},
		Quit:    mygo.App.Quit,
		Badge:   mygo.App.SetBadgeCount,
		Raise: func() {
			if win != nil {
				win.Restore()
				win.Focus()
			}
		},
	})
	if err != nil {
		return err
	}
	mygo.App.WhenReady(func() {
		win = mygo.NewWindow(mygo.WindowOptions{
			Title: "Pocket", Width: 1360, Height: 860, MinWidth: 760, MinHeight: 480,
			TitleBarStyle: mygo.TitleBarHidden,
			Content:       ui.View(a.View),
		})
		win.OnFocus(func() { p.post(func() { a.SetFocused(true) }) })
		win.OnBlur(func() { p.post(func() { a.SetFocused(false) }) })
		win.OnClose(func(*mygo.CloseEvent) { a.Close() })
		p.attach(win)
		a.Start()
	})
	return mygo.App.Run()
}

// poster hands functions to the window's UI thread. It is safe from any
// goroutine and keeps what comes before the window exists until attach.
type poster struct {
	mu      sync.Mutex
	win     *mygo.Window
	pending []func()
}

func (p *poster) post(fn func()) {
	p.mu.Lock()
	if p.win == nil {
		p.pending = append(p.pending, fn)
		p.mu.Unlock()
		return
	}
	win := p.win
	p.mu.Unlock()
	win.Update(fn)
}

func (p *poster) attach(win *mygo.Window) {
	p.mu.Lock()
	p.win = win
	pending := p.pending
	p.pending = nil
	p.mu.Unlock()
	for _, fn := range pending {
		win.Update(fn)
	}
}

// creds keeps tokens in the desktop's keyring, or, with
// POCKET_NO_KEYRING set (development), in a file of the config folder only.
func creds(dir string) config.Credentials {
	if os.Getenv("POCKET_NO_KEYRING") != "" {
		return config.Credentials{Dir: dir}
	}
	return config.Credentials{Ring: secrets{}, NotFound: mygo.ErrSecretNotFound, Dir: dir}
}

// secrets is mygo.Secrets as a config.Keyring.
type secrets struct{}

// keyring is the credential store's service: the app's identifier
// whether it runs packaged or with go run, so both find one sign-in.
var keyring = mygo.Secrets.Service(config.AppID)

func (secrets) Set(key string, value []byte) error { return keyring.Set(key, value) }
func (secrets) Get(key string) ([]byte, error)     { return keyring.Get(key) }
func (secrets) Delete(key string) error {
	err := keyring.Delete(key)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return nil
	}
	return err
}

// notifier shows desktop notifications.
type notifier struct{ win func() *mygo.Window }

func (n notifier) Notify(title, body string, onClick func()) {
	if !mygo.NotificationsSupported() {
		return
	}
	note := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body})
	note.OnClick(func() {
		if w := n.win(); w != nil {
			w.Restore()
			w.Focus()
		}
		if onClick != nil {
			onClick()
		}
		note.Close()
	})
	go func() {
		if err := note.Show(); err != nil {
			slog.Debug("notification", "err", err)
		}
	}()
}
