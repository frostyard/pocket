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
	// Artifacts' pages show in a web view where the system has one.
	var web app.WebPane
	pane := &webPane{}
	if mygo.WebViewAvailable() {
		web = pane
	}
	a, err := app.New(app.Options{
		Version: version,
		Post:    p.post,
		Store:   store,
		Creds:   creds(store.Dir()),
		Notify:  notifier{win: func() *mygo.Window { return win }},
		Web:     web,
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
		if web != nil {
			view, err := win.NewWebView(mygo.WebViewOptions{})
			if err != nil {
				slog.Warn("the artifacts' web view", "err", err)
			} else {
				pane.attach(view)
				page := view.Page()
				page.OnWillNavigate(func(e *mygo.NavigateEvent) {
					switch pane.web.Navigate(e.URL, e.UserInitiated) {
					case app.NavigationAllow:
					case app.NavigationExternal:
						e.PreventDefault()
						openExternal(e.URL)
					default:
						e.PreventDefault()
					}
				})
				// Reloads and moves in the history do not reach OnWillNavigate:
				// a page that lands outside the rule all the same is blanked.
				page.OnDidNavigate(pane.web.Committed)
				page.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
					if app.ArtifactPopup(req.URL, req.UserInitiated) {
						openExternal(req.URL)
					}
					return nil
				})
			}
		}
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

// webPane shows artifacts' pages in a web view of the window, signed in
// with the person's token as the cookie Pi Pocket's own pages use.
//
// The page of an artifact is a program a person did not write, so the view
// loads only the server's /a/ pages, the ones the server sandboxes; a link
// elsewhere goes to the person's browser. app.ArtifactWeb holds that logic.
type webPane struct {
	view *mygo.WebView
	web  *app.ArtifactWeb
}

// attach gives the pane the window's web view.
func (w *webPane) attach(view *mygo.WebView) {
	w.view = view
	w.web = app.NewArtifactWeb(mygoWeb{view: view}, func(what string, err error) { slog.Warn(what, "err", err) })
}

func (w *webPane) View() ui.NativeWebView {
	if w.view == nil {
		return nil
	}
	return w.view
}

// Show signs the view in to server and loads page, off the UI thread.
func (w *webPane) Show(page, server, token string) error {
	if w.web == nil {
		return errors.New("no web view")
	}
	return w.web.Show(page, server, token)
}

// SignOut deletes the sign-in cookies and blanks the view.
func (w *webPane) SignOut() {
	if w.web != nil {
		w.web.SignOut()
	}
}

// mygoWeb is MyGo's web view as app.ArtifactWeb uses it.
type mygoWeb struct{ view *mygo.WebView }

func (m mygoWeb) SetCookie(c app.WebCookie) error {
	return mygo.Cookies.Set(mygo.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
		Secure: c.Secure, HTTPOnly: true, SameSite: mygo.CookieSameSiteLax})
}

func (m mygoWeb) DeleteCookie(c app.WebCookie) error {
	return mygo.Cookies.Delete(mygo.Cookie{Name: c.Name, Domain: c.Domain, Path: c.Path})
}

func (m mygoWeb) Load(page string) error { return m.view.Page().LoadURL(page) }

// openExternal opens a link of an artifact in the person's browser. It
// never waits on the open: the web view's hooks must return at once.
func openExternal(u string) {
	go func() {
		if err := (frostui.Opener{Schemes: []string{"https", "http"}}).OpenURL(u); err != nil {
			slog.Warn("opening a link of an artifact", "err", err)
		}
	}()
}
