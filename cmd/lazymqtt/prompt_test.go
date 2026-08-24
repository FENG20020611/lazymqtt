package main

import (
	"strings"
	"testing"

	"github.com/Onizuka893/lazymqtt/internal/config"
)

func TestNeedsPrompt(t *testing.T) {
	cases := []struct {
		name string
		b    config.Broker
		want bool
	}{
		{"user with no credential source", config.Broker{Username: "u"}, true},
		{"password_cmd set", config.Broker{Username: "u", PasswordCmd: "pass show x"}, false},
		{"password_env set", config.Broker{Username: "u", PasswordEnv: "PW"}, false},
		{"literal password set", config.Broker{Username: "u", Password: "p"}, false},
		{"anonymous broker", config.Broker{}, false},
	}
	for _, c := range cases {
		if got := needsPrompt(c.b); got != c.want {
			t.Errorf("%s: needsPrompt = %v, want %v", c.name, got, c.want)
		}
	}
}

// Once the TUI owns the terminal a prompt must fail with an instruction
// rather than writing to stderr in the middle of the topic panel.
func TestSealedPromptRefusesInsteadOfCorruptingTheDisplay(t *testing.T) {
	p := newCachedPrompt()
	p.Seal()

	_, err := p.Func()("password for u@prod: ")
	if err == nil {
		t.Fatal("a sealed prompt returned a password; it must refuse")
	}
	for _, want := range []string{"password_cmd", "--broker"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

func TestSealedPromptReplaysACachedAnswer(t *testing.T) {
	p := newCachedPrompt()
	const key = "password for u@prod: "
	p.values[key] = []byte("hunter2")
	p.Seal()

	got, err := p.Func()(key)
	if err != nil {
		t.Fatalf("a cached answer was not replayed: %v", err)
	}
	if string(got) != "hunter2" {
		t.Fatalf("replayed %q, want %q", got, "hunter2")
	}
}
