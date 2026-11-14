package app

import (
	"fmt"
	"math"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/config"
)

// prefs is the Settings dialog's state: frostui's layout, and the
// choices it shows in words.
type prefs struct {
	layout   frostui.Settings
	finished string
	theme    string
	zoom     string
}

var finishedChoices = []struct{ value, label string }{
	{config.FinishedMine, "Mine and pinned"},
	{config.FinishedAll, "Every session"},
	{config.FinishedNone, "None"},
}

var themeChoices = []struct{ value, label string }{
	{"", "Follow the system"},
	{"light", "Light"},
	{"dark", "Dark"},
}

// openSettings opens the Settings dialog at page (empty for the first).
func (a *App) openSettings(page string) {
	a.prefs.layout.Show(page)
}

// settingsDialog draws the Settings dialog while it is open.
func (a *App) settingsDialog(c *ui.Context) {
	if !a.prefs.layout.Open {
		return
	}
	ns := a.settings.Notes()
	for _, f := range finishedChoices {
		if f.value == ns.Finished {
			a.prefs.finished = f.label
		}
	}
	for _, th := range themeChoices {
		if th.value == a.settings.Theme {
			a.prefs.theme = th.label
		}
	}
	a.prefs.zoom = zoomLabel(a.zoom())
	save := a.saveSettings
	off := !ns.Enabled
	quietOff := off || !ns.Quiet
	a.prefs.layout.Dialog(c, "Settings", []frostui.SettingsPage{
		{ID: "notifications", Title: "Notifications", Icon: "preferences-system-notifications", Keywords: "alerts quiet dnd",
			Groups: []frostui.SettingsGroup{
				{Note: "What Pocket shows of the server's notifications and of the Desk. The server's own settings (Notifications in the web app's menu) decide what it sends at all, and also reach your phone.",
					Settings: []frostui.Setting{
						{Title: "Desktop notifications", Subtitle: "Shown while Pocket's window is not in front", Switch: &ns.Enabled, OnChange: save},
					}},
				{Title: "Tell me about", Settings: []frostui.Setting{
					{Title: "Calls to allow and questions", Subtitle: "A run waits for you until you answer", Switch: &ns.Approvals, OnChange: save, Disabled: off},
					{Title: "Desk decisions", Subtitle: "An agent or the supervisor asks you to decide", Switch: &ns.Decisions, OnChange: save, Disabled: off, Keywords: "asks"},
					{Title: "Finished runs", Subtitle: "Delegated runs finish all day: \"Mine and pinned\" leaves them out", Choice: &a.prefs.finished,
						Options: labels(finishedChoices), Disabled: off, OnChange: func() {
							for _, f := range finishedChoices {
								if f.label == a.prefs.finished {
									ns.Finished = f.value
								}
							}
							save()
						}},
					{Title: "Chat and mentions", Switch: &ns.Chat, OnChange: save, Disabled: off},
					{Title: "Usage limits", Subtitle: "A run waits for a provider's limit to clear", Switch: &ns.Limits, OnChange: save, Disabled: off},
					{Title: "Watched pull requests", Switch: &ns.Watches, OnChange: save, Disabled: off, Keywords: "pr"},
				}},
				{Title: "Calm", Settings: []frostui.Setting{
					{Title: "Gather bursts", Subtitle: "What comes within 10 seconds of a notification shows as one", Switch: &ns.Batch, OnChange: save, Disabled: off, Keywords: "batch group"},
					{Title: "Do not disturb", Subtitle: "Hold notifications until you turn this off (" + keyName("Cmd") + " Shift D)", Switch: &ns.DND, OnChange: func() {
						a.setQuiet(ns.DND, 0)
					}, Disabled: off},
					{Title: "Quiet hours", Subtitle: "Hold notifications every day between these times", Switch: &ns.Quiet, OnChange: save, Disabled: off, Keywords: "night"},
					{Title: "From", Text: &ns.QuietFrom, Placeholder: "22:00", OnChange: save, Disabled: quietOff},
					{Title: "To", Text: &ns.QuietTo, Placeholder: "07:00", OnChange: save, Disabled: quietOff},
					{Title: "Let calls to allow through", Subtitle: "A run that waits for approval still notifies while it is quiet", Switch: &ns.Urgent, OnChange: save, Disabled: off, Keywords: "urgent"},
				}},
				{Title: "Try it", Settings: []frostui.Setting{
					{Title: "Send a test notification", Subtitle: "Arrives at once, whatever the settings above say", Control: func(c *ui.Context) {
						if ui.Button(c, "Send").Clicked() && a.opts.Notify != nil {
							a.opts.Notify.Notify("Pocket", "Notifications work. This is what they look like.", nil)
						}
					}},
				}},
			}},
		{ID: "appearance", Title: "Appearance", Icon: "pocket-sparkle", Keywords: "dark light theme zoom scale font text size",
			Groups: []frostui.SettingsGroup{{Settings: []frostui.Setting{
				{Title: "Theme", Choice: &a.prefs.theme, Options: labels(themeChoices), OnChange: func() {
					for _, th := range themeChoices {
						if th.label == a.prefs.theme {
							a.settings.Theme = th.value
						}
					}
					save()
				}},
				{Title: "Zoom", Subtitle: "Text, icons and spacing together (" + keys("Cmd") + " +, " + keys("Cmd") + " -, " + keys("Cmd") + " 0)",
					Choice: &a.prefs.zoom, Options: zoomLabels(), Keywords: "scale font text size bigger smaller", OnChange: func() {
						for _, z := range zoomLevels {
							if zoomLabel(z) == a.prefs.zoom {
								a.setZoom(z)
							}
						}
					}},
			}}}},
		{ID: "server", Title: "Server", Icon: "network-server", Keywords: "connection sign out account",
			Groups: []frostui.SettingsGroup{{
				Values: a.serverFacts(),
				Settings: []frostui.Setting{
					{Title: "Sign out", Subtitle: "Pocket forgets this device's sign-in; you need a sign-in link to come back", Control: func(c *ui.Context) {
						if ui.Button(c, "Sign Out…").Clicked() {
							a.prefs.layout.Open = false
							a.table.Run("sign-out")
						}
					}},
				}}}},
		a.githubSettingsPage(),
		{ID: "about", Title: "About", Icon: "help-about",
			Groups: []frostui.SettingsGroup{{Values: []frostui.KeyValue{
				{Key: "Pocket", Value: firstNonEmpty(a.opts.Version, "dev")},
				{Key: "Settings", Value: a.opts.Store.Dir(), Mono: true},
				{Key: "Platform", Value: runtime.GOOS + "/" + runtime.GOARCH},
			}}}},
	})
}

