package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"golang.org/x/term"

	"github.com/Onizuka893/lazymqtt/internal/config"
)

// terminalPrompt reads a password from the terminal with echo disabled. The
// value is held in memory only and never written anywhere.
func terminalPrompt(prompt string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, errors.New("no terminal available to prompt for a password")
	}
	fmt.Fprint(os.Stderr, prompt)
	defer fmt.Fprintln(os.Stderr)
	return term.ReadPassword(fd)
}

// cachedPrompt collects passwords from the terminal *before* the alt screen is
// entered, then replays them for the rest of the session.
//
// This exists because a connect runs inside a tea.Cmd, on its own goroutine,
// while Bubble Tea owns the terminal in raw alt-screen mode. Prompting there
// would write to stderr in the middle of the topic panel and fight Bubble Tea
// for stdin. So the TUI seals the prompt at startup: a cached answer is
// replayed, and anything else fails with an instruction rather than corrupting
// the display.
type cachedPrompt struct {
	mu     sync.Mutex
	sealed bool
	values map[string][]byte
}

func newCachedPrompt() *cachedPrompt {
	return &cachedPrompt{values: map[string][]byte{}}
}

// Func returns the config.PromptFunc to hand to the app.
func (c *cachedPrompt) Func() config.PromptFunc {
	return func(prompt string) ([]byte, error) {
		c.mu.Lock()
		defer c.mu.Unlock()

		if v, ok := c.values[prompt]; ok {
			return v, nil
		}
		if c.sealed {
			return nil, errors.New(
				"this broker needs a password and none is configured.\n" +
					"  The TUI cannot prompt once it owns the terminal. Either:\n" +
					"    - name it with --broker so lazymqtt asks before starting, or\n" +
					"    - set password_cmd (recommended) or password_env on the profile")
		}
		v, err := terminalPrompt(prompt)
		if err != nil {
			return nil, err
		}
		c.values[prompt] = v
		return v, nil
	}
}

// Seal stops further terminal prompting. Called immediately before the Bubble
// Tea program takes over the terminal.
func (c *cachedPrompt) Seal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sealed = true
}

// needsPrompt reports whether a profile will fall through to an interactive
// prompt: it names a user but no credential source.
func needsPrompt(b config.Broker) bool {
	return b.Username != "" && b.Password == "" && b.PasswordEnv == "" && b.PasswordCmd == ""
}

// warmPrompt asks for the password of the broker named on the command line,
// before the TUI starts. A failure here is not fatal: the connect will surface
// it in the UI like any other error.
func warmPrompt(cfg config.Config, brokerRef string, p *cachedPrompt) {
	if brokerRef == "" {
		return
	}
	b, err := cfg.BrokerRef(brokerRef)
	if err != nil || !needsPrompt(b) {
		return
	}
	_, _ = p.Func()(fmt.Sprintf("password for %s@%s: ", b.Username, b.Name))
}
