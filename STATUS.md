# lazymqtt — implementation status

Working notes for picking this up in a fresh session. The design of record is
[`lazymqtt_plan.md`](lazymqtt_plan.md); section references below (§n) point at it.

**Last updated:** 2026-08-24
**State:** Phases 0–9 implemented and green. Phase 10 (release) not started.
Module path `github.com/Onizuka893/lazymqtt`, Go 1.27.

```
go build ./...                    # clean
go vet ./...                      # clean
go vet -tags=integration ./test/… # clean
go test -race ./...               # all packages pass
gofmt -l ./cmd ./internal ./test  # empty
```

~8,700 lines of source, ~4,700 lines of tests, 81 Go files.

`golangci-lint` is **not installed locally**, so `make lint` has not been run
since the Phase 9 files were added. CI runs it on every push; expect it to have
opinions about the new test files.

**Still not verified against a real broker.** Docker was not running in any
session so far, so the compose stack has never started and the integration
suite has never actually executed — it compiles and skips. This remains the
highest-value next step; see "What is left".

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
{store, mqtt, config, state}`); the domain packages cannot import
`charm.land/*`.

A third invariant, added in Phase 9 and guarded by a test:

- **The root `ui.Model` stays under 2 KB.** It is copied on every `Update` and
  boxed into a `tea.Model` on the way out, so a fat embedded component costs
  that much garbage per keystroke. `internal/ui/modelsize_test.go` fails if it
  grows.

Every rule above is now written down in `CONTRIBUTING.md` as well, so it is
reviewable rather than folklore.

---

## What is done

### Phase 0 — toolchain ✅
`go.mod`, directory skeleton, `Makefile`, `.golangci.yml` (with the depguard
layering rule), `.gitignore`, `internal/version` with ldflags injection and a
`debug.ReadBuildInfo` fallback for the `go install …@latest` path plus tests,
`.github/workflows/{ci,release}.yml`, `.goreleaser.yaml`, `LICENSE`,
`README.md`, `CONTRIBUTING.md`.

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
run `make dev`, then `make test-int`.

The integration suite that will verify it now exists (Phase 9 below) and is
written to skip, not fail, when no broker answers — so a green
`go test ./...` today says nothing about the adapter against a real broker.

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

### Phase 9 — hardening ✅
- **`internal/state`** (§9.4): `state.json` under `$XDG_STATE_HOME` with an
  atomic temp-file-and-rename write at mode 0600, holding the last broker
  *profile name*, the expanded-node set and recent publish payloads. A missing
  file is a first run; a corrupt or foreign-version file is logged and
  ignored. Nothing in the schema can hold a credential, and a test asserts on
  the written bytes. `config.yaml` is still never rewritten.
  - Expansion is restored lazily: at startup the tree is empty, so
    `Store.RestoreExpanded` records the wanted paths and `ensureNode` applies
    them as nodes appear.
  - Persistence happens in `main.go` after `p.Run()` returns, from the final
    model — not from a `tea.Cmd` racing `tea.Quit`, which would read the store
    off the update goroutine.
  - The remembered broker is a convenience for a bare `lazymqtt`; `--broker`
    always wins, and a profile since deleted from `config.yaml` is ignored.
- **`cmd/mqttload`** (§15.2): rate, topic cardinality, payload size, QoS,
  duration and `--pattern steady|sawtooth|retained`. Rate control is a 10 ms
  tick with a fractional carry, because a ticker per message at 10,000 msg/s
  spends more time waking up than publishing — and truncating the per-tick
  quota silently delivers a third of the requested rate. Excluded from release
  builds (`.goreleaser.yaml` builds only `./cmd/lazymqtt`).
- **`test/integration/`**, `//go:build integration`: round trip, retained
  delivery to a client that was not connected, QoS 1 and 2, ingest truncation,
  granted-QoS reporting, the §12.1 broker-restart test that asserts *messages
  flow again*, a 20,000-message retained flood against `max_topics` with a
  heap ceiling, TLS, mTLS, an untrusted certificate, and the auth-rejection
  paths against the broker on 1884. All skip cleanly with no broker.
- **Golden-file `View()` tests**: ten frames in `internal/ui/testdata`, ANSI
  stripped so they do not depend on `TERM` (§21 pitfall 19), with
  `TestFramesAreStyled` separately asserting styling is still emitted and
  `TestRenderIsDeterministic` guarding against map-order and clock leakage.
  `make golden` regenerates.
- **Chaos tests**: payloads full of ESC/OSC/C1 sequences and invalid UTF-8,
  10 MB payloads, a 1 MB single-line payload, wide and emoji topics, 200-level
  deep topics, empty topic segments, and sustained ingest under a filter on a
  40×12 terminal. Every frame is checked to fit the window by *display* width.
- **Perf pass** (§19): benchmarks for ingest, render, filtered render and
  keypress, plus a heap-ceiling test driving a minute at 5,000 msg/s over 500
  topics, and an unbounded-cardinality test. Two real regressions found and
  fixed — see "Fixed in the hardening pass".
- **`goleak`** now guards `internal/ui` and `internal/mqtt/paho5` as well as
  `internal/app`, with paho5 gaining explicit tests for closing an adapter
  that never connected and for repeated failed connect cycles (§21 pitfall
  12).
- **Tests for the two untested packages**: `internal/version` (including that
  `Resolve` never overwrites linker-injected values) and `internal/logging`
  (nothing reaches stderr, JSON to the configured file only at mode 0600, ring
  eviction, concurrent writers under `-race`, derived handlers sharing the
  ring).
- **`CONTRIBUTING.md`** records the Cobra rule, the layering rules, the
  single-writer and non-blocking-callback invariants, the model-size rule, the
  golden-file workflow and the perf targets.

### Dev environment ⚠️
`deploy/docker-compose.yml` (mosquitto with plain/TLS/**mTLS**/websocket
listeners, a second password-protected broker on 1884 whose password file is
generated at startup so no hash is committed, and a seed container publishing a
realistic retained tree), mosquitto configs, and `deploy/certs/gen.sh`.
**Never run.**

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