func labels(choices []struct{ value, label string }) []string {
	out := make([]string, len(choices))
	for i, ch := range choices {
		out[i] = ch.label
	}
	return out
}

// serverFacts says what Pocket is connected to.
func (a *App) serverFacts() []frostui.KeyValue {
	facts := []frostui.KeyValue{{Key: "Server", Value: a.settings.Server, Mono: true}}
	if a.hello != nil {
		facts = append(facts,
			frostui.KeyValue{Key: "Signed in as", Value: a.hello.User.Name + " (" + a.hello.User.Role + ")"},
			frostui.KeyValue{Key: "Pi Pocket", Value: a.hello.Server.PocketVersion})
	}
	modules := []string{}
	if a.team.on {
		modules = append(modules, "team")
	}
	if a.desk.on {
		modules = append(modules, "desk")
	}
	if len(modules) > 0 {
		facts = append(facts, frostui.KeyValue{Key: "Team modules", Value: strings.Join(modules, ", ")})
	}
	return facts
}

// keyName writes a modifier as the platform names it: Cmd and Option on
// macOS, Ctrl and Alt elsewhere.
func keyName(mod string) string {
	mac := runtime.GOOS == "darwin"
	switch mod {
	case "Cmd", "Ctrl":
		if mac {
			return "⌘"
		}
		return "Ctrl"
	case "Alt":
		if mac {
			return "⌥"
		}
		return "Alt"
	case "Shift":
		if mac {
			return "⇧"
		}
	}
	return mod
}

