package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// PasswordCmdTimeout bounds a secret-manager invocation. A hung `op read`
// must not hang the connect.
const PasswordCmdTimeout = 10 * time.Second

// PromptFunc asks the user for a password interactively. The app supplies one
// that reads from the terminal with echo disabled; tests supply a stub.
type PromptFunc func(prompt string) ([]byte, error)

// ErrNoPassword is returned when a broker sets a username but no credential
// source and no prompt is available.
var ErrNoPassword = errors.New("no password source configured")

// ResolvePassword walks the credential chain, first match wins:
//
//  1. password_cmd — stdout of a command, trailing newline trimmed
//  2. password_env — the named environment variable
//  3. password     — a literal, with ${VAR} expansion
//  4. an interactive prompt, held in memory only
//
// The result is never written anywhere and never logged.
func ResolvePassword(ctx context.Context, b Broker, prompt PromptFunc) ([]byte, error) {
	switch {
	case b.PasswordCmd != "":
		return runPasswordCmd(ctx, b.PasswordCmd)

	case b.PasswordEnv != "":
		v, ok := os.LookupEnv(b.PasswordEnv)
		if !ok {
			return nil, fmt.Errorf("password_env: $%s is not set", b.PasswordEnv)
		}
		return []byte(v), nil

	case b.Password != "":
		return []byte(os.ExpandEnv(b.Password)), nil

	case b.Username != "" && prompt != nil:
		return prompt(fmt.Sprintf("password for %s@%s: ", b.Username, b.Name))
	}
	return nil, nil
}

func runPasswordCmd(ctx context.Context, command string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, PasswordCmdTimeout)
	defer cancel()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("password_cmd timed out after %s", PasswordCmdTimeout)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		// The command string can itself be sensitive, so report only its
		// failure, never its output beyond stderr.
		return nil, fmt.Errorf("password_cmd failed: %s", msg)
	}
	return []byte(strings.TrimRight(string(out), "\r\n")), nil
}
