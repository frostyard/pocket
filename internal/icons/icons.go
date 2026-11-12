// Package icons holds the icons Pocket draws that neither the desktop's
// theme nor frostui's set names. They follow Lucide's look (24 DIP grid,
// 2 DIP strokes, round ends) and are drawn here, not copied.
package icons

const open = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">`

func svg(body string) []byte { return []byte(open + body + `</svg>`) }

// Embedded returns the icons by name, for frostui.NewIcons.
func Embedded() map[string][]byte {
	return map[string][]byte{
		// A speech bubble: a session.
		"pocket-session": svg(`<path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z"/>`),
		// A sparkle: Pi, the assistant.
		"pocket-sparkle": svg(`<path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/><path d="M19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z"/>`),
		// A robot face: a named agent.
		"pocket-agent": svg(`<rect x="4" y="8" width="16" height="12" rx="3"/><path d="M12 8V4M9 13v1M15 13v1M10 17h4"/><circle cx="12" cy="3" r="1"/>`),
		// A raised hand: waiting for you.
		"pocket-waiting": svg(`<path d="M18 11V6a2 2 0 0 0-4 0v5M14 10V4a2 2 0 0 0-4 0v6M10 10.5V6a2 2 0 0 0-4 0v8"/><path d="M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.9-6-2.4l-3.6-3.6a2 2 0 0 1 2.8-2.8L7 15"/>`),
		// A shield with a check: an approval.
		"pocket-approval": svg(`<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><path d="M9 12l2 2 4-4"/>`),
		// A question in a circle: Pi asks.
		"pocket-question": svg(`<circle cx="12" cy="12" r="10"/><path d="M9.1 9a3 3 0 0 1 5.8 1c0 2-3 3-3 3M12 17h.01"/>`),
		// A wrench: a tool call.
		"pocket-tool": svg(`<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.8-3.8a6 6 0 0 1-7.9 7.9l-6.9 6.9a2.1 2.1 0 0 1-3-3l6.9-6.9a6 6 0 0 1 7.9-7.9z"/>`),
		// A branch: a git branch or worktree.
		"pocket-branch": svg(`<circle cx="6" cy="6" r="2"/><circle cx="6" cy="18" r="2"/><circle cx="18" cy="8" r="2"/><path d="M6 8v8M18 10a6 6 0 0 1-6 6H8"/>`),
		// A brain: thinking.
		"pocket-thinking": svg(`<path d="M12 5a3 3 0 0 0-5.9-.8A3 3 0 0 0 4 9a3 3 0 0 0 0 5 3 3 0 0 0 2 4.8A3 3 0 0 0 12 19z"/><path d="M12 5a3 3 0 0 1 5.9-.8A3 3 0 0 1 20 9a3 3 0 0 1 0 5 3 3 0 0 1-2 4.8A3 3 0 0 1 12 19z"/>`),
		// Stacked squares: subagents.
		"pocket-subagents": svg(`<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M4 16a2 2 0 0 1-1-1.7V5a2 2 0 0 1 2-2h9.3a2 2 0 0 1 1.7 1"/>`),
		// A coin: spend.
		"pocket-spend": svg(`<circle cx="12" cy="12" r="9"/><path d="M14.5 9.5a2.5 2 0 0 0-2.5-1.5c-1.4 0-2.5.8-2.5 2s1.1 1.6 2.5 2 2.5.8 2.5 2-1.1 2-2.5 2a2.5 2 0 0 1-2.5-1.5M12 6.5v1.5M12 16v1.5"/>`),
		// An arrow up in a circle: send.
		"pocket-send": svg(`<circle cx="12" cy="12" r="10"/><path d="M12 16V8M8 12l4-4 4 4"/>`),
		// A filled square: stop.
		"pocket-stop": svg(`<circle cx="12" cy="12" r="10"/><rect x="9" y="9" width="6" height="6" rx="1" fill="currentColor"/>`),
		// A file with lines changed: a diff.
		"pocket-diff": svg(`<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M12 11v6M9 14h6"/>`),
		// A pin.
		"pocket-pin": svg(`<path d="M12 17v5M9 10.8a2 2 0 0 1-1.1 1.8l-1.8.9A2 2 0 0 0 5 15.2V17h14v-1.8a2 2 0 0 0-1.1-1.8l-1.8-.9a2 2 0 0 1-1.1-1.8V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"/>`),
		// A terminal prompt.
		"pocket-terminal": svg(`<path d="M4 17l6-6-6-6M12 19h8"/>`),
		// A plus in a bubble: a new session.
		"pocket-new": svg(`<path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z"/><path d="M12 8v8M8 12h8"/>`),
		// A plug: the connection.
		"pocket-plug": svg(`<path d="M12 22v-5M9 8V2M15 8V2M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8z"/>`),
		// A moon: the night shift.
		"pocket-night": svg(`<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9z"/>`),
		// A desk lamp: the desk.
		"pocket-desk": svg(`<path d="M3 21h18M12 21v-6M8 15h8M9 4l6 3-3 6-6-3zM15 7l3-1"/>`),
		// An eye: viewing.
		"pocket-eye": svg(`<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z"/><circle cx="12" cy="12" r="3"/>`),
	}
}
