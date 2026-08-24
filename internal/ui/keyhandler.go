package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/state"
	"github.com/Onizuka893/lazymqtt/internal/ui/panel"
)

func (m Model) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// ctrl+c always wins, whatever is on screen.
	if matchesAny(k, m.keys.ForceQuit) {
		m.quitting = true
		return m, tea.Quit
	}

	// An active overlay owns the keyboard. Nothing below this point runs
	// while the user is typing.
	if m.mode != ModeNormal {
		return m.handleModeKey(k)
	}
	return m.handleNormalKey(k)
}

func (m Model) handleModeKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeHelp:
		if matchesAny(k, m.keys.Back, m.keys.Help, m.keys.Quit) {
			m.mode = ModeNormal
		}
		return m, nil

	case ModeLogs:
		return m.handleLogsKey(k)

	case ModeBrokers:
		switch {
		case matchesAny(k, m.keys.Cancel):
			m.mode = ModeNormal
		case matchesAny(k, m.keys.Up):
			m.brokers = m.brokers.Move(-1)
		case matchesAny(k, m.keys.Down):
			m.brokers = m.brokers.Move(1)
		case matchesAny(k, m.keys.Confirm):
			name := m.brokers.Selected()
			m.mode = ModeNormal
			if name == "" {
				return m, nil
			}
			b, err := m.cfg.BrokerRef(name)
			if err != nil {
				return m, app.Toast(app.LevelError, "%v", err)
			}
			m.subscriptions = nil
			return m, m.app.ConnectCmd(b, nil, m.promptFn)
		}
		return m, nil

	case ModeConfirm:
		switch k.String() {
		case "y", "Y":
			kind := m.confirmKind
			m.mode, m.confirmKind = ModeNormal, confirmNone
			return m.applyConfirm(kind)
		case "n", "N", "esc":
			m.mode, m.confirmKind = ModeNormal, confirmNone
		}
		return m, nil

	case ModePublish:
		switch {
		case matchesAny(k, m.keys.Cancel):
			m.mode = ModeNormal
			return m, nil
		case k.String() == "ctrl+s" || (k.String() == "enter" && !m.publish.PayloadFocused()):
			if err := m.publish.Validate(); err != nil {
				return m.setPublish(m.publish.SetError(err)), nil
			}
			req := m.publish.Request()
			m.mode = ModeNormal
			m.persisted = m.persisted.RememberPublish(state.Publish{
				Topic:   req.Topic,
				Payload: string(req.Payload),
				QoS:     req.QoS,
				Retain:  req.Retain,
			})
			return m, m.app.PublishCmd(req)
		}
		pub, cmd := m.publish.Update(k)
		return m.setPublish(pub), cmd

	case ModePrompt:
		switch {
		case matchesAny(k, m.keys.Cancel):
			return m.cancelPrompt()
		case matchesAny(k, m.keys.Confirm):
			return m.submitPrompt()
		}
		prompt, cmd := m.prompt.Update(k)
		m = m.setPrompt(prompt)
		if m.prompt.Live {
			m = m.applyFilter(m.prompt.Value())
		}
		return m, cmd
	}
	return m, nil
}

func (m Model) handleLogsKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total := m.app.Logger.Ring.Len()
	switch {
	case matchesAny(k, m.keys.Back, m.keys.Logs, m.keys.Quit):
		m.mode = ModeNormal
	case matchesAny(k, m.keys.Down):
		m.logs = m.logs.Scroll(total, 1)
	case matchesAny(k, m.keys.Up):
		m.logs = m.logs.Scroll(total, -1)
	case matchesAny(k, m.keys.HalfDown):
		m.logs = m.logs.Scroll(total, max(m.height/2, 1))
	case matchesAny(k, m.keys.HalfUp):
		m.logs = m.logs.Scroll(total, -max(m.height/2, 1))
	case matchesAny(k, m.keys.Bottom):
		m.logs = m.logs.Bottom(total)
	case matchesAny(k, m.keys.Top):
		m.logs = m.logs.Scroll(total, -total)
	}
	return m, nil
}

