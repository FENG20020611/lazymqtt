# lazymqtt — implementation status

Working notes for picking this up in a fresh session. The design of record is
[`lazymqtt_plan.md`](lazymqtt_plan.md); section references below (§n) point at it.

**Last updated:** 2026-08-24
**State:** Phases 0–9 implemented, green, and verified against real brokers in
CI. Phase 10 (release) in progress. Module path
`github.com/Onizuka893/lazymqtt`, `go 1.25.0` in `go.mod`.

The `go` directive is the **minimum**, not the toolchain in use. It was 1.27
purely because that is what the machine had installed, which broke the
`oldstable` CI matrix entry — 1.25 is the real floor (a dependency needs it;
1.24 fails `go mod tidy`), and 1.25 is what the matrix now proves.

```
go build ./...                    # clean
go vet ./...                      # clean
go vet -tags=integration ./test/… # clean
go test -race ./...               # all packages pass
gofmt -l ./cmd ./internal ./test  # empty
```

~8,700 lines of source, ~4,700 lines of tests, 81 Go files.

`golangci-lint` and `goreleaser` are **not installed locally**, so `make lint`
has only ever run in CI, and the release pipeline is exercised by the
`release-dry-run` job (`goreleaser release --snapshot`) on every push rather
than by hand.

**Verified against real brokers — in CI, not locally.** The integration job
brings the compose stack up and runs the whole suite with `-race`: plain, TLS,
mTLS, the password-protected broker on 1884, the broker-restart reconnect test,
the overlap/dedupe tests and the 20,000-message retained flood. It is green.
Docker has still never run on the development machine, so `make dev` and
`make test-int` remain unexercised as *commands*, though everything they run is
proven in CI.

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

### Phase 3 — MQTT adapter (headless) ✅
`internal/mqtt/paho5/adapter.go` implements the port on autopaho: truncation at
ingest, `Seq` assignment, non-blocking send with a drop counter, callbacks
mapped to events, **re-subscribe in `OnConnectionUp`** (not at startup — §12.1),
terminal classification of auth/TLS failures so a rejected credential is never
retried in a loop, paho's loggers wired into slog.

`internal/mqtt/mqtttest` is a programmable in-memory `Client` used by everything
above the port.

`--headless` streams one line per message to stdout.

**Verified against a real broker in CI.** The integration suite (Phase 9 below)
exercises the adapter against mosquitto over plain TCP, TLS, mTLS and
password auth, including the §12.1 reconnect case that asserts *messages flow
again* after a broker restart. Note that the suite is written to skip, not
fail, when no broker answers — so a green `go test ./...` on a machine with no
broker still says nothing. The signal is the `integration` CI job.

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
- `internal/ui/theme`: a dark and a light palette built from one colour set,
  selected by `ui.theme` or from the terminal's reported background.
- `internal/ui/mouse.go`: opt-in wheel scrolling and click-to-focus.
- JSON payloads pretty-printed and syntax-highlighted in the detail pane —
  see "JSON pretty-printing in the detail pane" below.
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
when shell completions are wanted; that rule is recorded in `CONTRIBUTING.md`.

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

### Dev environment ✅
`deploy/docker-compose.yml` (mosquitto with plain/TLS/**mTLS**/websocket
listeners, a second password-protected broker on 1884 whose password file is
generated at startup so no hash is committed, and a seed container publishing a
realistic retained tree), mosquitto configs, and `deploy/certs/gen.sh`.

**Verified in CI rather than locally.** The integration job brings this stack
up on every push, and the first runs exposed three real bugs that had been
latent since the stack was written — two file-permission bugs in the compose
setup and a data race in the adapter's shutdown path (see "Fixed after the
hardening pass"). All four listeners now accept connections and the suite
passes against them. Docker has never run on the development machine, so
`make dev` and `make test-int` remain unexercised as commands.

---

## What is left

Ordered by value.

