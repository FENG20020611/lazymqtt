package panel

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// publishField identifies the focused control in the publish modal.
type publishField int

const (
	fieldTopic publishField = iota
	fieldPayload
	fieldQoS
	fieldRetain
	publishFieldCount
)

// Publish is the publish modal: topic, payload, QoS and retain.
type Publish struct {
	topic   textinput.Model
	payload textarea.Model
	qos     byte
	retain  bool
	field   publishField
	Err     error
}

// NewPublish returns a publish modal with the topic prefilled from the
// current tree selection.
func NewPublish(topic string, w, h int) Publish {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(topic)
	ti.CursorEnd()
	ti.CharLimit = 512
	ti.SetWidth(max(w-12, 10))
	ti.SetVirtualCursor(true)
	ti.Focus()

	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetWidth(max(w-4, 10))
	ta.SetHeight(max(h, 3))
	ta.SetVirtualCursor(true)

	return Publish{topic: ti, payload: ta, field: fieldTopic}
}

// Update routes a key to the focused control, or handles field navigation.
//
// While this modal is open it owns the keyboard: `q` inside the payload
// inserts a literal q rather than quitting the app.
func (p Publish) Update(msg tea.Msg) (Publish, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "tab":
			return p.focus((p.field + 1) % publishFieldCount), nil
		case "shift+tab":
			return p.focus((p.field + publishFieldCount - 1) % publishFieldCount), nil
		}
		switch p.field {
		case fieldQoS:
			switch k.String() {
			case "0", "1", "2":
				p.qos = k.String()[0] - '0'
				return p, nil
			case "left", "h":
				if p.qos > 0 {
					p.qos--
				}
				return p, nil
			case "right", "l":
				if p.qos < 2 {
					p.qos++
				}
				return p, nil
			}
		case fieldRetain:
			switch k.String() {
			case "space", " ", "left", "right", "h", "l":
				p.retain = !p.retain
				return p, nil
			}
		}
	}

	var cmd tea.Cmd
	switch p.field {
	case fieldTopic:
		p.topic, cmd = p.topic.Update(msg)
	case fieldPayload:
		p.payload, cmd = p.payload.Update(msg)
	}
	return p, cmd
}

func (p Publish) focus(f publishField) Publish {
	p.field = f
	p.topic.Blur()
	p.payload.Blur()
	switch f {
	case fieldTopic:
		p.topic.Focus()
	case fieldPayload:
		p.payload.Focus()
	}
	return p
}

// PayloadFocused reports whether the multi-line payload field has focus, so
// the root model knows whether Enter should insert a newline or publish.
func (p Publish) PayloadFocused() bool { return p.field == fieldPayload }

// Request builds the PublishRequest this modal describes.
func (p Publish) Request() mqtt.PublishRequest {
	return mqtt.PublishRequest{
		Topic:   strings.TrimSpace(p.topic.Value()),
		Payload: []byte(p.payload.Value()),
		QoS:     p.qos,
		Retain:  p.retain,
	}
}

// Validate checks the topic before anything is sent.
func (p Publish) Validate() error { return mqtt.ValidateTopic(strings.TrimSpace(p.topic.Value())) }

// SetError attaches an inline error.
func (p Publish) SetError(err error) Publish {
	p.Err = err
	return p
}

// View renders the modal body.
func (p Publish) View(ctx Context, w int) string {
	t := ctx.Theme
	sel := func(f publishField, s string) string {
		if p.field == f {
			return t.Accent.Render("▸ " + s)
		}
		return t.Dim.Render("  " + s)
	}

	var b strings.Builder
	b.WriteString(sel(fieldTopic, "topic") + "\n  " + p.topic.View() + "\n")
	b.WriteString(sel(fieldPayload, "payload") + "\n" + p.payload.View() + "\n")

	qos := ""
	for i := byte(0); i <= 2; i++ {
		mark := " "
		if i == p.qos {
			mark = "●"
		}
		qos += fmt.Sprintf(" %s%d", mark, i)
	}
	b.WriteString(sel(fieldQoS, "qos") + "  " + t.Value.Render(qos) + "\n")

	check := "[ ]"
	if p.retain {
		check = "[✓]"
	}
	b.WriteString(sel(fieldRetain, "retain") + "  " + t.Value.Render(check) + "\n")

	if p.Err != nil {
		b.WriteString(t.Error.Render("✗ "+p.Err.Error()) + "\n")
	}
	b.WriteString(t.Dim.Render("⇥ field  ↵ publish  esc cancel"))
	return b.String()
}

// Cursor exposes the focused control's cursor.
func (p Publish) Cursor() *tea.Cursor {
	if p.field == fieldTopic {
		return p.topic.Cursor()
	}
	return nil
}
