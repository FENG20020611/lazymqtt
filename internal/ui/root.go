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
	keys  keys.Map
	help  help.Model
	theme theme.Palette

	width, height int
	layout        Layout

	focus Focus
	mode  Mode

	topics   panel.Topics
	messages panel.Messages
	detail   panel.Detail
	subs     panel.Subscriptions
	logs     panel.Logs
	prompt   panel.Prompt
	publish  panel.Publish
	brokers  panel.Brokers

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
}

// Options configure the root model.
type Options struct {
	App          *app.App
	Config       config.Config
	InitialTopic []string
	// AutoBroker is connected to at startup, if set.
	AutoBroker string
	Prompt     config.PromptFunc
}

// New builds the root model.
func New(opts Options) Model {
	h := help.New()
	h.Styles = help.DefaultDarkStyles()

	startFocus := FocusTopics
	switch opts.Config.UI.StartPanel {
	case "messages":
		startFocus = FocusMessages
	case "detail":
		startFocus = FocusDetail
	case "subscriptions":
		startFocus = FocusSubs
	}

	return Model{
		app:        opts.App,
		store:      store.New(opts.Config.Limits.Store()),
		cfg:        opts.Config,
		keys:       keys.Default(),
		help:       h,
		theme:      theme.Default,
		focus:      startFocus,
		messages:   panel.NewMessages(),
		logs:       panel.NewLogs(),
		now:        time.Now(),
		autoTopics: opts.InitialTopic,
		autoBroker: opts.AutoBroker,
		promptFn:   opts.Prompt,
		// A sensible default height so the first frame before WindowSizeMsg
		// is not degenerate.
		width:  80,
		height: 24,
	}
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
