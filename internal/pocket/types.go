package pocket

import (
	"encoding/json"
	"time"
)

// Millis is a time as the server sends it: milliseconds since the epoch.
type Millis int64

// Time returns m as a time.Time; the zero time for 0.
func (m Millis) Time() time.Time {
	if m == 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(m))
}

// User is a person of the server.
type User struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Online   bool   `json:"online,omitempty"`
	LastSeen Millis `json:"lastSeen,omitempty"`
}

// Model is one model the server can run.
type Model struct {
	Provider      string   `json:"provider"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ContextWindow int      `json:"contextWindow"`
	Reasoning     bool     `json:"reasoning"`
	Images        bool     `json:"images"`
	Levels        []string `json:"levels"`
}

// ServerInfo is what hello says of the server.
type ServerInfo struct {
	PocketVersion string `json:"pocketVersion"`
	Home          string `json:"home"`
	DefaultCwd    string `json:"defaultCwd"`
	Extensions    []string
	Switches      bool   `json:"switches"`
	Peeks         bool   `json:"peeks"`
	Build         string `json:"build"`
	Now           Millis `json:"now"`
	PiP           bool   `json:"pip"`
	DefaultModel  *struct {
		Provider string `json:"provider"`
		ModelID  string `json:"modelId"`
	} `json:"defaultModel"`
	ExtensionCommands []ExtensionCommand `json:"extensionCommands"`
}

// ExtensionCommand is a slash command a module adds.
type ExtensionCommand struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Args        string `json:"args"`
}

// Hello is the first event of a connection.
type Hello struct {
	// Connection names this event stream, for /api/switch.
	Connection string     `json:"connection"`
	User       User       `json:"user"`
	Users      []User     `json:"users"`
	Models     []Model    `json:"models"`
	Server     ServerInfo `json:"server"`
	Guard      struct {
		Available bool   `json:"available"`
		Enabled   bool   `json:"enabled"`
		Detail    string `json:"detail"`
	} `json:"guard"`
	FolderFavorites []string `json:"folderFavorites"`
}

// Person is someone in a session.
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SessionApproval is the oldest ask waiting in a session or its
// subagents.
type SessionApproval struct {
	ID             string `json:"id"`
	ConversationID int    `json:"conversationId"`
	Tool           string `json:"tool"`
	Subject        string `json:"subject"`
	Reason         string `json:"reason"`
	RequestedBy    string `json:"requestedBy,omitempty"`
	Form           bool   `json:"form,omitempty"`
	At             Millis `json:"at"`
	Count          int    `json:"count"`
}

// SubagentCounts counts a session's subagents by state.
type SubagentCounts struct {
	Working int `json:"working,omitempty"`
	Waiting int `json:"waiting,omitempty"`
	Failed  int `json:"failed,omitempty"`
	Stopped int `json:"stopped,omitempty"`
	Done    int `json:"done,omitempty"`
	Dealt   int `json:"dealt,omitempty"`
}

// Session is one row of the session list.
type Session struct {
	ID        int    `json:"id"`
	Title     string `json:"title,omitempty"`
	Cwd       string `json:"cwd"`
	CreatedAt Millis `json:"createdAt"`
	UpdatedAt Millis `json:"updatedAt"`
	CreatedBy string `json:"createdBy,omitempty"`
	Archived  bool   `json:"archived,omitempty"`
	Worktree  *struct {
		Path   string `json:"path"`
		Branch string `json:"branch"`
		Source string `json:"source"`
	} `json:"worktree,omitempty"`
	LinkedFrom *struct {
		ID int `json:"id"`
	} `json:"linkedFrom,omitempty"`
	Busy       bool             `json:"busy"`
	Waiting    bool             `json:"waiting"`
	Limited    Millis           `json:"limited,omitempty"`
	EndedAt    Millis           `json:"endedAt,omitempty"`
	Failed     bool             `json:"failed,omitempty"`
	Failure    string           `json:"failure,omitempty"`
	Approval   *SessionApproval `json:"approval,omitempty"`
	Model      string           `json:"model,omitempty"`
	People     []Person         `json:"people,omitempty"`
	ChatAt     Millis           `json:"chatAt,omitempty"`
	Subagents  *SubagentCounts  `json:"subagents,omitempty"`
	Background int              `json:"background,omitempty"`
}

// DisplayTitle is the session's title, or what the web app shows for none.
func (s *Session) DisplayTitle() string {
	if s.Title == "" {
		return "New session"
	}
	return s.Title
}

// Working reports whether a run goes on in the session or its subagents.
func (s *Session) Working() bool {
	return s.Busy || (s.Subagents != nil && s.Subagents.Working+s.Subagents.Waiting > 0)
}

// Block is one part of an assistant's message.
type Block struct {
	Type     string         `json:"type"` // text, thinking, toolCall
	Text     string         `json:"text,omitempty"`
	Redacted bool           `json:"redacted,omitempty"`
	Partial  bool           `json:"partial,omitempty"`
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
	// From, in a live delta, is how many characters of the kept block
	// this one's text goes on from.
	From *int `json:"from,omitempty"`
}

// Entry is one entry of a session's transcript. Kind says which fields
// it has: user (Text, From, Images, Files), assistant (Blocks,
// StopReason, Error, Model), toolResult (CallID, Name, Text, IsError,
// Details), compaction (Summary), reset, shell, command, note (Text,
// Name), fromPi (Title, File), other (EntryKind).
type Entry struct {
	ID         int             `json:"id"`
	Kind       string          `json:"kind"`
	Text       string          `json:"text,omitempty"`
	Images     int             `json:"images,omitempty"`
	From       string          `json:"from,omitempty"`
	Files      []string        `json:"files,omitempty"`
	At         Millis          `json:"at,omitempty"`
	Blocks     []Block         `json:"blocks,omitempty"`
	StopReason string          `json:"stopReason,omitempty"`
	Error      string          `json:"error,omitempty"`
	Model      string          `json:"model,omitempty"`
	Provider   string          `json:"provider,omitempty"`
	Partial    bool            `json:"partial,omitempty"`
	CallID     string          `json:"callId,omitempty"`
	Name       string          `json:"name,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
	Clipped    int             `json:"clipped,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	Title      string          `json:"title,omitempty"`
	File       string          `json:"file,omitempty"`
	EntryKind  string          `json:"entryKind,omitempty"`
	// Shell and command entries.
	Command string `json:"command,omitempty"`
	By      string `json:"by,omitempty"`
	Output  string `json:"output,omitempty"`
	Code    *int   `json:"code,omitempty"`
	Status  string `json:"status,omitempty"`
}

// ToolSlot is a tool call at work in the live run.
type ToolSlot struct {
	CallID  string          `json:"callId"`
	Name    string          `json:"name"`
	Status  string          `json:"status"`
	TaskID  int             `json:"taskId,omitempty"`
	Output  string          `json:"output,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}

// LiveMessage is the message being written: whole (Keep nil), or what
// changed since the last (Keep the blocks kept, Blocks what follows).
type LiveMessage struct {
	Keep   *int    `json:"keep,omitempty"`
	Blocks []Block `json:"blocks"`
}

// Limit is a run waiting for a provider's limit to clear.
type Limit struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId,omitempty"`
	Scope    string `json:"scope"`
	Kind     string `json:"kind"`
	Until    Millis `json:"until"`
	Reason   string `json:"reason"`
	Tries    int    `json:"tries"`
	Probing  bool   `json:"probing"`
}

