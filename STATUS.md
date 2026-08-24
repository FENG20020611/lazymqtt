# lazymqtt — implementation status

Working notes for picking this up in a fresh session. The design of record is
[`lazymqtt_plan.md`](lazymqtt_plan.md); section references below (§n) point at it.

**Last updated:** 2026-08-24
**State:** Phases 0–8 implemented and green. Phase 9 (hardening) and Phase 10
(release) not started. Module path `github.com/Onizuka893/lazymqtt`, Go 1.27.

```
go build ./...      # clean
go vet ./...        # clean
go test -race ./... # all packages pass
gofmt -l ./cmd ./internal   # empty
```

~7,500 lines of source, ~1,700 lines of tests, 55 Go files.

---

## The one thing to know before touching anything

**Never send one `tea.Msg` per MQTT message.** The coalescer in
`internal/app/bridge.go` is what makes the app survive a firehose: it batches
into at most one `Update` per 50 ms (or per 512 messages, whichever comes
first). Everything else in the codebase is ordinary engineering; that choice
is load-bearing. §6 of the plan explains why, and `bridge_test.go` locks the
behaviour in.

Two related invariants, both stated in doc comments and worth defending in
review:

- `store.Store` has exactly one writer — the Bubble Tea update goroutine. It
  contains no mutexes and needs none. Do not call a `Store` method from a
  `tea.Cmd` goroutine or a paho callback.
- Paho callbacks never block. `onPublishReceived` does one non-blocking
  channel send and returns. Blocking it stalls ack processing until the broker
  disconnects a client that appears to be doing nothing wrong.

A `depguard` rule in `.golangci.yml` enforces the layering (`ui → app →
{store, mqtt, config}`); the domain packages cannot import `charm.land/*`.

---

## What is done

### Phase 0 — toolchain (partial)
`go.mod`, directory skeleton, `Makefile`, `.golangci.yml` (with the depguard
layering rule), `.gitignore`, `internal/version` with ldflags injection and a
`debug.ReadBuildInfo` fallback for the `go install …@latest` path.

**Missing:** `.github/workflows/{ci,release}.yml`, `LICENSE`, `README.md`,
a test for `version`.

### Phase 1 — domain core ✅
- `internal/mqtt`: `Message`, `Properties`, `Subscription`, `Options`,
  `Client` interface, `ConnState`/`ConnStatus`, reason-code classification
  (`Fatal`, `ReasonText`), `MatchTopic`/`ValidateFilter`/`ValidateTopic`,
  TLS config construction.
- `internal/store`: generic `Ring[T]`, `TopicNode` tree, `Store` facade with
  `Ingest` as the only write path, all four caps, `container/list` LRU topic
  eviction that prunes emptied parents, `Filter`, `Stats` with an EWMA rate.

Tests cover the spec examples for topic matching (including `$SYS` invisibility
to a leading wildcard) plus a fuzz target, ring wraparound and `Slice` across
the boundary, tree insert/flatten/collapse, LRU eviction, byte accounting.

### Phase 2 — configuration ✅
Schema, discovery chain, `goccy/go-yaml` with `DisallowUnknownField()`,
`version:` checked before anything else, pointer fields so absent is
distinguishable from zero, aggregated semantic validation, the file-permission
refusal for a literal `password:` in a group/world-readable file, the
credential chain (`password_cmd` → `password_env` → literal → prompt), path
expansion, `Resolve` producing a ready-to-connect value.

`lazymqtt config init` writes a commented starter file at mode 0600, and a test
asserts that file parses.

### Phase 3 — MQTT adapter (headless) ⚠️
`internal/mqtt/paho5/adapter.go` implements the port on autopaho: truncation at
ingest, `Seq` assignment, non-blocking send with a drop counter, callbacks
mapped to events, **re-subscribe in `OnConnectionUp`** (not at startup — §12.1),
terminal classification of auth/TLS failures so a rejected credential is never
retried in a loop, paho's loggers wired into slog.

`internal/mqtt/mqtttest` is a programmable in-memory `Client` used by everything
above the port.

`--headless` streams one line per message to stdout.

**Not verified against a real broker.** The Docker CLI is installed but the
daemon was not running, so the compose stack has never been started. The only
live exercise so far is a connection-refused path (correctly classified as
retryable). **This is the highest-value next step** — start Docker Desktop and
run `make dev`.

### Phase 4 — bridge and coalescer ✅
Drain loops, ticker flush, batch cap, drop propagation, context shutdown,
panic recovery on both goroutines, `app.App` wiring config → client → bridge
with one context per connection. Tests use an injected ticker and `goleak`.

### Phases 5–8 — the TUI ✅
- `internal/ui/layout.go`: pure geometry, including a stacked single-panel mode
  for terminals under 64 columns.
- `internal/ui/root.go` / `update.go` / `keyhandler.go` / `view.go`: the root
  model, mode-based key routing, and rendering.
- `internal/ui/panel/*`: topic tree (bespoke, with cursor stability), message
  list, detail pane, subscriptions with granted-QoS display, status header,
  footer, logs, prompt, publish modal, broker picker, help, confirm.
