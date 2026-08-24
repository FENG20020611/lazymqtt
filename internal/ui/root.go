// Package ui is the Bubble Tea program: layout, focus, mode routing and
// rendering. It is the only package that talks to the terminal.
package ui

import (
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/state"
	"github.com/Onizuka893/lazymqtt/internal/store"
	"github.com/Onizuka893/lazymqtt/internal/ui/keys"
	"github.com/Onizuka893/lazymqtt/internal/ui/panel"
	"github.com/Onizuka893/lazymqtt/internal/ui/theme"
)

// Focus identifies the active panel.
type Focus int

// Panels, in Tab order.
const (
	FocusTopics Focus = iota
	FocusMessages
	FocusDetail
	FocusSubs
	focusCount
)

func (f Focus) String() string {
	switch f {
	case FocusTopics:
		return "Topics"
	case FocusMessages:
		return "Messages"
	case FocusDetail:
		return "Detail"
	case FocusSubs:
		return "Subscriptions"
	}
	return ""
}

// Mode is the input mode. Anything other than ModeNormal means an overlay
// owns the keyboard.
type Mode int

// Input modes.
const (
	ModeNormal Mode = iota
	ModePrompt
	ModePublish
	ModeConfirm
	ModeHelp
	ModeBrokers
	ModeLogs
)

type toast struct {
	id    int
	text  string
	level app.Level
}

type confirmKind int

const (
	confirmNone confirmKind = iota
	confirmClearAll
	confirmQuit
)

// Model is the root Bubble Tea model.
//
// Every field is a value except store, which is an explicit pointer: it is
// mutable shared state with exactly one writer, and pretending otherwise by
// copying a struct full of maps would only make ownership ambiguous.
type Model struct {
	app   *app.App
	store *store.Store
	cfg   config.Config
	help  *help.Model
	theme *theme.Palette

	// keys is a pointer for the same reason as prompt and publish below: the
	// binding table is 2.6 KB of help strings that never change after New.
	keys *keys.Map

	width, height int
	layout        Layout

	focus Focus
	mode  Mode

	topics   panel.Topics
	messages panel.Messages
	detail   panel.Detail
	subs     panel.Subscriptions
	logs     panel.Logs
	brokers  panel.Brokers

	// prompt and publish are behind pointers because they embed bubbles'
	// textinput and textarea, which are 8 KB and 23 KB respectively. This
	// struct is copied on every Update and boxed into a tea.Model on the way
	// out, so an embedded textarea costs 46 KB of garbage per keystroke —
	// §21 pitfall 15, and modelsize_test.go is the guard against it
	// reappearing. Both are non-nil for the life of the model.
	//
	// The rule of thumb: read-only for the model's lifetime, or bigger than a
	// few hundred bytes, means a pointer.
	prompt  *panel.Prompt
	publish *panel.Publish

	status        mqtt.ConnStatus
	subscriptions []mqtt.Subscription

	paused   bool
	insecure bool

	// pending holds messages that arrived while the view was paused. They are
	// applied to the store on resume. Bounded, so a pause left running
	// overnight cannot grow without limit.
	pending []*mqtt.Message
	filter  string

	confirmKind confirmKind
	confirmText string

	toasts     []toast
	nextToast  int
	now        time.Time
	quitting   bool
	autoTopics []string
	autoBroker string
	promptFn   config.PromptFunc

	// persisted carries the previous session's preferences forward and
	// accumulates this session's. It is written out once, after the program
	// has exited and the terminal has been restored.
	persisted state.State
}

// Options configure the root model.
type Options struct {
	App          *app.App
	Config       config.Config
	InitialTopic []string
	// AutoBroker is connected to at startup, if set.
	AutoBroker string
	Prompt     config.PromptFunc
	// State is the previous session's persisted preferences. The zero value
	// is a first run.
	State state.State
}

// New builds the root model.
func New(opts Options) Model {
	h := help.New()
	h.Styles = help.DefaultDarkStyles()
	// help.Model carries its own style set, another 4.6 KB that would
	// otherwise be copied on every Update.
	helpModel := &h

	startFocus := FocusTopics
	switch opts.Config.UI.StartPanel {
	case "messages":
		startFocus = FocusMessages
	case "detail":
		startFocus = FocusDetail
	case "subscriptions":
		startFocus = FocusSubs
	}

	// An explicit --broker always wins; the remembered profile is only a
	// convenience for starting with no arguments at all.
	autoBroker := opts.AutoBroker
	if autoBroker == "" && opts.State.LastBroker != "" {
		if _, ok := opts.Config.Brokers[opts.State.LastBroker]; ok {
			autoBroker = opts.State.LastBroker
		}
	}

	km := keys.Default()

	m := Model{
		app:        opts.App,
		store:      store.New(opts.Config.Limits.Store()),
		cfg:        opts.Config,
		keys:       &km,
		help:       helpModel,
		theme:      theme.Default,
		focus:      startFocus,
		messages:   panel.NewMessages(),
		logs:       panel.NewLogs(),
		prompt:     &panel.Prompt{},
		publish:    &panel.Publish{},
		now:        time.Now(),
		autoTopics: opts.InitialTopic,
		autoBroker: autoBroker,
		promptFn:   opts.Prompt,
		persisted:  opts.State,
		// A sensible default height so the first frame before WindowSizeMsg
		// is not degenerate.
		width:  80,
		height: 24,
	}
	m.store.RestoreExpanded(opts.State.Expanded)
	return m
}

// StateSnapshot captures what should survive this session. It reads the store,
// so it runs on the Bubble Tea goroutine or after the program has stopped —
// never from a tea.Cmd.
func (m Model) StateSnapshot() state.State {
	s := m.persisted
	s.Expanded = m.store.ExpandedPaths(state.MaxExpandedNodes)
	return s.Sanitize()
}

// Store exposes the store for tests.
func (m Model) Store() *store.Store { return m.store }

// Focused returns the active panel, for tests.
func (m Model) Focused() Focus { return m.focus }

// Mode returns the input mode, for tests.
func (m Model) Mode() Mode { return m.mode }

// Paused reports whether the view is frozen.
func (m Model) Paused() bool { return m.paused }

// Buffered reports how many messages are waiting for the view to resume.
func (m Model) Buffered() int { return len(m.pending) }

// Init starts the clock and, when a broker was named, the connection.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{app.TickCmd()}
	if m.autoBroker != "" {
		if b, err := m.cfg.BrokerRef(m.autoBroker); err == nil {
			cmds = append(cmds, m.app.ConnectCmd(b, m.autoTopics, m.promptFn))
		} else {
			cmds = append(cmds, app.Toast(app.LevelError, "%v", err))
		}
	}
	return tea.Batch(cmds...)
}

// setPrompt and setPublish keep the panels' value semantics at the call sites
// while the model holds them behind a pointer.
func (m Model) setPrompt(p panel.Prompt) Model {
	m.prompt = &p
	return m
}

func (m Model) setPublish(p panel.Publish) Model {
	m.publish = &p
	return m
}

func (m Model) context() panel.Context {
	return panel.Context{
		Store:         m.store,
		Theme:         m.theme,
		Keys:          m.keys,
		Config:        m.cfg,
		Paused:        m.paused,
		SelectedTopic: m.topics.Selected(),
	}
}
