package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/ui/sanitize"
)

func cmdBrokers(args []string) int {
	var g globalFlags
	fs := newFlagSet("lazymqtt brokers", &g)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := loadConfig(g)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}
	names := cfg.Names()
	if len(names) == 0 {
		fmt.Println("no broker profiles configured; run `lazymqtt config init`")
		return 0
	}
	for _, n := range names {
		b := cfg.Brokers[n]
		url, err := b.ServerURL()
		if err != nil {
			url = "invalid: " + err.Error()
		}
		fmt.Printf("%-20s %s\n", n, url)
	}
	return 0
}

func cmdConfig(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: lazymqtt config init|check")
		return 2
	}
	switch args[0] {
	case "init":
		path := config.DefaultPath()
		if err := config.WriteStarter(path); err != nil {
			fmt.Fprintln(os.Stderr, "lazymqtt:", err)
			return 1
		}
		fmt.Println("wrote", path)
		return 0

	case "check":
		var g globalFlags
		fs := newFlagSet("lazymqtt config check", &g)
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		path := config.Discover(g.config)
		if path == "" {
			fmt.Println("no config file found; built-in defaults are in effect")
			return 0
		}
		cfg, err := config.Load(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for _, w := range config.Check(&cfg).Warnings {
			fmt.Printf("warning: %s\n", w)
		}
		fmt.Printf("%s is valid: %d broker profile(s)\n", path, len(cfg.Brokers))
		for _, n := range cfg.Names() {
			url, _ := cfg.Brokers[n].ServerURL()
			fmt.Printf("  %-20s %s\n", n, url)
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown config subcommand %q\n", args[0])
	return 2
}

// connectOneShot builds a client for the scriptable subcommands and waits for
// the connection to come up.
func connectOneShot(g globalFlags) (*app.App, config.Resolved, error) {
	cfg, err := loadConfig(g)
	if err != nil {
		return nil, config.Resolved{}, err
	}
	logger, err := logging.Setup(logging.Options{Level: logLevel(cfg, g), File: logFile(cfg, g)})
	if err != nil {
		return nil, config.Resolved{}, err
	}
	b, err := cfg.BrokerRef(g.broker)
	if err != nil {
		return nil, config.Resolved{}, err
	}
	a := app.New(cfg, logger)

	ctx, cancel := signalContext()
	defer cancel()
	r, err := cfg.Resolve(ctx, b, terminalPrompt, g.topics)
	if err != nil {
		return nil, config.Resolved{}, err
	}
	if err := a.Connect(ctx, r); err != nil {
		return nil, config.Resolved{}, err
	}
	return a, r, nil
}

func waitForUp(a *app.App, timeout time.Duration) error {
	deadline := time.After(timeout)
	client := a.Client()
	for {
		select {
		case <-deadline:
			return errors.New("timed out waiting for the broker")
		case ev, ok := <-client.Events():
			if !ok {
				return errors.New("connection closed")
			}
			switch ev.Status.State {
			case mqtt.StateConnected:
				return nil
			case mqtt.StateFailed:
				return ev.Err
			}
		}
	}
}

func cmdPub(args []string) int {
	var g globalFlags
	fs := newFlagSet("lazymqtt pub", &g)
	qos := fs.Int("q", 0, "QoS level (0, 1 or 2)")
	retain := fs.Bool("r", false, "set the retain flag")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "usage: lazymqtt pub <topic> <payload|-> [-q N] [-r]")
		return 2
	}
	topic, payload := rest[0], []byte(rest[1])
	if rest[1] == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lazymqtt:", err)
			return 1
		}
		payload = b
	}
	if *qos < 0 || *qos > 2 {
		fmt.Fprintln(os.Stderr, "lazymqtt: -q must be 0, 1 or 2")
		return 2
	}

	a, _, err := connectOneShot(g)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	if err := waitForUp(a, 15*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}
	ctx, cancel := signalContext()
	defer cancel()
	if err := a.Client().Publish(ctx, mqtt.PublishRequest{
		Topic: topic, Payload: payload, QoS: byte(*qos), Retain: *retain,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}
	return 0
}