Run `make test-int`, which brings the stack up, runs the suite and tears it
down. Then run the tests that need certificates:

```
make certs
docker compose -f deploy/docker-compose.yml up -d
go test -tags=integration -race -run 'TLS' ./test/...
```

Expect the mTLS test to be the one that surprises you: the `require_certificate
true` listener on 8884 is new in this pass and has never accepted a connection.

Two suites in there encode assumptions about broker behaviour rather than about
our own code, so they are the ones worth reading the output of:
`overlap_test.go` (does mosquitto send one copy per matching subscription, or
one copy carrying every identifier?) and `reconnect_test.go`.

### 2. A real soak run
The heap ceiling is asserted over one simulated minute in
`internal/ui/bench_test.go`, which is enough to prove the caps bound growth but
not enough to catch slow accumulation. The §19 criterion is an hour:

```
docker compose -f deploy/docker-compose.yml up -d
go run ./cmd/mqttload --rate 5000 --topics 500 --duration 1h &
./bin/lazymqtt -b tcp://localhost:1883 -t '#'
```

Watch RSS and the goroutine count. Also worth doing under `--pattern sawtooth`,
and with a broker restart loop running alongside.

### 3. A `pprof` pass against the render path
`BenchmarkRenderFrame` is ~1 ms per frame with ~9,700 allocations against a
50 ms budget, so this is not urgent — but nearly all of those allocations are
per-row `lipgloss.Style.Render` calls, and `Store.Dirty()`/`ClearDirty()`
already exist and are wired for the §7.4 "skip rebuilding an unchanged panel"
optimisation that no panel consults yet. That is the fix if it ever matters.

### 4. Phase 10 — release
A Homebrew tap and `v0.1.0`. `.goreleaser.yaml` and both workflows are already
in place and the release pipeline is dry-run on every push, and
`docs/configuration.md` is the documented config reference, so what is left is
mostly the tag and the tap.

### 5. Documentation polish
A VHS or asciinema recording for the README. The golden frames in
`internal/ui/testdata` are a reasonable stand-in for what the app looks like in
the meantime.

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

## Fixed in the hardening pass

All four were found by tests written in this phase, not by using the app.

- **The root model was 58 KB and copied on every keystroke.** `ui.Model`
  embedded `panel.Publish` and `panel.Prompt` by value, which embed
  `textarea.Model` and `textinput.Model` — roughly 25 KB each of history rings
  and styles. Bubble Tea copies the model on every `Update` and boxes it into
  the `tea.Model` interface on return, so idle keystrokes were allocating
  58 KB each. Those two, plus `help.Model`, `theme.Palette` and `keys.Map`
  (2.6 KB of binding help strings, and the largest remaining field once the
  text inputs were out), are now pointers. The model is **1128 bytes** and
  `BenchmarkKeypress` went from ~11 µs and 6 allocs to 687 ns and 2.
  `modelsize_test.go` holds the ceiling at 2 KB. `panel.Context` carried the
  same binding table by value into every panel on every frame; it is a pointer
  there too.
- **Rendering a large payload was O(payload), not O(viewport).** The detail
  pane sanitised and split the *entire* payload, then took the visible slice —
  so a 10 MB message cost 3.3 s per frame and froze the UI on a single
  keystroke. `payloadLines` now windows the raw bytes to the visible rows
  first (`lineWindow`), and only sanitises and wraps that. Same frame, ~1 ms.
  A test asserts the cost of a 4 MB payload stays within 3× that of a 4 KB one
  rather than asserting a wall-clock bound, which would be flaky on CI.