### 1. A real soak run — the last MVP acceptance criterion
Everything else in §19 is proven. The app has now been driven by hand for an
extended local session against the dev broker, roughly 15,000 messages in
total, with no misbehaviour — which is real evidence that it works, but not
this criterion: 15,000 messages over a long session is a few messages a second,
and the failure this test is looking for is what sustained throughput does to
memory over an hour.

The heap ceiling is asserted over one *simulated* minute in
`internal/ui/bench_test.go`, which is enough to prove the caps bound growth but
not enough to catch slow accumulation. The §19 criterion is an hour at
5,000 msg/s over 500 topics, under 100 MB RSS, with input latency you cannot
perceive:

```
docker compose -f deploy/docker-compose.yml up -d
go run ./cmd/mqttload --rate 5000 --topics 500 --duration 1h &
./bin/lazymqtt -b tcp://localhost:1883 -t '#'
```

Watch RSS and the goroutine count. Also worth doing under `--pattern sawtooth`,
and with a broker restart loop running alongside. This needs Docker running on
a real machine — CI is the wrong place for an hour-long soak.

### 2. Phase 10 — release
`.goreleaser.yaml` and both workflows are in place, the release pipeline is
dry-run on every push, and `docs/configuration.md` is the documented config
reference. What is left is the Homebrew tap and the tag; see "Phase 10
progress" below.

### 3. Not blocking
- A `pprof` pass against the render path. `BenchmarkRenderFrame` is ~1 ms per
  frame with ~9,700 allocations against a 50 ms budget, so this is not urgent —
  but nearly all of those allocations are per-row `lipgloss.Style.Render` calls,
  and `Store.Dirty()`/`ClearDirty()` already exist and are wired for the §7.4
  "skip rebuilding an unchanged panel" optimisation that no panel consults yet.
  That is the fix if the soak ever flags it.
- The rough edges below: credentials in a bare `--broker` URL, an in-TUI masked
  password prompt for the broker picker, a persisted subscription set.

### 4. Deferred by design
The MQTT 3.1.1 adapter (`internal/mqtt/paho3`) with `protocol: auto`
negotiation on CONNACK `0x84`, and everything in the §20 roadmap. The port
interface and the `version`/`protocol` config field are already in place for
the 3.1.1 adapter to slot into.

---

## Phase 10 progress

Done:

- **The four inert config keys are settled** — see "Config keys, settled"
  below. This was the one item that had to land before a tag, because a
  release freezes the schema in a way a commit does not.
- **The Homebrew tap is configured.** `.goreleaser.yaml` gained a
  `homebrew_casks:` block publishing `Casks/lazymqtt.rb` to
  `Onizuka893/homebrew-tap`, so `brew install Onizuka893/tap/lazymqtt` works
  without a `brew tap` first. It is a **cask, not a formula**: Homebrew
  deprecated binary-only formulae and goreleaser deprecated `brews:` to match.
  A post-install hook clears the macOS quarantine attribute — the binaries are
  unsigned, so without it `brew install` succeeds and the first run fails with
  a Gatekeeper dialog that explains nothing.
- **`skip_upload: auto`**, so a prerelease tag never becomes what
  `brew install lazymqtt` resolves to.
- **Install docs** in `README.md`: Homebrew, `go install`, and the archive path
  with `checksums.txt` verification and the `xattr` note.
- **`docs/releasing.md`**: the runbook — tap setup, the token, the pre-tag
  checklist, how to verify a release, and what to do about a bad tag.
- **`make release-check`** (`goreleaser check`), and the config now validates
  clean with no deprecation warnings against goreleaser v2.