// keys writes a chord ("Cmd Shift D") as the platform names it.
func keys(chord string) string {
	parts := strings.Fields(chord)
	for i, p := range parts {
		parts[i] = keyName(p)
	}
	return strings.Join(parts, " ")
}

// applyTheme sets the theme the person chose, before the frame adapts
// it; following the system leaves it to frostui.
func (a *App) applyTheme(c *ui.Context) {
	if a.settings.Theme == "" {
		return
	}
	p := c.Preferences()
	p.Accent = ui.Color{}
	c.SetTheme(frostui.FrostyardTheme(a.settings.Theme == "dark").WithPreferences(p))
}

// zoomLevels are the window's zooms, in the steps a browser takes.
var zoomLevels = []float32{0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3}

// zoomIndex is the level nearest to z.
func zoomIndex(z float32) int {
	best := 0
	for i, l := range zoomLevels {
		if math.Abs(float64(l-z)) < math.Abs(float64(zoomLevels[best]-z)) {
			best = i
		}
	}
	return best
}

// zoom is the window's zoom, the level nearest to the saved one; 1 when
// none is saved.
func (a *App) zoom() float32 {
	if a.settings.Zoom <= 0 {
		return 1
	}
	return zoomLevels[zoomIndex(a.settings.Zoom)]
}

// setZoom zooms the window to z and saves it; 1 saves none.
func (a *App) setZoom(z float32) {
	z = zoomLevels[zoomIndex(z)]
	if z == 1 {
		z = 0
	}
	if z == a.settings.Zoom {
		return
	}
	a.settings.Zoom = z
	a.saveSettings()
}

// zoomBy zooms steps levels in (out when negative), staying within the
// levels.
func (a *App) zoomBy(steps int) {
	i := min(max(zoomIndex(a.zoom())+steps, 0), len(zoomLevels)-1)
	a.setZoom(zoomLevels[i])
}

func zoomLabel(z float32) string { return fmt.Sprintf("%d%%", int(math.Round(float64(z)*100))) }

func zoomLabels() []string {
	out := make([]string, len(zoomLevels))
	for i, z := range zoomLevels {
		out[i] = zoomLabel(z)
	}
	return out
}

// zoomCommands zoom the window, with the keys a browser zooms with: Cmd
// = (and Cmd + with Shift), Cmd - and Cmd 0.
func (a *App) zoomCommands() []frostui.Command {
	return []frostui.Command{
		{ID: "zoom-in", Title: "Zoom In", Group: "View", Icon: "zoom-in",
			Bindings: []frostui.KeyBinding{
				{Mods: ui.Cmd, Key: ui.KeyEqual, Scope: frostui.Everywhere},
				{Mods: ui.Cmd | ui.Shift, Key: ui.KeyEqual, Scope: frostui.Everywhere},
			},
			Enabled: func() (bool, string) {
				if a.zoom() >= zoomLevels[len(zoomLevels)-1] {
					return false, "Pocket is at its largest"
				}
				return true, ""
			},
			Run: func() { a.zoomBy(1) }},
		{ID: "zoom-out", Title: "Zoom Out", Group: "View", Icon: "zoom-out",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.KeyMinus, Scope: frostui.Everywhere}},
			Enabled: func() (bool, string) {
				if a.zoom() <= zoomLevels[0] {
					return false, "Pocket is at its smallest"
				}
				return true, ""
			},
			Run: func() { a.zoomBy(-1) }},
		{ID: "zoom-reset", Title: "Actual Size", Group: "View",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.Key0, Scope: frostui.Everywhere}},
			Enabled: func() (bool, string) {
				if a.zoom() == 1 {
					return false, "Pocket is at 100%"
				}
				return true, ""
			},
			Run: func() { a.setZoom(1) }},
	}
}