func (m Model) handleNormalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case matchesAny(k, m.keys.Quit):
		m.quitting = true
		return m, tea.Quit

	case matchesAny(k, m.keys.Help):
		m.mode = ModeHelp
		return m, nil

	case matchesAny(k, m.keys.Logs):
		m.mode = ModeLogs
		m.logs = m.logs.Bottom(m.app.Logger.Ring.Len())
		return m, nil

	case matchesAny(k, m.keys.Back):
		if m.filter != "" {
			return m.applyFilter(""), app.Toast(app.LevelInfo, "filter cleared")
		}
		return m, nil

	case matchesAny(k, m.keys.NextPanel):
		m.focus = (m.focus + 1) % focusCount
		return m, nil
	case matchesAny(k, m.keys.PrevPanel):
		m.focus = (m.focus + focusCount - 1) % focusCount
		return m, nil
	case matchesAny(k, m.keys.Panel1):
		m.focus = FocusTopics
		return m, nil
	case matchesAny(k, m.keys.Panel2):
		m.focus = FocusMessages
		return m, nil
	case matchesAny(k, m.keys.Panel3):
		m.focus = FocusDetail
		return m, nil
	case matchesAny(k, m.keys.Panel4):
		m.focus = FocusSubs
		return m, nil

	case matchesAny(k, m.keys.Pause):
		m.paused = !m.paused
		if m.paused {
			return m, app.Toast(app.LevelWarn, "view paused — the connection stays up")
		}
		held := len(m.pending)
		m = m.resume()
		if held > 0 {
			return m, app.Toast(app.LevelInfo, "view resumed — applied %d buffered message(s)", held)
		}
		return m, app.Toast(app.LevelInfo, "view resumed")

	case matchesAny(k, m.keys.Follow):
		m.topics.Follow = !m.topics.Follow
		return m, app.Toast(app.LevelInfo, "follow %s", onOff(m.topics.Follow))

	case matchesAny(k, m.keys.Autoscroll):
		m.messages.Autoscroll = !m.messages.Autoscroll
		return m, app.Toast(app.LevelInfo, "autoscroll %s", onOff(m.messages.Autoscroll))

	case matchesAny(k, m.keys.Wrap):
		m.detail.Wrap = !m.detail.Wrap
		return m, app.Toast(app.LevelInfo, "wrap %s", onOff(m.detail.Wrap))

	case matchesAny(k, m.keys.Retained):
		f := m.store.Filter()
		f.RetainedOnly = !f.RetainedOnly
		m.store.SetFilter(f)
		m.topics = m.topics.Resync(m.store.Flatten())
		return m, app.Toast(app.LevelInfo, "retained-only %s", onOff(f.RetainedOnly))

	case matchesAny(k, m.keys.Filter):
		m.mode = ModePrompt
		return m.setPrompt(panel.NewPrompt(panel.PromptFilter, "filter:", m.filter, true)), nil

	case matchesAny(k, m.keys.Subscribe):
		m.mode = ModePrompt
		return m.setPrompt(panel.NewPrompt(
			panel.PromptSubscribe, "subscribe:", suggestFilter(m.topics.Selected()), false)), nil

	case matchesAny(k, m.keys.Unsubscribe):
		return m.unsubscribeSelected()

	case matchesAny(k, m.keys.Publish):
		m.mode = ModePublish
		_, h := panel.BoxInner(m.width*2/3, m.height/2)
		topic := m.topics.Selected()
		pub := panel.NewPublish(topic, m.width*2/3, max(h-8, 3))
		if p, ok := m.persisted.RecentFor(topic); ok {
			pub = pub.Seed(p.Payload, p.QoS, p.Retain)
		}
		return m.setPublish(pub), nil

	case matchesAny(k, m.keys.Connect):
		if m.brokers = panel.NewBrokers(m.cfg, m.app.Resolved().Name); m.brokers.Empty() {
			return m, app.Toast(app.LevelWarn, "no broker profiles — run `lazymqtt config init`")
		}
		m.mode = ModeBrokers
		return m, nil

	case matchesAny(k, m.keys.Reconnect):
		if cmd := m.app.ReconnectCmd(); cmd != nil {
			return m, tea.Batch(cmd, app.Toast(app.LevelInfo, "reconnecting…"))
		}
		return m, app.Toast(app.LevelWarn, "no broker to reconnect to")

	case matchesAny(k, m.keys.CopyPayload):
		return m.copyPayload()
	case matchesAny(k, m.keys.CopyTopic):
		return m.copyTopic()

	case matchesAny(k, m.keys.Format):
		msg := m.currentMessage()
		if msg == nil {
			return m, nil
		}
		m.detail.Formatting = true
		return m, app.FormatCmd(msg)

	case matchesAny(k, m.keys.ClearTopic):
		topic := m.topics.Selected()
		if topic == "" {
			return m, nil
		}
		m.store.ClearTopic(topic)
		return m, app.Toast(app.LevelInfo, "cleared %s", topic)

	case matchesAny(k, m.keys.ClearAll):
		m.mode, m.confirmKind = ModeConfirm, confirmClearAll
		m.confirmText = "Clear every retained message and topic from the view?"
		return m, nil

	case matchesAny(k, m.keys.NextMatch):
		return m.jumpMatch(1), nil
	case matchesAny(k, m.keys.PrevMatch):
		return m.jumpMatch(-1), nil
	}

	return m.handlePanelKey(k)
}