- **`docs/demo.gif` is recorded and in the README** (§Phase 9's "done when",
  finally). `make demo` re-records it from `docs/demo.tape`, and refuses to
  run when the broker is not answering — with nothing listening the tape
  records a perfectly good GIF of an empty app stuck on "reconnecting…", and
  you only find out when you open the result. The tape passes
  `--config docs/demo-config.yaml` for the same class of reason: left to the
  discovery chain it picks up whoever's personal config, and the first real
  run recorded the parse error from a stale `redact_payloads` key.
  The version line in the help overlay reads a dirty commit hash rather than
  a tag; worth re-recording once `v0.1.0` exists.

Left:

- **Create the tap repository.** A public `Onizuka893/homebrew-tap`. It can be
  empty; the first release commits the cask into it.
- **Add the `HOMEBREW_TAP_GITHUB_TOKEN` secret** — a fine-grained PAT with
  Contents: write on the tap repo only. The default `GITHUB_TOKEN` cannot push
  to another repository. Missing, the release publishes and *only* the cask
  step fails, which is the annoying half-done state worth avoiding.
- **Tag `v0.1.0`.** Ideally after the soak run, since that is the §19
  acceptance criterion and the README claims bounded memory.
- **Verify both install paths** print the tagged version rather than `dev`.

---

## Config keys, settled

The four keys that parsed and validated but did nothing. Each was either wired
up or removed, because a release freezes the schema.

- **`ui.theme` — implemented.** `theme.Palette` is now built from a colour set
  rather than hard-coded, and there are two: `theme.Dark` and `theme.Light`.
  The light set is not the dark one inverted — the pastels that read well on
  near-black turn to mush on white, so every hue is darkened. `auto` picks from
  `tea.BackgroundColorMsg`, which the terminal answers asynchronously: a light
  terminal shows a dark frame or two before it corrects itself, and a terminal
  that never answers stays dark.
- **`ui.mouse` — implemented, shallowly.** `View` asks for
  `tea.MouseModeCellMotion` only when the key is set; the wheel scrolls the
  focused panel (or the open overlay, never the panel behind it) and a left
  click focuses the panel under the pointer. It stays **off by default**
  because mouse reporting takes drag-select and middle-click paste away from
  the terminal, which is not a trade to make on someone's behalf.
  `internal/ui/mouse.go`, with a test pinning the hit-test to the same
  arithmetic `ComputeLayout` uses.
- **`protocol` — now refused rather than ignored.** `3.1.1` is a validation
  error naming the reason. Accepting it and connecting with v5 anyway was the
  worst of the options: the connection succeeds, so nothing looks wrong, and
  the user concludes their broker speaks v5. `auto` and `5` are accepted, and
  `auto` stays correct when a 3.1.1 adapter lands.
- **`logging.redact_payloads` — removed.** It was vacuous, and a latent trap:
  the first person to log a payload would not know they were supposed to check
  it. Payloads are simply never logged, which is now stated in the reference
  instead of implied by a key. Because `DisallowUnknownField()` means a
  removed key is a parse error, `config.removed` maps it to a hint explaining
  what happened — a bare "unknown field" reads like a typo.

---

## JSON pretty-printing in the detail pane

Was a manual, colourless `F` press; is now automatic and highlighted (§20 puts
this in v0.2 — it was pulled forward).

- **Automatic.** `Model.Update` wraps the real update and calls
  `ensureFormatted` once, so whatever moved the selection — a keypress, an
  ingest batch, a resume — the request is issued from one place rather than
  from every branch that could have moved it. `F` is now a session-wide
  preference toggle rather than a one-shot.
- **Off the UI goroutine**, as it always was: a 14 MB payload takes tens of
  milliseconds to indent, which is a visible stall inside `Update`. The
  in-flight sequence number stops a goroutine being spawned per frame while
  the selection sits still.
- **Only the cheap test runs on the UI goroutine.** `app.MaybeJSON` looks at
  the first non-space byte; the full `json.Valid` happens on the command
  goroutine. Under `follow` the selected message changes twenty times a
  second, and validating a megabyte at that rate costs frames.