- **Selecting a branch node showed an empty message list.** §7.3 says a
  structural node falls back to the firehose, but `selectedTopic` returned the
  branch's path, which matched no messages — so `devices` read `Messages
  devices (0) / no messages`, implying the tree was lying about its children.
  It now returns `""` for a node with no message, which is the firehose. This
  is the redundant-branches note from the previous "rough edges" list; the
  branches were not equivalent after all.
- **The Topics panel count contradicted the header.** The title counted
  *visible flattened rows*, so collapsing a branch appeared to lose topics
  while the header still said `topics 6`. It now shows `Stats().Topics`.

Both perf fixes were invisible in normal use and would have shown up as "the
app feels sluggish" long after the cause was forgettable.

## Fixed after the hardening pass

Two items the Phase 9 review turned up, both correctness rather than polish.

- **A panic on a spawned goroutine killed the process with the terminal still
  in raw mode.** §13 asks for `recover()` in every goroutine the app spawns,
  and the bridge had it, but the adapter's deferred-SUBSCRIBE goroutine did
  not — and `main`'s deferred recover cannot catch a panic on another
  goroutine. That goroutine runs on every connection-up, so it was on the
  reconnect path. All adapter goroutines now go through `Adapter.spawn`, which
  recovers, logs with a stack, and reports an `EventError` instead of dying.
- **Overlapping subscriptions double-counted every message** (§11, pitfall
  14). `#` plus `devices/+/state` means mosquitto delivers a matching message
  once per subscription, so the message list showed it twice and the counts
  and header rate were inflated — with lazymqtt looking like it was inventing
  traffic. `internal/mqtt/paho5/dedupe.go` now tags each subscription with an
  MQTT 5 subscription identifier and keeps only the copy from the
  lowest-numbered matching subscription.
  - Identifiers are per-filter and stable across reconnects, so the canonical
    choice cannot flip mid-session and make counts jump.
  - Only *active* subscriptions are candidates. Letting a rejected or
    not-yet-SUBACK'd filter be canonical would suppress *every* copy rather
    than one, turning an overlap into silence.
  - The snapshot is an `atomic.Pointer`, not mutex-guarded state, because the
    check runs in `OnPublishReceived` on the client's read loop (§21 pitfall
    2). Payloads are never compared: two identical messages published twice
    are two messages, and hiding the second is lying about the broker.
  - This forced one SUBSCRIBE packet per filter, since MQTT 5 carries the
    identifier on the packet rather than per filter. That costs a round trip
    per filter on the reconnect replay and removes the request/reason-code
    index matching, which was the fiddly part of the old code.

**The overlap behaviour is unverified against a real broker.** MQTT 5 §3.3.4
lets a broker send either one copy carrying every matching identifier or one
copy per subscription; the unit tests cover both shapes, but which one
mosquitto actually does is still an assumption.
`test/integration/overlap_test.go` is the test that settles it.

## Known rough edges

- **Four config keys parse and validate but do nothing.** Writing the reference
  in `docs/configuration.md` was what surfaced them, and they are listed there
  under "accepted but not yet implemented" rather than quietly documented as
  working:
  - `defaults.protocol` / `brokers.*.protocol` reach `Options.Protocol` and
    stop; the v5 adapter is always selected. `app.DefaultClientFactory` is the
    switch point.
  - `ui.theme` is validated against `auto|dark|light` and then never read; the
    dark palette is always used.
  - `ui.mouse` is never read.
  - `logging.redact_payloads` is vacuous — no log call at any level includes a
    payload, so there is nothing to redact. It is a latent trap: the first
    person to log a payload will not know they were supposed to check it.
- Dedupe across overlapping subscriptions is implemented for MQTT 5 only, and
  **it depends on the broker supporting subscription identifiers**. A broker
  that advertises `SubIDAvailable: false` in its CONNACK gets no identifiers
  and no dedupe, so `#` plus `devices/+/state` will double-count there. The
  3.1.1 adapter will need a different strategy; there is no identifier to key
  on, so it would have to be a payload-and-arrival-window heuristic, which is
  exactly the kind of thing that hides genuine republishes.
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
- **The state file remembers a broker but not a subscription.** Restart and
  you reconnect to the same profile with an empty tree until you subscribe
  again. Persisting the subscription set is a one-field change to
  `state.State`, deliberately left out because auto-subscribing to `#` on
  launch is a surprising amount of traffic to invite without being asked.
- `internal/ui/testdata` holds ANSI-stripped frames, so the golden tests would
  not catch a change that only affects colour. `TestFramesAreStyled` asserts
  styling is emitted at all, which is a weaker guarantee than it sounds.

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