- `internal/ui/sanitize`: strips ESC, C0/C1 and Unicode `Cf` before anything
  reaches the terminal. Raw bytes stay in the store so copy stays faithful.
- `internal/ui/keys`: one binding registry feeding both the dispatcher and
  `bubbles/help`, so the help overlay cannot drift.
- Pause / follow / autoscroll / wrap / retained-only, incremental filter with
  `n`/`N`, OSC52 copy, clear topic and clear-all-with-confirmation, toasts,
  the persistent `insecure_skip_verify` banner, the reconnect countdown.

Tests cover focus cycling, cursor clamping, cursor stability under 100
insertions, `q` inside a prompt staying a literal `q`, `esc` closing every
mode, `ctrl+c` always quitting, invalid filters rejected inline with no MQTT
call, pause freezing the view while ingest continues, layout arithmetic and
non-panicking render at 80×24 / 120×40 / 30×10 / 10×4 / 1×1 / 0×0.

### CLI ✅
`lazymqtt [flags]`, `brokers`, `config init|check`, `pub`, `sub` (with
`--json` emitting NDJSON), `--headless`. stdlib `flag` with a subcommand
switch — the plan's rule is to adopt Cobra only past four real subcommands or
when shell completions are wanted; that rule belongs in `CONTRIBUTING.md`,
which does not exist yet.

### Dev environment ⚠️
`deploy/docker-compose.yml` (mosquitto with plain/TLS/websocket listeners, a
second password-protected broker on 1884 whose password file is generated at
startup so no hash is committed, and a seed container publishing a realistic
retained tree), mosquitto configs, and `deploy/certs/gen.sh`. **Never run.**

---

## What is left

Ordered by value.

### 1. Verify Phase 3 against a real broker — blocking everything else
Start Docker Desktop (the CLI is installed; the daemon was not running), then:

```
docker compose -f deploy/docker-compose.yml up -d
./bin/lazymqtt --headless -b tcp://localhost:1883 -t '#'
```

Expect the seeded tree to stream. Then the test that matters most: kill the
broker container, confirm the UI shows `reconnecting…`, restart it, and
confirm **messages flow again**. That is the §12.1 bug — after a blip the UI
says "Connected" and nothing ever arrives — and it is silent when it happens.

Also unverified: TLS and mTLS (`make certs` first), QoS 1/2 round trips and
retained-message floods. Auth-rejection classification is now unit-tested, but
confirm end to end against `mosquitto-auth` on port 1884 (`lazymqtt / secret`)
that a wrong password lands in `failed` and stops rather than retrying.

### 2. Integration tests (`test/integration/`, `//go:build integration`)
The directory does not exist. Per §14.2: round trip, retained delivery on
subscribe, QoS 1 and 2, the reconnect test above, a 20,000-message retained
flood asserting `max_topics` is respected without deadlock, TLS and mTLS.
`make test-int` is already wired to run them.

### 3. `cmd/mqttload` (§15.2)
Not written. The load generator is what validates the coalescer and the memory
ceiling for real: `--rate`, `--topics`, `--payload`, `--qos`, `--duration`, and
`--pattern sawtooth|retained`. Excluded from release builds.

### 4. Phase 9 — hardening
- Golden-file `View()` tests with `tea.WithColorProfile(colorprofile.Ascii)`
  and `tea.WithWindowSize` — without forcing the profile they break between CI
  and a developer's terminal (§21 pitfall 19).
- The state file at `~/.local/state/lazymqtt/state.json` (§9.4): last broker,
  expanded nodes, recent publish payloads. **Never rewrite `config.yaml`.**
- A `pprof` pass under load; the §19 success test is 500 topics at 5,000 msg/s
  for an hour under 100 MB RSS with imperceptible input latency.
- Soak and chaos tests: broker restart loop, 10 MB payloads, payloads full of
  ANSI escapes, wide/emoji topics.