- **Capped at 1 MiB for the automatic path** (`maxAutoFormatBytes`). Indenting
  allocates roughly twice the payload and the result is held in the model, so
  the cap is about memory rather than time. An explicit `F` ignores it.
- **A non-JSON payload is not copied into the model.** `FormatCmd` returns an
  empty string in that case; returning the payload verbatim would have the UI
  hold a second copy of every message it displays.
- **Highlighting is per visible line**, in `internal/ui/panel/jsoncolor.go`,
  for the same reason `payloadLines` windows the payload: the pane must cost
  what it displays, not what it holds. The lexer carries exactly one bit
  across a line boundary — whether a soft-wrapped string is still open — and
  that bit is dropped when wrapping is off, where an unterminated string means
  the line was truncated rather than continued.
- Keys and string values get different colours, because that is the whole
  point; a test asserts they never converge, and another asserts highlighting
  changes styling only, never the text, since the pane has already truncated
  each line to the panel width.
- The golden frames are ANSI-stripped and so cannot see any of this, which is
  why `TestJSONIsHighlighted` exists.
- **Cost:** `BenchmarkKeypress` went from 687 ns / 2 allocs to ~1.1 µs / 4,
  because `ensureFormatted` resolves the selected message on every Update. That
  is 2% of the 50 ms frame budget and buys the guarantee that no branch can
  forget to request formatting; if it ever matters, the fix is to compare the
  selection anchor rather than resolve the message.

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

- **The dev stack could never have started its TLS or auth listeners.** Two
  bugs of the same shape, both found the first time CI actually ran the compose
  file, and both invisible on macOS because Docker Desktop virtualises file
  ownership:
  - The auth broker generated its password file as root at mode 0700, then
    mosquitto dropped privileges to the `mosquitto` user and could not read it
    — `Unable to open pwfile`, container exits. Ownership now follows the
    process rather than the script that created the file.
  - `deploy/certs/gen.sh` left keys at openssl's default 0600, owned by the
    developer, and mosquitto reads them through a read-only mount as uid 1883.
    Unreadable keys mean it starts *without* its TLS listeners, which is the
    worse failure: the broker looks healthy and only the TLS tests fail. The
    script now marks them 0644, which is fine for throwaway certificates that
    `.gitignore` keeps out of the repository.

**The overlap behaviour is now verified against mosquitto.** MQTT 5 §3.3.4
lets a broker send either one copy carrying every matching identifier or one
copy per subscription; the unit tests cover both shapes, and
`test/integration/overlap_test.go` — green in CI, including across a broker
restart — confirms the dedupe holds against a real broker either way.

- **A data race on shutdown, found by the integration job.** `Close` closed the
  `messages` and `events` channels while paho's own callback goroutines could
  still be inside `emit`/`onPublishReceived`. `a.wg` does not track those
  goroutines — paho owns them — so `wg.Wait()` never covered the case, and it
  was a send-on-closed-channel panic waiting to happen, not merely a race
  report. A `chanMu sync.RWMutex` plus a `chansClosed` flag now guards the
  channels' lifetime: senders take the read lock (their sends are non-blocking,
  so they never hold it) and drop the value if the channels are already closed;
  `Close` takes the write lock after `Disconnect` returns.
- **`TestDedupeSurvivesAReconnect` published through a client that had not
  reconnected yet.** After restarting mosquitto it waited only for the
  *subscriber* to come back, then published on the publisher — which had lost
  the broker too. Intermittent `connection with the MQTT server is currently
  down`. The publisher's events are consumed by `drainEvents`, so `waitState`
  cannot be used on it; `waitConnected` polls `Adapter.Status()` instead.

## Known rough edges

- The light palette is **untested against a real light terminal**. The colours
  were chosen for contrast on paper; `theme: light` has only ever been rendered
  into a test buffer.
- Mouse support is deliberately shallow: wheel scrolling and click-to-focus.
  There is no drag, no click-to-select-a-row, and no click on the tree's
  expand arrows.
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