// Live is the run going on now.
type Live struct {
	Busy       bool   `json:"busy"`
	Limit      *Limit `json:"limit,omitempty"`
	Generation *struct {
		Attempt int          `json:"attempt"`
		Message *LiveMessage `json:"message,omitempty"`
		Retry   *struct {
			At    Millis `json:"at"`
			Error string `json:"error"`
		} `json:"retry,omitempty"`
	} `json:"generation,omitempty"`
	Tools       []ToolSlot `json:"tools,omitempty"`
	Compactions []struct {
		Reason   string `json:"reason"`
		Blocking bool   `json:"blocking"`
		Attempt  int    `json:"attempt"`
	} `json:"compactions,omitempty"`
}

// AskChoice is an answer to pick on an ask's card.
type AskChoice struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// AskField is one field of an ask's card.
type AskField struct {
	Name        string      `json:"name"`
	Label       string      `json:"label"`
	Header      string      `json:"header,omitempty"`
	Choices     []AskChoice `json:"choices,omitempty"`
	Multiple    bool        `json:"multiple,omitempty"`
	Other       bool        `json:"other,omitempty"`
	Secret      bool        `json:"secret,omitempty"`
	Placeholder string      `json:"placeholder,omitempty"`
	Multiline   bool        `json:"multiline,omitempty"`
	Value       string      `json:"value,omitempty"`
}

// AskForm is a question Pi or a module asks a person.
type AskForm struct {
	Title  string     `json:"title"`
	Text   string     `json:"text,omitempty"`
	Fields []AskField `json:"fields"`
	Submit string     `json:"submit"`
}

// Approval is a tool call waiting for a person, or a question (Form).
type Approval struct {
	ID             string   `json:"id"`
	ConversationID int      `json:"conversationId"`
	CallID         string   `json:"callId,omitempty"`
	Tool           string   `json:"tool"`
	Subject        string   `json:"subject"`
	Reason         string   `json:"reason"`
	Score          *float64 `json:"score,omitempty"`
	CreatedAt      Millis   `json:"createdAt"`
	RequestedBy    string   `json:"requestedBy,omitempty"`
	Form           *AskForm `json:"form,omitempty"`
}