- `goleak` across more of the suite (currently only `internal/app`).
- `README.md` with a VHS or asciinema recording, `CONTRIBUTING.md` recording
  the Cobra rule and the layering rules, `LICENSE` (MIT or Apache-2.0 — both
  are compatible with paho's EPL-2.0/EDL-1.0).

### 5. Phase 10 — release
`.goreleaser.yaml`, the two GitHub workflows, a Homebrew tap, a documented
config reference, `v0.1.0`.

### 6. Deferred by design
The MQTT 3.1.1 adapter (`internal/mqtt/paho3`) with `protocol: auto`
negotiation on CONNACK `0x84`, and everything in the §20 roadmap. The port
interface and the `version`/`protocol` config field are already in place for
the 3.1.1 adapter to slot into.

---

## Fixed after the first pass

- **The interactive password prompt corrupted the TUI.** `promptPassword` wrote
  to stderr and read stdin from a `tea.Cmd` goroutine while Bubble Tea owned
  the terminal in raw alt-screen mode (§21, pitfall 9). Replaced with
  `cmd/lazymqtt/prompt.go`: passwords are collected *before* the program
  starts and replayed from a cache; once sealed, a prompt returns an
  instruction instead of writing to the terminal.
- **A wrong password would have retried forever.** `mqtt.Fatal` narrowed to
  `interface{ Code() byte }`, but `autopaho.ConnackError` carries its reason
  as a *field*, so the check never matched and an authentication rejection was
  treated as a network blip. The port now defines its own `mqtt.ConnackError`
  (with `Code()`), and the adapter translates autopaho's denial into it in
  `onConnectError`. Covered by tests in `internal/mqtt/state_test.go` and
  `internal/mqtt/paho5/adapter_test.go`.
- **`tls.enabled: true` could silently connect in the clear.** autopaho picks
  TLS from the URL scheme alone and ignores the `*tls.Config` on a plaintext
  scheme, so a profile written as `url: mqtt://host:1883` (or `scheme: mqtt`)
  built a TLS config that was never used. `Broker.ServerURL` now upgrades the
  scheme whenever `tls.enabled` is set — `mqtt`/`tcp` → `tls`, `ws` → `wss`.
- **A warning that was wrong.** A "tls.enabled with port 1883" warning was
  added and then removed: TLS on the conventionally plaintext port is unusual
  but entirely legal, and the first real config it met was doing exactly that.
  A test now asserts the setup produces `tls://host:1883` with no complaint.
- **Pause did not pause anything.** The flag only skipped the rate tick and
  the follow-jump; every panel still re-rendered from a live store. Pause now
  defers the store write and buffers arrivals, bounded by `stream_history`
  with the overflow dropped and counted. Resume applies the backlog. This is a
  deliberate deviation from §7.5's "ingest continues into the store": every
  panel reads from the store, so deferring the write is the only thing that
  actually freezes the view while leaving navigation working — which is the
  entire point of pausing on a 50 Hz topic. The header shows `⏸ paused
  (+N held)`.
- **The message selection slid onto its neighbour on every arrival.** The
  message list tracked a bare index counted from the newest, so each new
  message silently reassigned every index. It now anchors on the message's
  `Seq` and re-resolves after each ingest — the same fix the tree already had
  for topics (§7.3, pitfall 13), which the plan never extended to the message
  list. When the anchored message ages out of the ring the cursor holds at the
  oldest survivor. The detail pane reads through the same selection, so it
  follows.
- **A spurious failed SUBACK on every connect.** The initial subscribe fires
  before CONNACK and returned `autopaho.ConnectionDownError`, which surfaced
  as a red toast at startup. The adapter now treats a connection-down
  subscribe as deferred — the desired set is already recorded and
  `OnConnectionUp` re-issues it.

## Known rough edges

- `Options.Protocol` is parsed and carried but nothing acts on it: the v5
  adapter is always selected. `app.DefaultClientFactory` is the switch point.
- Dedupe across overlapping subscriptions (§11, pitfall 14) is **not
  implemented**. With both `#` and `devices/+/state` active, MQTT 3.1.1
  delivers a matching message once per subscription and the counts will
  double. The MQTT 5 subscription identifier is already carried on
  `Properties.SubIdentifiers` and is the intended fix.
- `Store.Stats()` recomputes the node count by walking the tree on every call,
  and `View` calls it every frame. Fine at 5,000 nodes, worth caching if the
  profiling pass flags it.
- The per-frame "skip rebuilding an unchanged panel body" optimisation (§7.4)
  is not implemented. `Store.Dirty()`/`ClearDirty()` exist and are wired but
  no panel consults them yet.
- The publish modal has no MQTT 5 property editing (correctly out of MVP
  scope, §19).
- **Credentials require a config profile.** `--broker tcp://user:pass@host`
  is not parsed: the bare-URL path in `BrokerRef` builds a `Broker` with no
  `Username`. Anything needing authentication needs a `brokers:` entry.
- The TUI's broker picker (`c`) cannot prompt for a password, because the
  prompt is sealed once the program starts. Profiles reached that way need
  `password_cmd` or `password_env`. An in-TUI masked prompt is the proper fix;
  `panel.PromptPassword` already exists as the hook for it.
- `internal/ui/update.go:selectedTopic` has redundant branches that all return
  the same value; harmless, but it should be simplified.

## Deviations from the plan

- **`internal/mqtt/mqtttest` lives beside the port**, as planned, but the
  bridge tests alias it (`type Fake = mqtttest.Fake`) purely for readable
  helper signatures.
- **`Ring.Slice` copies** rather than returning a view. A view across a wrap
  boundary is not expressible as one slice, and the copy is of pointers.
- **The subscriptions panel counts matches in the UI** (`update.go:ingest`)
  rather than in the adapter, so the count reflects what the view actually
  received.
- **`panel.Box` sizes by outer dimensions.** lipgloss v2's `Width`/`Height` on
  a bordered style include the border; the content is built at `w-2` by `h-3`.
  Getting this wrong was worth three rows of overflow per panel.
