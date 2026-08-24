package panel

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// PromptKind says what a prompt is collecting, so the root model knows what
// to do with the value.
type PromptKind int

const (
	PromptNone PromptKind = iota
	PromptSubscribe
	PromptFilter
	PromptPassword
)

// Prompt is a single-line input used for subscribing and filtering.
type Prompt struct {
	Kind  PromptKind
	Label string
	input textinput.Model
	Err   error
	// Live makes the root apply the value on every keystroke, which is what
	// an incremental filter needs.
	Live bool
}

// NewPrompt returns a prompt ready to receive keys.
func NewPrompt(kind PromptKind, label, initial string, live bool) Prompt {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.CharLimit = 512
	if kind == PromptPassword {
		ti.EchoMode = textinput.EchoPassword
	}
	ti.SetVirtualCursor(true)
	ti.Focus()
	return Prompt{Kind: kind, Label: label, input: ti, Live: live}
}

// Update forwards a message to the text input. While a prompt is open it
// consumes every key, which is what stops `q` from quitting the app while the
// user is typing a topic filter containing a q.
func (p Prompt) Update(msg tea.Msg) (Prompt, tea.Cmd) {
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return p, cmd
}

// Value returns the current text.
func (p Prompt) Value() string { return p.input.Value() }

// SetError attaches an inline validation message.
func (p Prompt) SetError(err error) Prompt {
	p.Err = err
	return p
}

// View renders the prompt as a single line plus an optional error.
func (p Prompt) View(ctx Context, w int) string {
	t := ctx.Theme
	line := t.Accent.Render(p.Label) + " " + p.input.View()
	if p.Err != nil {
		line += "  " + t.Error.Render("✗ "+p.Err.Error())
	}
	return Truncate(line, w)
}

// Cursor exposes the input's cursor so the root view can position the real
// terminal cursor over it.
func (p Prompt) Cursor() *tea.Cursor { return p.input.Cursor() }
