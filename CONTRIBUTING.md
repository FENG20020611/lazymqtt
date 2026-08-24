# Contributing to lazymqtt

Thanks for looking. This file records the decisions that are easy to undo by
accident — the ones where a reviewer would otherwise have to explain the same
thing twice. The full design rationale lives in
[`lazymqtt_plan.md`](lazymqtt_plan.md); section references below (§n) point at
it.

## Getting set up

```sh
git clone https://github.com/Onizuka893/lazymqtt
cd lazymqtt
make build
make dev        # docker compose up + seed + run against the local broker
```

`make dev` brings up three brokers: an anonymous one on 1883 (plus TLS on 8883,
mutual TLS on 8884 and websockets on 9001), a password-protected one on 1884
(`lazymqtt` / `secret`), and a one-shot seeder that publishes a realistic
retained topic tree so a fresh clone shows something interesting immediately.

Run `make certs` before touching the TLS listeners; the certificates are
generated locally and are not committed.

```sh
make test       # go test -race ./...
make test-int   # compose up + integration suite + compose down
make lint       # golangci-lint + gofumpt
make loadgen RATE=10000
```

## The one thing to know before changing anything

**Never send one `tea.Msg` per MQTT message.** The coalescer in
`internal/app/bridge.go` batches into at most one `Update` per 50 ms, or per
512 messages, whichever comes first. It is what makes the app survive a
firehose, and `bridge_test.go` locks the behaviour in. §6 explains why.

Two invariants follow from it, both worth defending in review:

- **`store.Store` has exactly one writer**, the Bubble Tea update goroutine. It
  contains no mutexes and needs none. Do not call a `Store` method from a
  `tea.Cmd` goroutine or from a paho callback.
- **Paho callbacks never block.** `onPublishReceived` does one non-blocking
  channel send and returns. Blocking it stalls ack processing until the broker
  disconnects a client that appears to be doing nothing wrong. This is why the
  dedupe table it consults is an `atomic.Pointer` to an immutable snapshot
  rather than mutex-guarded state — taking `a.mu` there would contend with
  every subscribe and status update.

## Layering

```
ui → app → {store, mqtt, config, state}
```

The domain packages must not import `charm.land/*`, `internal/ui` or
`internal/app`. A `depguard` rule in `.golangci.yml` enforces this, and the
reason it is enforced rather than documented is that it is what makes the MQTT
layer testable without a terminal.

`internal/mqtt` is the port. Adapters live under it (`paho5`, and a `paho3`
adapter is planned); nothing above the port may know which one is in use.

## Rules that look arbitrary and are not

**CLI parsing stays on stdlib `flag`.** Adopt `spf13/cobra` when the project
crosses **four real subcommands** or genuinely wants generated shell
completions — not before. Cobra pulls in a dependency tree for a help formatter
this project does not use. (§4)

**Nothing writes to stdout or stderr while the TUI is running.** Any such write
corrupts the alt screen. The standard `log` package is redirected to
`io.Discard` in `logging.Setup`, and paho's loggers are adapted into slog. If
you need to tell the user something, use a toast or the logs panel. (§16)

**Every goroutine recovers.** A panic on a goroutine you spawned cannot be
caught by `main`'s deferred recover — it takes the process down with the
terminal still in raw mode and the alt screen active, which is the one failure
mode a TUI must never have. `internal/app` has `Bridge.run` and
`internal/mqtt/paho5` has `Adapter.spawn`; use them rather than a bare `go`.
(§13, §21 pitfall 11)

**Never rewrite `config.yaml`.** It is a file the user hand-edited and
commented. UI preferences go in `internal/state`, which writes a separate
`state.json` and holds no credentials. (§9.4, §21 pitfall 18)

**Keep the root `ui.Model` small.** It is copied on every `Update` and boxed
into a `tea.Model` on the way out, so an embedded `textarea` costs tens of
kilobytes of garbage per keystroke. Anything read-only for the model's lifetime
or larger than a few hundred bytes goes behind a pointer — that is why `keys`,
`theme`, `help`, `prompt` and `publish` all are. `modelsize_test.go` fails if
the struct grows past 2 KB. (§21 pitfall 15)

**Sanitise at the render boundary, not at ingest.** Payloads are
attacker-controllable bytes going into a terminal, so everything displayed
passes through `internal/ui/sanitize`. The store keeps the raw bytes, because
that is what `y` copies.

