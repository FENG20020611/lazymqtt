package ui

import (
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/ui/panel"
)

// Update is the single mutation point for the whole application.
//
// Routing order, and the reason for it:
//
//  1. Non-key messages are broadcast — data arrives regardless of focus.
//  2. ctrl+c always quits, whatever is on screen.
//  3. If a mode is active, its overlay consumes the key. This is what stops
//     `q` from quitting while the user is typing a topic filter; getting it
//     wrong is the single most common TUI bug.
//  4. Global normal-mode keys.
//  5. The focused panel.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.resize(msg.Width, msg.Height), nil

	case app.BatchMsg:
		return m.ingest(msg), nil

	case app.ConnStateMsg:
		return m.applyConnState(msg.Status)

	case app.ConnectedMsg:
		if msg.Err != nil {
			return m, app.Toast(app.LevelError, "connect failed: %v", msg.Err)
		}
		m.insecure = m.app.Resolved().Insecure
		// Only a named profile is remembered. A bare URL may carry a host
		// the user typed once and would not expect to reconnect to.
		if name := m.app.Resolved().Name; name != "" {
			if _, ok := m.cfg.Brokers[name]; ok {
				m.persisted.LastBroker = name
			}
		}
		return m, nil

	case app.SubResultMsg:
		return m.applySubResult(msg)

	case app.UnsubResultMsg:
		if msg.Err != nil {
			return m, app.Toast(app.LevelError, "unsubscribe failed: %v", msg.Err)
		}
		for _, f := range msg.Filters {
			m.subscriptions = removeSub(m.subscriptions, f)
		}
		m.subs = m.subs.Move(m.subscriptions, 0)
		return m, app.Toast(app.LevelInfo, "unsubscribed")

	case app.PubResultMsg:
		if msg.Err != nil {
			return m, app.Toast(app.LevelError, "publish failed: %v", msg.Err)
		}
		return m, app.Toast(app.LevelSuccess, "published to %s", msg.Topic)

	case app.FormatDoneMsg:
		if cur := m.messages.Current(m.context()); cur != nil && cur.Seq == msg.Seq {
			m.detail.Pretty, m.detail.PrettySeq = msg.Rendered, msg.Seq
		}
		m.detail.Formatting = false
		return m, nil

	case app.ErrorMsg:
		if msg.Fatal {
			m.quitting = true
			return m, tea.Quit
		}
		return m, app.Toast(app.LevelError, "%v", msg.Err)

	case app.ToastMsg:
		return m.pushToast(msg)

	case app.ToastExpiredMsg:
		m.toasts = dropToast(m.toasts, msg.ID)
		return m, nil

	case app.TickMsg:
		m.now = msg.Time
		if !m.paused {
			m.store.Tick(msg.Time)
		}
		return m, app.TickCmd()

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) resize(w, h int) Model {
	m.width, m.height = w, h
	m.layout = ComputeLayout(w, h, m.insecure)
	_, topicsH := panel.BoxInner(m.layout.LeftW, m.layout.TopicsH)
	_, msgH := panel.BoxInner(m.layout.RightW, m.layout.MessagesH)
	_, detailH := panel.BoxInner(m.layout.RightW, m.layout.DetailH)
	_, subsH := panel.BoxInner(m.layout.LeftW, m.layout.SubsH)
	m.topics = m.topics.SetHeight(topicsH)
	m.messages = m.messages.SetHeight(msgH)
	m.detail = m.detail.SetHeight(detailH)
	m.subs = m.subs.SetHeight(subsH)
	m.logs = m.logs.SetHeight(max(h-4, 1))
	return m
}

// ingest hands a coalesced batch to the store. This is the only place the
// store is written, and it runs on the Bubble Tea goroutine — which is what
// lets the store be entirely lock-free.
func (m Model) ingest(msg app.BatchMsg) Model {
	if msg.Dropped > m.store.Stats().Dropped {
		m.store.AddDropped(msg.Dropped - m.store.Stats().Dropped)
	}

	// While paused, hold the batch back instead of writing it. Every panel
	// reads from the store, so deferring the write is what actually freezes
	// the view — and it leaves navigation fully working, which is the whole
	// point of pausing on a topic publishing at 50 Hz.
	if m.paused {
		return m.buffer(msg.Msgs)
	}

	m.store.Ingest(msg.Msgs)

	for i := range m.subscriptions {
		for _, mm := range msg.Msgs {
			if mqtt.MatchTopic(m.subscriptions[i].Filter, mm.Topic) {
				m.subscriptions[i].Count++
			}
		}
	}

	if m.topics.Follow && len(msg.Msgs) > 0 && !m.paused {
		m.topics = m.topics.FollowTopic(m.store, msg.Msgs[len(msg.Msgs)-1].Topic)
	}
	// The flattened slice shifts as topics appear; re-resolve the cursor from
	// the remembered topic so the selection does not jitter under load.
	m.topics = m.topics.Resync(m.store.Flatten())
	m.messages = m.messages.SetTopic(m.selectedTopic()).Resync(m.context())
	return m
}