func (m Model) handlePanelKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	ctx := m.context()
	switch m.focus {
	case FocusTopics:
		flat := m.store.Flatten()
		switch {
		case matchesAny(k, m.keys.Down):
			m.topics = m.topics.Move(flat, 1)
		case matchesAny(k, m.keys.Up):
			m.topics = m.topics.Move(flat, -1)
		case matchesAny(k, m.keys.HalfDown):
			m.topics = m.topics.Move(flat, max(m.layout.TopicsH/2, 1))
		case matchesAny(k, m.keys.HalfUp):
			m.topics = m.topics.Move(flat, -max(m.layout.TopicsH/2, 1))
		case matchesAny(k, m.keys.Top):
			m.topics = m.topics.GoTo(flat, 0)
		case matchesAny(k, m.keys.Bottom):
			m.topics = m.topics.GoTo(flat, len(flat)-1)
		case matchesAny(k, m.keys.Right):
			m.topics = m.topics.Expand(m.store)
		case matchesAny(k, m.keys.Left):
			m.topics = m.topics.Collapse(m.store)
		case matchesAny(k, m.keys.Select):
			m.topics = m.topics.Toggle(m.store)
		default:
			return m, nil
		}
		m.messages = m.messages.SetTopic(m.selectedTopic())
		m.detail = m.detail.Reset()
		return m, nil

	case FocusMessages:
		switch {
		case matchesAny(k, m.keys.Down):
			m.messages = m.messages.Move(ctx, 1)
		case matchesAny(k, m.keys.Up):
			m.messages = m.messages.Move(ctx, -1)
		case matchesAny(k, m.keys.HalfDown):
			m.messages = m.messages.Move(ctx, max(m.layout.MessagesH/2, 1))
		case matchesAny(k, m.keys.HalfUp):
			m.messages = m.messages.Move(ctx, -max(m.layout.MessagesH/2, 1))
		case matchesAny(k, m.keys.Top):
			m.messages = m.messages.Top(ctx)
		case matchesAny(k, m.keys.Bottom):
			m.messages = m.messages.Bottom(ctx)
		default:
			return m, nil
		}
		m.detail = m.detail.Reset()
		return m, nil

	case FocusDetail:
		switch {
		case matchesAny(k, m.keys.Down):
			m.detail = m.detail.Scroll(1)
		case matchesAny(k, m.keys.Up):
			m.detail = m.detail.Scroll(-1)
		case matchesAny(k, m.keys.HalfDown):
			m.detail = m.detail.Scroll(max(m.layout.DetailH/2, 1))
		case matchesAny(k, m.keys.HalfUp):
			m.detail = m.detail.Scroll(-max(m.layout.DetailH/2, 1))
		case matchesAny(k, m.keys.Top):
			m.detail = m.detail.Reset()
		}
		return m, nil

	case FocusSubs:
		switch {
		case matchesAny(k, m.keys.Down):
			m.subs = m.subs.Move(m.subscriptions, 1)
		case matchesAny(k, m.keys.Up):
			m.subs = m.subs.Move(m.subscriptions, -1)
		}
		return m, nil
	}
	return m, nil
}

