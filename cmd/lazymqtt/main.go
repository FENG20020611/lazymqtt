// Command lazymqtt is a keyboard-driven TUI MQTT client.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/state"
	"github.com/Onizuka893/lazymqtt/internal/ui"
	"github.com/Onizuka893/lazymqtt/internal/version"
)

func main() {
	version.Resolve()
	os.Exit(run())
}

// run returns the process exit code so every defer still fires.
func run() int {
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "brokers":
			return cmdBrokers(args[1:])
		case "config":
			return cmdConfig(args[1:])
		case "pub":
			return cmdPub(args[1:])
		case "sub":
			return cmdSub(args[1:])
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", args[0])
			usage()
			return 2
		}
	}
	return cmdTUI(args)
}

type globalFlags struct {
	broker   string
	config   string
	topics   stringList
	logFile  string
	debug    bool
	headless bool
	version  bool
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func newFlagSet(name string, g *globalFlags) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&g.broker, "broker", "", "broker profile name, or a URL such as tcp://host:1883")
	fs.StringVar(&g.broker, "b", "", "shorthand for --broker")
	fs.StringVar(&g.config, "config", "", "config file path")
	fs.StringVar(&g.config, "c", "", "shorthand for --config")
	fs.Var(&g.topics, "topic", "subscribe on connect (repeatable, overrides the profile)")
	fs.Var(&g.topics, "t", "shorthand for --topic")
	fs.StringVar(&g.logFile, "log-file", "", "debug log destination")
	fs.BoolVar(&g.debug, "debug", false, "set the log level to debug")
	fs.BoolVar(&g.version, "version", false, "print version, commit and build date")
	fs.Usage = usage
	return fs
}

func usage() {
	fmt.Fprint(os.Stderr, `lazymqtt — a keyboard-driven TUI MQTT client

usage:
  lazymqtt [flags]
  lazymqtt brokers
  lazymqtt config init|check
  lazymqtt pub <topic> <payload|-> [-q N] [-r]
  lazymqtt sub <filter> [-q N] [--json] [-n N]

flags:
  -b, --broker <name|url>   broker profile name, or a URL (tcp://host:1883, mqtts://…)
  -c, --config <path>       config file path
  -t, --topic <filter>      subscribe on connect (repeatable, overrides the profile)
      --log-file <path>     debug log destination
      --debug               set the log level to debug
      --headless            stream messages to stdout instead of starting the TUI
      --version             print version, commit and build date
  -h, --help
`)
}

func cmdTUI(args []string) int {
	var g globalFlags
	fs := newFlagSet("lazymqtt", &g)
	fs.BoolVar(&g.headless, "headless", false, "stream messages to stdout instead of starting the TUI")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if g.version {
		fmt.Println(version.Info())
		return 0
	}

	cfg, err := loadConfig(g)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}

	logger, err := logging.Setup(logging.Options{
		Level: logLevel(cfg, g),
		File:  logFile(cfg, g),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}
	defer func() { _ = logger.Close() }()

	a := app.New(cfg, logger)
	defer func() { _ = a.Close() }()

	if g.headless {
		return runHeadless(a, cfg, g)
	}
	return runTUI(a, cfg, g)
}

func runTUI(a *app.App, cfg config.Config, g globalFlags) (code int) {
	// Collect any interactive password now, while stdout and stdin are still
	// ours. Once the program starts, the prompt is sealed: a connect running
	// inside a tea.Cmd must never write to the terminal (§21, pitfall 9).
	prompt := newCachedPrompt()
	warmPrompt(cfg, g.broker, prompt)

	// A bad state file must never stop the app: it holds preferences, not
	// configuration. Log the reason and start fresh.
	statePath := state.Path()
	saved, err := state.Load(statePath)
	if err != nil {
		a.Logger.Warn("ignoring the state file", "path", statePath, "err", err)
	}

	model := ui.New(ui.Options{
		App:          a,
		Config:       cfg,
		InitialTopic: g.topics,
		AutoBroker:   g.broker,
		Prompt:       prompt.Func(),
		State:        saved,
	})

	prompt.Seal()

	p := tea.NewProgram(model)
	a.Sender = app.SenderFunc(func(msg any) { p.Send(msg) })

	// A panic must never leave the terminal in raw mode with the alt screen
	// active: restore first, print the stack afterwards.
	defer func() {
		if r := recover(); r != nil {
			p.Kill()
			fmt.Fprintf(os.Stderr, "lazymqtt panicked: %v\n\n%s\n", r, debug.Stack())
			code = 1
		}
	}()

	final, err := p.Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}

	// Persist after the program has stopped rather than from a tea.Cmd racing
	// tea.Quit: the store may only be read from this goroutine, and here it
	// is the only one left.
	if fm, ok := final.(ui.Model); ok {
		if err := state.Save(statePath, fm.StateSnapshot()); err != nil {
			a.Logger.Warn("could not write the state file", "path", statePath, "err", err)
		}
	}
	return 0
}

func loadConfig(g globalFlags) (config.Config, error) {
	path := config.Discover(g.config)
	if path == "" && g.config != "" {
		return config.Config{}, fmt.Errorf("config file not found: %s", g.config)
	}
	return config.Load(path)
}

func logLevel(cfg config.Config, g globalFlags) string {
	if g.debug || os.Getenv("LAZYMQTT_DEBUG") == "1" {
		return "debug"
	}
	return cfg.Logging.Level
}

func logFile(cfg config.Config, g globalFlags) string {
	if g.logFile != "" {
		return config.ExpandPath(g.logFile)
	}
	return cfg.Logging.File
}

// signalContext cancels on SIGINT/SIGTERM, so the headless and one-shot
// commands stop cleanly.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