func cmdSub(args []string) int {
	var g globalFlags
	fs := newFlagSet("lazymqtt sub", &g)
	qos := fs.Int("q", 0, "QoS level (0, 1 or 2)")
	asJSON := fs.Bool("json", false, "emit NDJSON instead of plain lines")
	count := fs.Int("n", 0, "exit after N messages (0 = run until interrupted)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	filters := fs.Args()
	if len(filters) == 0 {
		filters = g.topics
	}
	if len(filters) == 0 {
		fmt.Fprintln(os.Stderr, "usage: lazymqtt sub <filter> [-q N] [--json] [-n N]")
		return 2
	}
	for _, f := range filters {
		if err := mqtt.ValidateFilter(f); err != nil {
			fmt.Fprintln(os.Stderr, "lazymqtt:", err)
			return 2
		}
	}
	g.topics = filters

	a, _, err := connectOneShot(g)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}
	defer func() { _ = a.Close() }()

	if err := waitForUp(a, 15*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}
	subs := make([]mqtt.Subscription, 0, len(filters))
	for _, f := range filters {
		subs = append(subs, mqtt.Subscription{Filter: f, QoS: byte(*qos)})
	}
	ctx, cancel := signalContext()
	defer cancel()
	if err := a.Client().Subscribe(ctx, subs); err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}

	out := bufio.NewWriter(os.Stdout)
	defer func() { _ = out.Flush() }()
	enc := json.NewEncoder(out)

	n := 0
	for {
		select {
		case <-ctx.Done():
			return 0
		case m, ok := <-a.Client().Messages():
			if !ok {
				return 0
			}
			if *asJSON {
				// NDJSON, so the output pipes straight into jq.
				_ = enc.Encode(struct {
					Time     time.Time `json:"time"`
					Topic    string    `json:"topic"`
					Payload  string    `json:"payload"`
					QoS      byte      `json:"qos"`
					Retained bool      `json:"retained"`
				}{m.ReceivedAt, m.Topic, string(m.Payload), m.QoS, m.Retained})
			} else {
				_, _ = fmt.Fprintf(out, "%s %s %s\n",
					m.ReceivedAt.Format("15:04:05.000"), m.Topic, sanitize.Line(m.Payload, 0))
			}
			_ = out.Flush()
			n++
			if *count > 0 && n >= *count {
				return 0
			}
		}
	}
}

// runHeadless streams one line per message to stdout. It exists to prove the
// whole MQTT layer against a real broker without any UI in the way.
func runHeadless(a *app.App, cfg config.Config, g globalFlags) int {
	b, err := cfg.BrokerRef(g.broker)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}
	ctx, cancel := signalContext()
	defer cancel()

	r, err := cfg.Resolve(ctx, b, terminalPrompt, g.topics)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}
	if err := a.Connect(ctx, r); err != nil {
		fmt.Fprintln(os.Stderr, "lazymqtt:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "connecting to %s as %s, subscribing to %s\n",
		r.Options.ServerURL, r.Options.ClientID, filterList(r.Subs))

	client := a.Client()
	out := bufio.NewWriter(os.Stdout)
	defer func() { _ = out.Flush() }()

	for {
		select {
		case <-ctx.Done():
			return 0
		case ev, ok := <-client.Events():
			if !ok {
				return 0
			}
			fmt.Fprintf(os.Stderr, "[%s] %s\n", ev.Kind, describeEvent(ev))
			if ev.Status.State == mqtt.StateFailed {
				return 1
			}
		case m, ok := <-client.Messages():
			if !ok {
				return 0
			}
			_, _ = fmt.Fprintf(out, "%s q%d r=%v %s %s\n",
				m.ReceivedAt.Format("15:04:05.000"), m.QoS, m.Retained,
				m.Topic, sanitize.Line(m.Payload, 200))
			_ = out.Flush()
		}
	}
}

func describeEvent(ev mqtt.Event) string {
	var parts []string
	parts = append(parts, ev.Status.State.String())
	for _, s := range ev.Subs {
		parts = append(parts, fmt.Sprintf("%s granted q%d", s.Filter, s.GrantedQoS))
	}
	if ev.Err != nil {
		parts = append(parts, "err: "+ev.Err.Error())
	}
	return strings.Join(parts, " ")
}

func filterList(subs []mqtt.Subscription) string {
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		out = append(out, s.Filter)
	}
	if len(out) == 0 {
		return "(nothing)"
	}
	return strings.Join(out, ", ")
}