func (m Model) applyConfirm(kind confirmKind) (tea.Model, tea.Cmd) {
	switch kind {
	case confirmClearAll:
		m.store.ClearAll()
		m.topics = m.topics.Resync(m.store.Flatten())
		m.messages = m.messages.SetTopic("")
		return m, app.Toast(app.LevelInfo, "cleared all topics")
	case confirmQuit:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) cancelPrompt() (tea.Model, tea.Cmd) {
	live := m.prompt.Live
	m.mode = ModeNormal
	if live {
		// An incremental filter is applied as it is typed, so cancelling has
		// to undo it.
		m = m.applyFilter(m.filter)
	}
	return m, nil
}

func (m Model) submitPrompt() (tea.Model, tea.Cmd) {
	value := m.prompt.Value()
	switch m.prompt.Kind {
	case panel.PromptSubscribe:
		if err := mqtt.ValidateFilter(value); err != nil {
			// Inline, and no MQTT call is made.
			return m.setPrompt(m.prompt.SetError(err)), nil
		}
		m.mode = ModeNormal
		m.subscriptions = upsertSub(m.subscriptions, mqtt.Subscription{Filter: value})
		return m, m.app.SubscribeCmd(value, 0)

	case panel.PromptFilter:
		m.mode = ModeNormal
		m.filter = value
		return m.applyFilter(value), nil
	}
	m.mode = ModeNormal
	return m, nil
}

func (m Model) applyFilter(text string) Model {
	m.filter = text
	f := m.store.Filter()
	f.Text = text
	f.Payload = true
	m.store.SetFilter(f)
	m.topics = m.topics.Resync(m.store.Flatten())
	m.messages = m.messages.SetTopic(m.selectedTopic())
	return m
}

// jumpMatch moves the tree cursor to the next node matching the filter.
func (m Model) jumpMatch(dir int) Model {
	flat := m.store.Flatten()
	if len(flat) == 0 || m.filter == "" {
		return m
	}
	start := m.topics.Cursor()
	for i := 1; i <= len(flat); i++ {
		idx := (start + dir*i + len(flat)*len(flat)) % len(flat)
		if m.store.Filter().MatchNode(flat[idx]) {
			m.topics = m.topics.GoTo(flat, idx)
			m.messages = m.messages.SetTopic(m.selectedTopic())
			return m
		}
	}
	return m
}

func (m Model) unsubscribeSelected() (tea.Model, tea.Cmd) {
	s := m.subs.Selected(m.subscriptions)
	if s == nil {
		return m, app.Toast(app.LevelWarn, "no subscription selected")
	}
	return m, m.app.UnsubscribeCmd(s.Filter)
}

// copyPayload writes the RAW payload — not the sanitised rendering — to the
// system clipboard over OSC52, which works over SSH with no helper binary.
func (m Model) copyPayload() (tea.Model, tea.Cmd) {
	msg := m.currentMessage()
	if msg == nil {
		return m, app.Toast(app.LevelWarn, "nothing to copy")
	}
	return m, tea.Batch(
		tea.SetClipboard(string(msg.Payload)),
		app.Toast(app.LevelSuccess, "payload copied (%d bytes)", len(msg.Payload)),
	)
}

func (m Model) copyTopic() (tea.Model, tea.Cmd) {
	topic := m.topics.Selected()
	if topic == "" {
		return m, app.Toast(app.LevelWarn, "nothing to copy")
	}
	return m, tea.Batch(
		tea.SetClipboard(topic),
		app.Toast(app.LevelSuccess, "topic copied"),
	)
}

// suggestFilter turns the selected topic into a plausible subscription.
func suggestFilter(topic string) string {
	if topic == "" {
		return "#"
	}
	return topic + "/#"
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