// Agent is the session's model and folder.
type Agent struct {
	Model *struct {
		Provider string `json:"provider"`
		ModelID  string `json:"modelId"`
	} `json:"model"`
	ThinkingLevel string   `json:"thinkingLevel"`
	Cwd           string   `json:"cwd"`
	Available     bool     `json:"available"`
	ModelName     string   `json:"modelName"`
	ContextWindow int      `json:"contextWindow"`
	Reasoning     bool     `json:"reasoning"`
	Images        bool     `json:"images"`
	Levels        []string `json:"levels"`
}

// Stats are a session's numbers.
type Stats struct {
	Cost          float64 `json:"cost"`
	CacheRate     float64 `json:"cacheRate"`
	ContextTokens int     `json:"contextTokens"`
	TTFTMs        int     `json:"ttftMs"`
	TokensPerSec  float64 `json:"tokensPerSec"`
}

// Conversation is a session's header.
type Conversation struct {
	ID           int    `json:"id"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Cwd          string `json:"cwd"`
	Archived     bool   `json:"archived"`
	InRepository bool   `json:"inRepository"`
	PiP          bool   `json:"pip,omitempty"`
	SubagentName string `json:"subagentName,omitempty"`
	Parent       *struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	} `json:"parent,omitempty"`
	Worktree *struct {
		Branch string `json:"branch"`
		Source string `json:"source"`
	} `json:"worktree,omitempty"`
	Spend struct {
		Spent  float64  `json:"spent"`
		Budget *float64 `json:"budget,omitempty"`
	} `json:"spend"`
}

// Subagent is a subagent run of a session (SubagentView).
type Subagent struct {
	Name           string `json:"name"`
	ConversationID int    `json:"conversationId"`
	Busy           bool   `json:"busy"`
	RunsOn         *struct {
		Name     string `json:"name"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"runsOn,omitempty"`
	Limited    Millis `json:"limited,omitempty"`
	Asked      string `json:"asked,omitempty"`
	AskedAt    Millis `json:"askedAt,omitempty"`
	AnsweredAt Millis `json:"answeredAt,omitempty"`
	Failed     bool   `json:"failed,omitempty"`
	Stopped    bool   `json:"stopped,omitempty"`
	Error      string `json:"error,omitempty"`
	Dealt      bool   `json:"dealt,omitempty"`
	Reporting  bool   `json:"reporting,omitempty"`
}

// QueueRow is a message waiting in a session's queue.
type QueueRow struct {
	ID      int      `json:"id"`
	Mode    string   `json:"mode"`
	By      string   `json:"by,omitempty"`
	Text    string   `json:"text,omitempty"`
	Reports []string `json:"reports,omitempty"`
	Label   string   `json:"label,omitempty"`
}

// Decision is who allowed or denied a tool call.
type Decision struct {
	Allow bool   `json:"allow"`
	By    string `json:"by"`
	At    Millis `json:"at"`
	Ask   bool   `json:"ask,omitempty"`
}

// Plan is plan mode's state.
type Plan struct {
	On bool `json:"on"`
}

// Branch is what a session's folder has checked out.
type Branch struct {
	Branch   string `json:"branch,omitempty"`
	Detached string `json:"detached,omitempty"`
}

// ViewUpdate is a "view" event: a whole view (Full) or what changed.
// Fields left out of an update keep their last value; json.RawMessage
// fields are nil when left out.
type ViewUpdate struct {
	Full         bool         `json:"full"`
	Conversation Conversation `json:"conversation"`
	Entries      []Entry      `json:"entries"`
	Order        []int        `json:"order"`
	OrderFrom    *int         `json:"orderFrom"`
	Live         Live         `json:"live"`
	Agent        *Agent       `json:"agent"`
	Stats        *Stats       `json:"stats"`
	Clients      int          `json:"clients"`
	Viewers      []string     `json:"viewers"`
	Approvals    []Approval   `json:"approvals"`
	// Sent only when they change: nil keeps the last value.
	Subagents *[]Subagent          `json:"subagents"`
	Authors   *map[string]string   `json:"authors"`
	Decisions *map[string]Decision `json:"decisions"`
	Plan      *Plan                `json:"plan"`
	Inbox     *[]QueueRow          `json:"inbox"`
	// Sent when they change, null a real value: a nil RawMessage keeps
	// the last value.
	Branch json.RawMessage `json:"branch"`
	Goal   json.RawMessage `json:"goal"`
}

// Notification is what the server would push to the person
// (embedder-notify).
type Notification struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url"`
	Tag      string `json:"tag"`
	Urgency  string `json:"urgency"`
	Approval *struct {
		ID    string `json:"id"`
		Allow bool   `json:"allow"`
		May   bool   `json:"may"`
		Call  string `json:"call"`
	} `json:"approval,omitempty"`
}

// SubmitResult is what /submit answers.
type SubmitResult struct {
	SubmissionID int    `json:"submissionId"`
	RequestID    string `json:"requestId"`
}