**Window before you transform.** The detail pane can show forty lines, so it
slices the raw payload to the visible window *before* sanitising it. Sanitising
first and slicing after is the natural order and it makes a 10 MB payload take
seconds per frame.

**One SUBSCRIBE packet per filter.** MQTT 5 carries the subscription identifier
on the SUBSCRIBE packet, not per filter (§3.8.2.1.2), so batching filters gives
them all the same identifier and `paho5/dedupe.go` can no longer tell which
subscription a delivery came from. Batching would save round trips only on the
reconnect replay, and correctness beats that.

**Never dedupe messages by comparing payloads.** Two identical messages
published twice are two messages. Suppressing the second one makes the viewer
lie about the broker, which is worse than showing a duplicate. Dedupe keys on
the subscription identifier or it does not happen.

## Tests

Layered, per §14:

- **Domain** — exhaustive and fast. Topic matching gets a table test with every
  example from both specs plus a fuzz target; it is a small function that goes
  subtly wrong constantly.
- **Port** — against `mqtt/mqtttest.Fake`. Re-subscribe on every connection,
  `StateFailed` with no retry after an auth rejection, drop-oldest when the
  channel fills.
- **Bridge** — with an injected ticker, plus `goleak`.
- **TUI** — `Update` is a pure function, so feed it `tea.KeyPressMsg` sequences
  and assert on model state.
- **Integration** — `//go:build integration` under `test/integration`, against
  a real Mosquitto. These **skip** rather than fail when no broker is
  reachable, so `go test ./...` is green on a laptop without Docker.

Coverage is reported but not gated on a number: coverage targets on a TUI
project produce test theatre around `View()` instead of tests of the things
that break.

### Golden files

`internal/ui/testdata/*.golden` holds ANSI-stripped frames. Regenerate with:

```sh
go test ./internal/ui -update
```

**Read the diff before committing it.** These files exist to make an
unintentional layout change visible; regenerating without looking defeats the
whole point. The escapes are stripped because a golden file containing colour
passes on the author's terminal and fails on a CI runner with a different
`TERM` (§21 pitfall 19); `TestFramesAreStyled` separately asserts that styling
is still emitted.

### Adding a test that needs a broker

Put it in `test/integration`, tag it `integration`, and take the broker URL
from the environment with a sensible default:

```go
url := requireBroker(t, brokerURL(envBroker, defaultBroker))
```

`requireBroker` skips when nothing is listening. A skip means "no broker" and
never "the broker misbehaved", so every real failure below that line is
genuine.

## Performance

The target from §19: **500 topics at 5,000 msg/s, under 100 MB resident, with
imperceptible input latency.** `internal/ui/bench_test.go` measures it:

```sh
go test ./internal/ui -run XXX -bench . -benchmem
go test ./internal/ui -run Memory -v          # the heap ceiling
```

Rough numbers on an M3 Pro, for orientation rather than as thresholds: a 250
message batch ingests in ~11 µs, a full frame renders in ~1 ms against a 50 ms
budget, and a keypress costs ~700 ns. If you make one of these an order of
magnitude worse, something is wrong.

Watch `allocs/op` as closely as `ns/op`. Both of the real regressions found so
far — a 58 KB model copy and an O(payload) detail render — showed up as
allocation counts long before anyone would have described the app as slow.

For a real profile, drive the app with the load generator:

```sh
go run ./cmd/mqttload --rate 10000 --topics 500 --payload 256 --duration 60s
go run ./cmd/mqttload --pattern sawtooth --rate 5000    # bursty, exercises batching
go run ./cmd/mqttload --pattern retained --count 20000  # retained flood
```

`mqttload` is a development tool and is excluded from release builds.

## Commits and pull requests

- One concern per pull request. A refactor and a behaviour change in the same
  diff are two pull requests.
- Explain *why* in the commit message; the diff already says what.
- `make test lint` must pass. `gofumpt`, not `gofmt`.
- New behaviour comes with a test that fails without it.
- Comments explain constraints and trade-offs the code cannot express. A
  comment restating the next line is noise; a comment explaining why the
  obvious implementation is wrong is the reason someone does not undo your fix.

## Things deliberately out of scope

Listed here so a pull request implementing one does not come as a surprise:
a plugin system, telemetry of any kind, auto-update, a config GUI, an
abstraction over the store with one implementation, and an event bus for a
single-process app. §20 has the roadmap for what *is* wanted.