// buffer parks messages until the view resumes, dropping the oldest once the
// backlog reaches the configured stream history. A viewer is not an ingestion
// pipeline: what it cannot show, it may discard — as long as it says so.
func (m Model) buffer(msgs []*mqtt.Message) Model {
	if len(msgs) == 0 {
		return m
	}
	limit := m.store.Limits().StreamHistory
	m.pending = append(m.pending, msgs...)
	if over := len(m.pending) - limit; over > 0 {
		m.pending = append(m.pending[:0], m.pending[over:]...)
		m.store.AddDropped(uint64(over))
	}
	return m
}

// resume applies everything held back during the pause, in arrival order.
func (m Model) resume() Model {
	if len(m.pending) > 0 {
		m.store.Ingest(m.pending)
		m.pending = nil
	}
	m.topics = m.topics.Resync(m.store.Flatten())
	m.messages = m.messages.SetTopic(m.selectedTopic()).Resync(m.context())
	return m
}

func (m Model) applyConnState(s mqtt.ConnStatus) (Model, tea.Cmd) {
	prev := m.status.State
	m.status = s
	if s.State == mqtt.StateConnected && prev != mqtt.StateConnected {
		m.insecure = m.app.Resolved().Insecure
		m.layout = ComputeLayout(m.width, m.height, m.insecure)
		return m, app.Toast(app.LevelSuccess, "connected to %s", s.Broker)
	}
	if s.State == mqtt.StateFailed {
		return m, app.Toast(app.LevelError, "connection failed: %v", s.Err)
	}
	return m, nil
}

func (m Model) applySubResult(msg app.SubResultMsg) (Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, s := range msg.Subs {
		m.subscriptions = upsertSub(m.subscriptions, s)
		switch {
		case s.Err != nil:
			cmds = append(cmds, app.Toast(app.LevelError, "subscribe %s failed: %v", s.Filter, s.Err))
		case s.Active && s.GrantedQoS < s.QoS:
			// A silent downgrade is exactly the kind of thing a viewer must
			// make loud.
			cmds = append(cmds, app.Toast(app.LevelWarn,
				"%s granted at QoS %d (requested %d)", s.Filter, s.GrantedQoS, s.QoS))
		}
	}
	if msg.Err != nil && len(msg.Subs) == 0 {
		cmds = append(cmds, app.Toast(app.LevelError, "subscribe failed: %v", msg.Err))
	}
	m.subs = m.subs.Move(m.subscriptions, 0)
	return m, tea.Batch(cmds...)
}

func (m Model) pushToast(msg app.ToastMsg) (Model, tea.Cmd) {
	m.nextToast++
	id := m.nextToast
	m.toasts = append(m.toasts, toast{id: id, text: msg.Text, level: msg.Level})
	if len(m.toasts) > 3 {
		m.toasts = m.toasts[len(m.toasts)-3:]
	}
	ttl := msg.TTL
	if ttl <= 0 {
		ttl = 4 * time.Second
	}
	return m, app.ExpireToastCmd(id, ttl)
}

func (m Model) selectedTopic() string {
	sel := m.topics.Selected()
	if n := m.store.Node(sel); n != nil && n.IsTopic() {
		return sel
	}
	// A structural node — `home`, `devices` — has no messages of its own.
	// Naming it anyway leaves the message list permanently empty while the
	// cursor sits on a branch, so fall back to the firehose instead.
	return ""
}

func upsertSub(subs []mqtt.Subscription, s mqtt.Subscription) []mqtt.Subscription {
	for i := range subs {
		if subs[i].Filter == s.Filter {
			count := subs[i].Count
			subs[i] = s
			subs[i].Count = count
			return subs
		}
	}
	return append(subs, s)
}

func removeSub(subs []mqtt.Subscription, filter string) []mqtt.Subscription {
	out := subs[:0]
	for _, s := range subs {
		if s.Filter != filter {
			out = append(out, s)
		}
	}
	return out
}

func dropToast(ts []toast, id int) []toast {
	out := ts[:0]
	for _, t := range ts {
		if t.id != id {
			out = append(out, t)
		}
	}
	return out
}

// currentMessage returns the message the detail pane should show.
func (m Model) currentMessage() *mqtt.Message {
	ctx := m.context()
	if msg := m.messages.Current(ctx); msg != nil {
		return msg
	}
	if n := m.store.Node(m.topics.Selected()); n != nil {
		return n.Last
	}
	return nil
}

func matchesAny(k tea.KeyPressMsg, bindings ...key.Binding) bool {
	return key.Matches(k, bindings...)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
