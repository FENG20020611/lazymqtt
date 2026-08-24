# lazymqtt — Implementation Plan

**Status:** Plan only. No implementation code. Type and interface *signatures* appear where they clarify a contract; no function bodies.

**Target:** A keyboard-driven TUI MQTT client in Go, in the spirit of lazygit/lazydocker, distributed as a single static binary for Linux and macOS.

---

## 0. Verdict up front

**Stack recommendation: Go + Bubble Tea v2 + `eclipse-paho/paho.golang` (autopaho). Your instinct was right, with three corrections to the details.**

The three corrections:

1. **Bubble Tea v2, not v1.** Charm shipped v2.0.0 of Bubble Tea, Lip Gloss and Bubbles in February 2026 — the first breaking change in the project's history. Import paths moved to vanity domains (`charm.land/bubbletea/v2`), `View()` now returns a `tea.View` struct instead of a string, key handling split into `KeyPressMsg`/`KeyReleaseMsg`, and the renderer was rewritten ("Cursed Renderer", ncurses-style diffing). Several v2 features map directly onto requirements in your brief — see §1.
2. **`paho.golang`/autopaho, not `paho.mqtt.golang`.** These are two different libraries with confusingly similar names. Details and the trade-off in §1.
3. **`gopkg.in/yaml.v3` is archived.** Use `goccy/go-yaml`. Details in §1.

The critical architectural decision in this whole document is in §7: **never send one `tea.Msg` per MQTT message.** Everything else is ordinary engineering; that one choice decides whether the app survives a 10k msg/s topic.

---

## 1. Technology choices and exact libraries

### 1.1 Language: Go

Correct for this. Single static binary, `CGO_ENABLED=0` cross-compilation, mature MQTT clients, goroutines map naturally onto "network loop + UI loop", and the Charm ecosystem is the strongest TUI toolkit in any language right now for *polished* output.

The one real argument against Go is GC pressure under a message firehose. That is manageable and addressed in §12 (bounded ring buffers, payload truncation, pointer reuse, no per-frame allocation in the hot render path). It is not a reason to reach for Rust — see §1.6.

Minimum version: **Go 1.24** (generics for the ring buffer, `for range int`, modern `slices`/`maps`, `log/slog` mature). Go 1.25+ preferred.

### 1.2 TUI: Charm v2

| Library | Module path | Why |
|---|---|---|
| Bubble Tea | `charm.land/bubbletea/v2` | The Elm Architecture. Update is a pure function of `(model, msg)`, so the whole app is testable without a terminal. |
| Lip Gloss | `charm.land/lipgloss/v2` | Styling, borders, layout composition (`JoinHorizontal`/`JoinVertical`), width-aware truncation. In v2 it is *pure* — Bubble Tea owns terminal I/O and Lip Gloss no longer queries the terminal, which removes a whole class of startup race. |
| Bubbles | `charm.land/bubbles/v2` | `viewport`, `textinput`, `textarea`, `list`, `table`, `help`, `key`, `spinner`. |

v2 features that matter specifically here:

- **`tea.SetClipboard` / `tea.ReadClipboard` (OSC52).** Your `y` (copy payload) binding works over SSH, with no `xclip`/`pbcopy` dependency and no cgo. This is a real win — most TUI clipboard implementations shell out.
- **`tea.WithWindowSize(80, 24)`.** Deterministic sizing in tests without mocking a terminal. Directly enables the golden-file view tests in §15.
- **`tea.WithColorProfile(...)`.** Force ASCII/no-color in tests so golden files don't break depending on whose terminal ran them.
- **Declarative `tea.View`.** Alt-screen, mouse mode, window title, cursor and keyboard enhancements are struct fields on the value returned by `View()`, not commands fired at startup. Fewer ordering bugs.
- **Cursed Renderer.** Cell-diffing plus synchronized output (mode 2026) and Unicode mode 2027. This is what makes a 60 Hz-capable full-screen TUI cheap; you get it for free.
- **Progressive keyboard enhancements.** `shift+enter`, `ctrl+m` etc. become bindable in Ghostty/Kitty/Alacritty/WezTerm/iTerm2/foot. Useful for a richer keymap later, with graceful fallback via `tea.KeyboardEnhancementsMsg`.

**What Bubbles does *not* give you: a tree component.** There is no `bubbles/tree`. The topic tree is custom code — a flattened-slice model rendered into a `viewport`. This is roughly 200–300 lines and is described in §8.3. Budget for it; it is the single largest bespoke UI component in the project.

### 1.3 MQTT client: `github.com/eclipse/paho.golang/autopaho`

Two Eclipse Paho Go libraries exist and they are easy to confuse:

| | `eclipse-paho/paho.mqtt.golang` | `eclipse-paho/paho.golang` |
|---|---|---|
| Protocol | MQTT 3.1 / 3.1.1 only | MQTT 5.0 only |
| Version | v1.5.x (stable, v1 API) | v0.23.0 (Sep 2025), still v0.x |
| Reconnect | `SetAutoReconnect`, `SetConnectRetry` | `autopaho` wrapper |
| API style | Token-based, pre-context Go | `context.Context` throughout |
| Maintenance | Active, low-volume | Active, low-volume |
| License | EPL-2.0 / EDL-1.0 | EPL-2.0 / EDL-1.0 |

Notes that shaped the recommendation:

- `paho.golang` gained **real QoS 1/2 session persistence in v0.20**. Before that the library "effectively operated at QOS0" — QoS 1/2 appeared to work but delivery guarantees were not honoured. Any advice you find predating v0.20 is stale.
- `autopaho` is the maintainers' explicit recommendation for new users. It owns the connection lifecycle: initial connect, backoff, reconnect, and an `OnConnectionUp` callback that fires on *every* successful connection — which is exactly the right hook for re-subscribing (§13).
- `ClientConfig.Router` is deprecated in favour of `OnPublishReceived`. Use `OnPublishReceived` from day one.
- The v0.x version number is the main risk. Mitigated by the port interface in §6 — the adapter is one file, and pinning a known-good version costs nothing.

**MQTT 3.1.1 support is not optional for a general-purpose client tool.** You will point this at brokers that reject a v5 CONNECT. So:

- **MVP ships the v5 adapter** (`autopaho`).
- **Phase 8 adds a v3.1.1 adapter** (`paho.mqtt.golang`) behind the same interface, selected by `version: 3.1.1` in the broker profile, with `auto` attempting v5 and falling back on CONNACK reason code `0x84` (unsupported protocol version).

This is the one place where an abstraction genuinely earns its keep — you are not abstracting "in case", you are abstracting because two concrete implementations are already on the roadmap and the config schema you specified already has a `version` field.

**Rejected alternatives:** `gmqtt`, `mochi-mqtt` (both brokers, not clients); `goiiot/libmqtt` (unmaintained); wrapping `mosquitto_sub` (you ruled it out and you were right — subprocess lifecycle, no structured metadata, no v5 properties, an external runtime dependency in a "single binary" project).

### 1.4 Configuration: `github.com/goccy/go-yaml`

`gopkg.in/yaml.v3` (`go-yaml/yaml`) **was archived in 2025** and is unmaintained. Two successors exist: `go.yaml.in/yaml/v3` (a drop-in compatible fork under the yaml.org umbrella) and `goccy/go-yaml` (MIT, written from scratch, v1.19.x).

Use **`goccy/go-yaml`**. It passes substantially more of the YAML test suite than yaml.v3, and two features pay for themselves in a config-driven tool:

- **Source-context error formatting.** A typo in `config.yaml` produces an error pointing at the offending line with context, rather than "line 14: cannot unmarshal". For a tool whose first-run experience is "edit this YAML", that is a UX feature.
- **`yaml.DisallowUnknownField()`.** Catches `hostname:` when you meant `host:` instead of silently ignoring it. Enable this.

If you would rather minimise churn, `go.yaml.in/yaml/v3` is the conservative choice — same API as before, maintained. Either is fine; do not use the archived path.

### 1.5 The rest

| Need | Choice | Why not something else |
|---|---|---|
| CLI parsing | stdlib `flag` + hand-rolled subcommand dispatch | The MVP has four flags. Cobra pulls in a dependency tree for a help formatter you will not use. **Rule: adopt `spf13/cobra` when you cross 4 real subcommands or want generated shell completions.** Not before. |
| Logging | stdlib `log/slog` | Structured, zero-dependency, `slog.Handler` is an interface so the in-app log ring buffer (§16) is ~40 lines. `zerolog`/`zap` buy throughput you do not need. |
| JSON detection/format | stdlib `encoding/json` | `json.Indent` for pretty-printing, `json.Valid` for detection. |
| Testing | stdlib `testing` + `github.com/google/go-cmp` | `go-cmp` for readable struct diffs. Skip testify — `cmp.Diff` plus plain `if` covers it. |
| Goroutine leak detection | `go.uber.org/goleak` (test-only) | A long-running app that reconnects repeatedly leaks goroutines silently. This catches it in CI. Test-only dependency, does not ship in the binary. |
| Release | `goreleaser` (CI tool, not a dependency) | Cross-compilation, checksums, GitHub release, Homebrew tap. |
| Lint | `golangci-lint` + `gofumpt` | |

**Deliberately not included:** Viper (config is one file, `os.ReadFile` + unmarshal is enough), testify, an event bus library, a DI container, `testcontainers-go` in the default test path (heavy; docker-compose plus an env var is simpler — see §16).

Total runtime dependency count: **6 modules** (bubbletea, lipgloss, bubbles, paho.golang, go-yaml, plus their transitive Charm internals). That is a defensible dependency footprint for an open-source tool.

### 1.6 Honest evaluation: is this actually the right stack?

You asked me to challenge the premise rather than rubber-stamp it. Three alternatives deserve a real hearing.

**gocui — what lazygit and lazydocker actually use.**
Worth stating plainly, since they are your stated inspiration: neither uses Bubble Tea. Both use `jesseduffield/gocui`, a fork of a fork. gocui gives you named views, z-ordering and mouse handling out of the box, and its imperative model ("write into view X") is arguably a more natural fit for multi-panel layouts than Elm's "re-render the world" approach.

*Rejected because:* it is effectively a single-maintainer fork maintained for lazygit's needs; the imperative model makes unit-testing UI logic hard (state lives in view buffers, not in a value you can assert on); and it directly conflicts with your requirement that "MQTT network events should not directly manipulate UI state" — in gocui that separation is a convention you must enforce by hand, whereas in Bubble Tea it is enforced by the type system. You cannot mutate the model except by returning a new one from `Update`.

**tview / tcell.**
The strongest technical argument against Bubble Tea for *this specific app*: **tview ships a `TreeView` widget.** The topic tree is your biggest bespoke component, and tview would hand it to you along with `Flex`, `Pages`, `Modal` and `Form` — the publish dialog and broker picker become near-free.

*Rejected because:* callback-driven widgets mean UI state is scattered across closures, so the testing story is much weaker; you must marshal every cross-goroutine update through `Application.QueueUpdateDraw`, which is a manual discipline that Bubble Tea's message queue gives you structurally; the aesthetic ceiling is lower (tview redraws whole widgets, and its styling API is far less expressive than Lip Gloss); and tview's `TreeView` is designed for filesystem-scale trees, not 5,000 nodes churning at 10k updates/sec — you would likely end up replacing it anyway.

Net: tview saves you ~300 lines up front and costs you testability and polish for the life of the project. Not worth it.

**Rust + ratatui.**
Higher performance ceiling, no GC pauses, `rumqttc` is a good client. Genuinely the better choice if you were targeting 100k msg/s.

*Rejected because:* you are not. A well-written Go TUI with a coalescing ingest path and a diffing renderer handles tens of thousands of messages per second with headroom to spare, and the bottleneck at those rates is the terminal and the human eye, not the runtime. Meanwhile Go gives you faster iteration, easier contribution (open-source friendliness was an explicit constraint), and — decisively — the Charm ecosystem has no equal in Rust for *polish*. ratatui is excellent and lower-level; Lip Gloss is higher-level and prettier.

**Conclusion: Bubble Tea v2 + autopaho. Proceed.** The two things you must get right that this stack does *not* give you for free are the tree component (§8.3) and the event coalescer (§7). Both are in the plan.

---

## 2. Project structure

```
lazymqtt/
├── cmd/
│   ├── lazymqtt/
│   │   └── main.go                  # flag parsing, subcommand dispatch, wiring, panic recovery
│   └── mqttload/
│       └── main.go                  # dev-only load generator (§16)
├── internal/
│   ├── mqtt/                        # port + domain types. No TUI imports, ever.
│   │   ├── client.go                #   Client interface, Options, Message, Subscription
│   │   ├── state.go                 #   ConnState enum + transitions
│   │   ├── topic.go                 #   wildcard matcher, filter validation, segment split
│   │   ├── tls.go                   #   *tls.Config construction from profile
│   │   ├── paho5/adapter.go         #   autopaho implementation
│   │   ├── paho3/adapter.go         #   paho.mqtt.golang implementation (phase 8)
│   │   └── mqtttest/fake.go         #   in-memory Client for tests
│   ├── store/                       # all mutable app state. No TUI imports.
│   │   ├── store.go                 #   facade: Ingest, Stats, Snapshot
│   │   ├── tree.go                  #   TopicNode, insert, expand/collapse, flatten
│   │   ├── ring.go                  #   Ring[T] bounded buffer
│   │   ├── limits.go                #   caps, LRU topic eviction, byte accounting
│   │   └── filter.go                #   topic/payload filter predicate
│   ├── app/
│   │   ├── bridge.go                #   MQTT goroutine → coalescer → tea.Program.Send
│   │   ├── events.go                #   every tea.Msg type in the app
│   │   └── commands.go              #   tea.Cmd constructors (connect, publish, subscribe…)
│   ├── config/
│   │   ├── config.go                #   schema structs + defaults
│   │   ├── load.go                  #   discovery, parse, strict-mode, file-permission check
│   │   ├── validate.go              #   semantic validation, aggregated errors
│   │   └── secret.go                #   credential resolution chain (§11)
│   ├── ui/
│   │   ├── root.go                  #   root Model: layout, focus, mode routing
│   │   ├── layout.go                #   size computation
│   │   ├── keys/keys.go             #   key.Binding registry, feeds bubbles/help
│   │   ├── theme/theme.go           #   lipgloss styles, defined once at package level
│   │   ├── sanitize/sanitize.go     #   payload → terminal-safe string (§21, pitfall 10)
│   │   └── panel/
│   │       ├── topics.go            #   tree panel
│   │       ├── messages.go          #   message list panel
│   │       ├── detail.go            #   payload + metadata viewport
│   │       ├── brokers.go           #   broker/subscription sidebar
│   │       ├── status.go            #   header + footer keybar
│   │       ├── logs.go              #   in-app log viewer
│   │       ├── publish.go           #   publish modal
│   │       ├── prompt.go            #   generic single-line input (filter, subscribe)
│   │       └── help.go              #   help overlay
│   ├── logging/
│   │   ├── logging.go               #   slog setup, file handler, fan-out
│   │   ├── ringhandler.go           #   slog.Handler → in-app ring buffer
│   │   └── paho.go                  #   adapt paho's logger interface to slog (§21, pitfall 9)
│   └── version/version.go           # ldflags-injected build info
├── test/
│   └── integration/                 # //go:build integration
├── deploy/
│   ├── docker-compose.yml
│   ├── mosquitto/mosquitto.conf
│   └── certs/gen.sh
├── .github/workflows/{ci.yml,release.yml}
├── .golangci.yml
├── .goreleaser.yaml
├── Makefile
└── README.md
```

**Rationale.**

- Everything under `internal/` so nothing accidentally becomes public API you must support. If `mqtt` and `store` prove reusable later, promote them to `pkg/` — that is a cheap one-way door you can walk through when someone asks.
- `mqtt.Message` lives *with* the port, not in a separate `domain`/`models` package. An anemic types-only package is the classic over-abstraction in Go layouts; the type and the interface that produces it belong together.
- The dependency graph is strictly acyclic and one-directional: `ui → app → {store, mqtt, config}`. `mqtt` and `store` import nothing from `ui` or `app`. **This is what makes "unit test MQTT without the TUI" true by construction rather than by discipline** — an import of `charm.land/bubbletea/v2` inside `internal/mqtt` should fail code review, and can be enforced with a `depguard` rule in `.golangci.yml`.
- `internal/app` is the seam. It knows about both `tea.Msg` and `mqtt.Message` and translates between them. It is the only package that does.

---

## 3. Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  TERMINAL                                                           │
└───────────────────────────────┬─────────────────────────────────────┘
                                │ key events / resize / paste
                                ▼
┌─────────────────────────────────────────────────────────────────────┐
│  internal/ui — Bubble Tea program (single goroutine)                │
│                                                                     │
│   root.Model                                                        │
│     ├─ focus: which panel                                           │
│     ├─ mode:  Normal | Prompt | Publish | Confirm | Help            │
│     ├─ panels: topics, messages, detail, brokers, status, logs      │
│     └─ store: *store.Store   ← pointer, mutated ONLY inside Update  │
│                                                                     │
│   Update(msg) → (Model, Cmd)          View() → tea.View             │
└──────────┬──────────────────────────────────────▲──────────────────┘
           │ tea.Cmd (user intent)                │ tea.Msg
           │ connect / subscribe / publish        │ (coalesced batches,
           ▼                                      │  conn state, errors)
┌─────────────────────────────────────────────────┴───────────────────┐
│  internal/app — the seam                                            │
│                                                                     │
│   commands.go: intent → mqtt.Client calls, wrapped in tea.Cmd       │
│   bridge.go:   ingest chan → COALESCER → program.Send(batch)        │
│                                    │                                │
│                                    └─ 50 ms tick / 512-msg flush    │
│                                       drop-oldest on overflow       │
└──────────┬──────────────────────────────────────▲──────────────────┘
           │                                      │ *mqtt.Message
           ▼                                      │ (buffered chan, cap 4096)
┌─────────────────────────────────────────────────┴───────────────────┐
│  internal/mqtt — port                                               │
│    type Client interface { Connect, Disconnect, Subscribe,          │
│                            Unsubscribe, Publish, Events }           │
│                                                                     │
│    paho5.Adapter        paho3.Adapter        mqtttest.Fake          │
└──────────┬──────────────────────────────────────────────────────────┘
           ▼
┌─────────────────────────────────────────────────────────────────────┐
│  autopaho / paho.mqtt.golang  (own goroutines: read loop, ping,     │
│                                reconnect, ack handling)             │
└──────────┬──────────────────────────────────────────────────────────┘
           ▼
        MQTT BROKER
```

**The three rules this diagram encodes:**

1. `internal/mqtt` never imports `internal/ui`. Enforced by `depguard`.
2. Nothing outside `Update` mutates `store.Store`. No mutexes needed anywhere in the store, because it has exactly one writer.
3. Paho's callbacks never block. They do one non-blocking channel send and return. Blocking a paho callback stalls the client's read loop and the broker eventually disconnects you (§21, pitfall 8).

---

## 4. Core domain models

Signatures only.

### 4.1 Message

```go
package mqtt

type Message struct {
    Seq        uint64        // monotonic, assigned at ingest; ordering + dedupe key
    Topic      string
    Payload    []byte        // truncated at ingest to Limits.MaxPayloadBytes
    Truncated  bool          // true if the original exceeded the cap
    OrigSize   int           // pre-truncation size, for display
    QoS        byte
    Retained   bool
    Duplicate  bool
    ReceivedAt time.Time     // LOCAL clock — MQTT carries no broker timestamp
    Props      *Properties   // nil for MQTT 3.1.1
}

type Properties struct {
    ContentType     string
    ResponseTopic   string
    CorrelationData []byte
    PayloadFormat   *byte              // 0 = bytes, 1 = UTF-8
    MessageExpiry   *uint32
    SubIdentifiers  []int
    User            []UserProperty
}

type UserProperty struct{ Key, Value string }
```

`Props` is a pointer so v3 messages cost one nil word, not a 100-byte zero struct — at 5,000 buffered messages that is the difference between noise and half a megabyte.

`ReceivedAt` is deliberately named to make it obvious in the UI that this is when *you* saw it, not when it was published. Label the column accordingly; users of GUI clients are routinely confused by this.

### 4.2 Connection state

```go
type ConnState int

const (
    StateDisconnected ConnState = iota
    StateConnecting
    StateConnected
    StateReconnecting
    StateFailed        // terminal: auth rejected, bad TLS — do not auto-retry
)

type ConnStatus struct {
    State       ConnState
    Broker      string
    ClientID    string
    Since       time.Time
    Attempt     int            // reconnect attempt counter
    NextRetryAt time.Time      // for the "reconnecting in 4s" countdown
    Err         error
    SessionPresent bool
    ProtoVersion   string      // "5.0" / "3.1.1", resolved after CONNACK
}
```

`StateFailed` is separate from `StateDisconnected` on purpose: authentication failures and certificate errors must *not* be retried in a loop. autopaho will happily hammer a broker that is rejecting your password; classify CONNACK reason codes and stop.

### 4.3 Subscription

```go
type Subscription struct {
    Filter    string      // may contain + and #
    QoS       byte
    Active    bool        // false = requested but not yet SUBACK'd, or failed
    GrantedQoS byte
    Count     uint64      // messages matched
    CreatedAt time.Time
    Err       error       // SUBACK failure reason
}
```

### 4.4 Topic tree

```go
package store

type TopicNode struct {
    Segment   string        // "livingroom"
    Full      string        // "home/livingroom"
    Parent    *TopicNode
    children  map[string]*TopicNode
    ordered   []*TopicNode  // sorted; rebuilt only on insert, not on read
    Expanded  bool

    Count     uint64
    Last      *Message
    History   *Ring[*Message]   // nil until first message; cap = Limits.PerTopicHistory
    FirstSeen time.Time
    LastSeen  time.Time
    Retained  bool              // last message on this topic had the retain flag

    lru       *list.Element     // position in the store's LRU list for eviction
    visible   bool              // survives the active filter (§8.4)
}
```

Holding both `children` (map, O(1) insert) and `ordered` (slice, stable render order) is the standard trade: inserts are frequent but *new topic* inserts are rare — the map hit on an existing topic touches neither. Sort on insert of a genuinely new child only.

### 4.5 Ring buffer

```go
type Ring[T any] struct { /* buf []T, head, size, cap int */ }

func NewRing[T any](capacity int) *Ring[T]
func (r *Ring[T]) Push(v T) (evicted T, didEvict bool)
func (r *Ring[T]) Len() int
func (r *Ring[T]) At(i int) T          // 0 = oldest
func (r *Ring[T]) Slice(from, to int) []T   // for viewport rendering; no copy of unused range
func (r *Ring[T]) Reset()
```

`Slice(from, to)` exists so the message list panel renders *only the visible window* — never the whole buffer. This is not premature optimisation; it is the difference between O(visible) and O(5000) per frame.

### 4.6 Store facade

```go
type Store struct { /* root *TopicNode, index map[string]*TopicNode, stream *Ring[*Message],
                       lru *list.List, stats Stats, limits Limits, dirty bool */ }

func (s *Store) Ingest(batch []*Message)       // the only write path
func (s *Store) Node(topic string) *TopicNode
func (s *Store) Flatten() []*TopicNode          // cached; invalidated by dirty
func (s *Store) Stream() *Ring[*Message]        // global firehose view
func (s *Store) Stats() Stats
func (s *Store) ClearTopic(topic string)
func (s *Store) ClearAll()
func (s *Store) SetFilter(f Filter)
```

```go
type Stats struct {
    Received, Dropped, Truncated uint64
    Topics, Nodes                int
    PayloadBytes                 int64
    RatePerSec                   float64   // EWMA over a 1s tick
    PeakRate                     float64
}
```

---

## 5. MQTT service design

```go
package mqtt

type Client interface {
    Connect(ctx context.Context) error
    Disconnect(ctx context.Context) error
    Subscribe(ctx context.Context, subs []Subscription) error
    Unsubscribe(ctx context.Context, filters []string) error
    Publish(ctx context.Context, msg PublishRequest) error
    Events() <-chan Event          // connection lifecycle, NOT messages
    Messages() <-chan *Message     // the firehose
    Close() error
}

type PublishRequest struct {
    Topic    string
    Payload  []byte
    QoS      byte
    Retain   bool
    Props    *Properties
}

type Event struct {
    Kind   EventKind   // EventConnecting, EventUp, EventDown, EventSubAck, EventError…
    Status ConnStatus
    Subs   []Subscription
    Err    error
}
```

**Design notes.**

- **Two channels, not one.** Connection events are rare and must never be dropped; messages are frequent and *must* be droppable. Merging them into one channel forces you to choose one policy for both. Keep `Events()` unbuffered-ish (cap 16, blocking send is fine, they are rare) and `Messages()` bounded with drop-oldest.
- **The adapter owns the drop policy**, not the caller. `OnPublishReceived` does a `select` with a `default:` branch that increments an atomic dropped counter. It never blocks. This is the single most important line of code in the adapter.
- **Options carry a resolved config, not a config file.** `mqtt.Options` takes a host, port, already-resolved credentials, and a ready `*tls.Config`. The adapter does no file I/O, no secret resolution, no env lookup. That keeps it trivially testable and keeps secret handling in one auditable place (§11).
- **`Subscribe` is idempotent and re-appliable.** The app keeps the desired subscription set; on every `EventUp` the app re-issues it. The adapter does not remember subscriptions on the app's behalf.
- **Publish is fire-and-forget from the UI's perspective**, wrapped in a `tea.Cmd` with a timeout context. QoS 1/2 acknowledgement arrives as an `Event`, surfaced as a toast.

`mqtttest.Fake` implements the same interface with programmable behaviour: inject messages, simulate connection drops, fail the third SUBACK, delay a PUBACK. Every test above the port layer uses it, and it runs in microseconds.

---

## 6. Event and message flow — the coalescer

**This is the section that decides whether the app works.**

### 6.1 The failure mode you must avoid

Bubble Tea processes messages **one at a time, sequentially, in a single goroutine**. Every `tea.Msg` triggers `Update`, and a re-render is scheduled. If you call `program.Send(msg)` once per MQTT message and a topic pushes 10,000 msg/s, you have asked the UI loop to execute 10,000 `Update` calls and up to 10,000 renders per second. It will not keep up, the internal message queue will back up, input latency will climb into seconds, and the app will appear frozen while pinning a core.

This is the number one way TUI MQTT clients die. Do not do it.

### 6.2 The design

```
paho goroutine                app goroutine (coalescer)          tea goroutine
──────────────                ─────────────────────────          ─────────────
OnPublishReceived
  ├ truncate payload
  ├ assign Seq (atomic)
  └ select {
      case ch <- msg:         ─┐
      default: dropped++       │  chan *Message, cap 4096
    }                          │
                              ─┘
                               drain loop:
                                 for {
                                   select {
                                   case m := <-ch: batch = append(...)
                                        if len(batch) >= 512 { flush() }
                                   case <-ticker.C (50 ms):
                                        if len(batch) > 0 { flush() }
                                   case <-ctx.Done(): return
                                   }
                                 }
                               flush() → program.Send(     ──────►  Update(BatchMsg)
                                   BatchMsg{Msgs, Dropped})           store.Ingest(batch)
                                                                      → one render
```

**Result:** at most 20 `Update` calls per second from message traffic, regardless of whether the broker is sending 10 messages or 100,000. The store absorbs the full rate; the UI absorbs a fixed rate.

50 ms (20 Hz) is the right default. Below ~30 ms you are re-rendering faster than a human perceives as "live" and paying for it. Above ~100 ms the UI feels laggy. Make it configurable (`ui.refresh_ms`) but ship 50.

The `512` batch cap bounds the size of a single `Update` — without it, a retained-message flood on `#` could hand `Ingest` a 50,000-element slice and block the UI for a visible beat.

### 6.3 Backpressure and drops

Bounded channel + drop-oldest + a visible counter. Dropping is correct behaviour for a *viewer*: you are not an ingestion pipeline, and a message you cannot render before it scrolls off is a message you did not need. What is **not** acceptable is dropping silently — the status bar shows `dropped: 1,204` in a warning colour whenever the counter is non-zero, so the user knows their view is lossy.

Alternative considered and rejected: a shared store guarded by an `RWMutex`, written by the paho goroutine, with a bare `tickMsg` triggering renders. This avoids copying batches, but reintroduces locks into the render path, makes `Update` impure, and breaks the "single writer" invariant that lets the entire store be lock-free. The batch copy is a slice of pointers — 8 bytes per message. It is not worth trading the invariant for.

### 6.4 The full message catalogue

```go
package app

// From the bridge
type BatchMsg struct { Msgs []*mqtt.Message; Dropped uint64 }
type ConnStateMsg struct { Status mqtt.ConnStatus }
type SubResultMsg struct { Subs []mqtt.Subscription; Err error }
type PubResultMsg struct { Topic string; Err error }
type ErrorMsg     struct { Err error; Fatal bool }
type LogMsg       struct { Entry logging.Entry }

// Internal UI
type TickMsg      struct { Time time.Time }   // 1 Hz: rate calc, countdown, clock
type ToastMsg     struct { Text string; Level Level; TTL time.Duration }
type ToastExpiredMsg struct { ID int }
type FormatDoneMsg struct { Topic string; Seq uint64; Rendered string }  // async pretty-print
```

`FormatDoneMsg` matters: pretty-printing a 2 MB JSON payload takes tens of milliseconds. Doing it inside `Update` freezes input. Doing it in a `tea.Cmd` and delivering the result as a message keeps the app responsive and shows a spinner in the detail pane meanwhile.

---

## 7. TUI component architecture

### 7.1 Layout

```
┌─ lazymqtt ────────────────────────────────────────────────────────────────┐
│ ● local · localhost:1883 · v5 · up 4m12s   msgs 12,482  1,204/s  drop 0  ⏸ │
├────────────────────────┬──────────────────────────────────────────────────┤
│ [1] Topics      (312)  │ [2] Messages  home/livingroom/temperature   (50) │
│                        │  09:42:34.221  q1 r-  23.5                       │
│ ▾ home           8,201 │  09:42:29.118  q1 r-  23.4                       │
│   ▾ livingroom   4,102 │  09:42:24.007  q1 r-  23.4                       │
│     ● temperature 2,051│ ▸09:42:18.995  q1 R   23.6                       │
│       humidity   2,051 ├──────────────────────────────────────────────────┤
│   ▾ kitchen      4,099 │ [3] Detail                                       │
│     temperature  2,050 │  topic     home/livingroom/temperature           │
│     humidity     2,049 │  qos 1   retain false   dup false   size 4 B     │
│ ▸ devices        4,281 │  received 09:42:18.995 (local)                   │
│                        │  ─────────────────────────────────────────────── │
├────────────────────────┤  23.6                                            │
│ [4] Subscriptions      │                                                  │
│  # (q0)          8,201 │                                                  │
│  devices/+/state (q1)  │                                                  │
├────────────────────────┴──────────────────────────────────────────────────┤
│ j/k move  ⇥ panel  s sub  p pub  / filter  y copy  ␣ pause  ? help  q quit│
└───────────────────────────────────────────────────────────────────────────┘
```

Changes from your sketch, and why:

- **A message *list*, not just the latest message.** Your original layout shows only the current payload. For anything time-varying — which is most of MQTT — you need to see the last N values to judge whether something is working. This is the highest-value change to your concept.
- **Rate and drop counters in the header.** You cannot reason about a high-frequency topic without knowing its rate, and you cannot trust the view without knowing whether it is lossy.
- **A pause indicator (`⏸`).** See §7.5.
- **Panel numbers `[1]`–`[4]`** for direct jumps, lazygit-style, alongside `Tab` cycling.
- **Per-node message counts in the tree.** This is what turns the tree from a navigation aid into a diagnostic tool — you spot the chatty topic instantly.
- **Subscriptions get their own panel** rather than being implicit. Users need to see what they asked for versus what the broker granted (QoS downgrade is common and silent otherwise).

### 7.2 Component contract

Every panel is a struct following a local convention (not a Go interface — Bubble Tea's value semantics make interfaces awkward here, and you would spend your life type-asserting):

```go
type TopicsPanel struct { /* … */ }

func (p TopicsPanel) Update(msg tea.Msg, ctx Context) (TopicsPanel, tea.Cmd)
func (p TopicsPanel) View(w, h int) string
func (p TopicsPanel) Focused() bool
```

`ctx Context` carries the shared read-only handles (`*store.Store`, theme, keymap, focus state) so panels do not each keep a copy.

The root model routes:

1. **Global keys first** — `ctrl+c`, `?`, `Tab`, `1`–`4`, `q` (mode-dependent).
2. **Mode dispatch** — if `mode != Normal`, the active overlay consumes the key. This is what stops `q` from quitting the app while you are typing a topic name into the subscribe prompt. Getting this wrong is the single most common TUI bug.
3. **Focused panel** — everything else.
4. **Broadcast** — `BatchMsg`, `WindowSizeMsg`, `TickMsg` go to all panels regardless of focus.

### 7.3 The topic tree component

The part Bubbles does not give you.

- **Model:** `*store.TopicNode` graph in the store; the panel holds only `cursor int`, `offset int`, and a cached `[]*TopicNode` flattened view.
- **Flatten** walks the tree depth-first, emitting only nodes whose ancestors are all `Expanded` and which pass the active filter. Cached in the store, invalidated by a `dirty` flag on ingest/expand/filter.
- **Render** touches only `flat[offset : offset+height]`. Never the whole tree.
- **Indent** by depth using a precomputed `[]string` of prefix strings (`"", "  ", "    ", …`) to avoid `strings.Repeat` per row per frame.
- **Cursor stability** is the subtle part: when new topics arrive and the flattened slice shifts, the cursor must stay on the *same node*, not the same index. Track the selected node's full topic string and re-resolve its index after each flatten. Without this, the selection jitters constantly under load and the app feels broken.

### 7.4 Rendering discipline

- Styles are **package-level `lipgloss.Style` values** in `internal/ui/theme`, built once. Constructing styles inside `View()` allocates on every frame.
- Use `strings.Builder` with `Grow(w*h)` for panel bodies.
- Avoid `lipgloss.Width()` in per-row loops — it does a full grapheme/width scan. Truncate with a width-aware helper once per row, not repeatedly.
- The Cursed Renderer diffs cells, so an unchanged frame costs nearly nothing to *output* — but it still costs you to *build*. Skip building panel bodies when neither the store's `dirty` flag nor the panel's own state changed, and return the cached string.

### 7.5 Pause, autoscroll, and follow

Three related behaviours that you did not list but that every serious MQTT TUI needs:

- **`Space` — pause.** Freezes the *view*, not the connection. Ingest continues into the store; the UI stops updating. Without this you cannot read a payload on a topic publishing at 50 Hz. Non-negotiable.
- **`f` — follow mode.** The tree cursor auto-jumps to whichever topic just received a message. Excellent for "what is even publishing here?" discovery, unusable at high rates — hence a toggle.
- **`a` — autoscroll.** The message list sticks to the newest entry. Automatically disabled when the user scrolls up (like a chat client), re-enabled on `G`.

---

## 8. State management

| State | Owner | Mutated by |
|---|---|---|
| Topic tree, ring buffers, stats | `store.Store` (pointer field on root model) | `Update` only, via `store.Ingest` |
| Connection status, subscriptions | root model, value fields | `Update` on `ConnStateMsg`/`SubResultMsg` |
| Focus, mode, panel cursors, filter text | root model / panel structs, value fields | `Update` |
| Config | loaded once, treated as immutable | reload command (post-MVP) |
| In-flight MQTT client | `app.App`, referenced by root model | `tea.Cmd` closures |

**Why the store is a pointer while everything else is a value.**

Bubble Tea copies the model on every `Update`. Copying a struct containing slices and maps copies *headers*, not contents — cheap. But it makes ownership ambiguous: two model values would share the same underlying map, so "returning the old model" would not actually undo a mutation. Making the store an explicit `*store.Store` is honest about the fact that it is mutable shared state with a single writer, and keeps the rest of the model genuinely value-semantic.

The invariant to state in `store.go`'s doc comment and enforce in review: **`Store` has exactly one writer, the Bubble Tea update goroutine. It contains no mutexes and needs none. Do not call any `Store` method from a `tea.Cmd` goroutine or a paho callback.**

---

## 9. Configuration design

### 9.1 Discovery order

1. `--config <path>`
2. `$LAZYMQTT_CONFIG`
3. `$XDG_CONFIG_HOME/lazymqtt/config.yaml`
4. `~/.config/lazymqtt/config.yaml` (via `os.UserConfigDir()`, which also does the right thing on macOS)

Missing config is **not an error**. `lazymqtt` with no config and no flags starts disconnected and opens the broker prompt. `lazymqtt --broker tcp://localhost:1883` works with no config file at all. First-run friction is a feature-adoption killer for CLI tools.

### 9.2 Schema

```yaml
version: 1

defaults:
  client_id: "lazymqtt-{{hostname}}-{{pid}}"   # tiny template, 3 vars
  keepalive: 30s
  connect_timeout: 10s
  clean_start: true
  protocol: auto                                # auto | 5 | 3.1.1
  subscriptions:
    - filter: "#"
      qos: 0

limits:
  max_topics: 5000              # LRU-evict least-recently-updated beyond this
  per_topic_history: 50         # messages retained per topic
  stream_history: 2000          # global firehose ring
  max_payload_bytes: 1048576    # truncate beyond this at ingest
  ingest_buffer: 4096           # channel capacity before drop-oldest

ui:
  refresh_ms: 50
  timestamp_format: "15:04:05.000"
  theme: auto                   # auto | dark | light
  start_panel: topics
  mouse: false

logging:
  level: warn                   # debug | info | warn | error
  file: ""                      # empty = no file logging. NEVER stdout.
  redact_payloads: true

brokers:
  local:
    host: localhost
    port: 1883

  production:
    host: mqtt.example.com
    port: 8883
    protocol: 5
    tls:
      enabled: true
      ca_file: ~/.config/lazymqtt/certs/ca.pem
      cert_file: ~/.config/lazymqtt/certs/client.pem
      key_file: ~/.config/lazymqtt/certs/client-key.pem
      server_name: mqtt.example.com
      insecure_skip_verify: false
    username: example
    password_cmd: "pass show mqtt/production"
    client_id: lazymqtt-ops
    keepalive: 60s
    subscriptions:
      - filter: "devices/+/state"
        qos: 1
      - filter: "$SYS/broker/load/#"
        qos: 0
```

### 9.3 Parsing rules

- `version: 1` is required and checked first, so you can migrate the schema later without guessing.
- Parse with `yaml.DisallowUnknownField()`. A typo'd key is an error with a line number, not silence.
- Per-broker settings override `defaults` field by field. Implement with pointer fields on the broker struct (`*time.Duration`) so "absent" is distinguishable from "explicitly zero".
- Validation is **semantic and aggregated** — report every problem at once, not one per run: unknown broker referenced by `--broker`; invalid topic filter (`#` not final, `+` not a whole segment); `tls.enabled: false` with port 8883 (warn); cert without key; `max_topics` below 100; both `password` and `password_cmd` set (error, ambiguous).
- Durations as strings (`30s`, `1m`), not integers. `keepalive: 30` is ambiguous; `30s` is not.

### 9.4 State file

Separate from config, at `~/.local/state/lazymqtt/state.json`: last used broker, panel sizes, expanded-node set, recent publish payloads. Written on clean exit, best-effort. **Never** contains credentials. Keeping it separate from `config.yaml` means the app never rewrites a file the user hand-edited and commented — a courtesy that hand-edited-YAML tools frequently get wrong.

---

## 10. Credentials and security

### 10.1 Resolution chain

Per broker, first match wins:

1. **`password_cmd`** — a shell command whose stdout (trailing newline trimmed) is the password. Works with `pass`, `gopass`, `op read`, `bw get`, `security find-generic-password`, `gcloud secrets`, anything. Executed with a 10s timeout; non-zero exit is a connection error surfaced in the UI. **This is the recommended path and should be what the README shows first.**
2. **`password_env: MQTT_PROD_PASSWORD`**, plus `${VAR}` expansion inside `password:`.
3. **OS keyring** (post-MVP) via `zalando/go-keyring` — Keychain / Secret Service / WinCred. **Caveat that must be documented:** on Linux this requires a running D-Bus Secret Service, which is absent on headless servers and in most SSH sessions — exactly where a TUI MQTT client gets used. This is why keyring is an *option*, not the default.
4. **Interactive prompt** at connect time, masked, held in memory only, never written anywhere.
5. **Literal `password:`** in the config file. Permitted, because forbidding it just makes people wrap the binary in a shell script with the password in it. But:

### 10.2 Hard rules

- **Refuse to load a config file containing a literal `password:` if its mode is group- or world-readable.** Print the offending path and mode and exit, exactly as OpenSSH does for private keys. This is a small amount of code that prevents a genuinely common mistake.
- **Never log credentials.** The paho adapter's debug logging must be wrapped; paho's own debug output includes CONNECT packets.
- **`logging.redact_payloads: true` by default.** Payloads can contain tokens, PII, and — for anyone in industrial IoT — commercially sensitive process data. Debug logs record topic, size, QoS, retain; not bytes.
- **`insecure_skip_verify: true` renders a persistent red banner in the header for the whole session.** Not a startup warning that scrolls away — a permanent one. People leave this on by accident and then wonder why staging worked.
- **Payload sanitisation before rendering is a security control, not cosmetics.** See §21, pitfall 10. An MQTT client renders attacker-controllable bytes directly into a terminal. Strip C0/C1 control characters and escape sequences.
- No telemetry, no phone-home, no auto-update. State it in the README.

### 10.3 TLS

`internal/mqtt/tls.go` builds `*tls.Config` from the profile: `RootCAs` from `ca_file` (falling back to the system pool), `Certificates` from cert/key pair, `ServerName` for SNI, `MinVersion: tls.VersionTLS12`. Support `mqtts://`, `ws://`, `wss://` URL schemes in `--broker`.

---

## 11. Message buffering strategy

Four independent caps, all enforced at ingest, all configurable:

| Cap | Default | Bounds |
|---|---|---|
| `max_payload_bytes` | 1 MiB | Any single message's memory |
| `per_topic_history` | 50 | Messages retained per topic |
| `max_topics` | 5,000 | Number of tree nodes |
| `stream_history` | 2,000 | The global firehose ring |

**Worst-case resident message memory** = `max_topics × per_topic_history × avg_payload`. At defaults with 200-byte payloads: 5,000 × 50 × ~250 B ≈ **62 MB**. Add tree node overhead (~150 B × 5,000 ≈ 0.8 MB) and the global stream (2,000 pointers, messages already counted). That is a hard, calculable ceiling — which is what you asked for.

**Sharing.** The global stream ring and per-topic histories both hold `*Message` pointers to the same objects. No duplication. A message is garbage only when evicted from both, which the GC handles without refcounting.

**Payload truncation happens at ingest, in the paho callback, before the message enters the pipeline.** Truncating at render time means you have already paid the memory cost. Set `Truncated: true` and `OrigSize` so the detail view can say `payload truncated: showing 1 MiB of 14 MiB`.

**Topic eviction.** An `container/list` LRU over leaf nodes, keyed by last-message time. Exceeding `max_topics` evicts the least-recently-updated leaf and prunes now-empty parents. This matters more than it sounds: topic namespaces containing UUIDs, session IDs, or timestamps (`sensors/{uuid}/data`) generate unbounded cardinality, and an MQTT viewer subscribed to `#` on such a broker will OOM within minutes without this. Most GUI MQTT clients handle this badly — it is part of why you want to replace yours.

**The retained flood.** Subscribing to `#` on an established broker delivers every retained message immediately — often tens of thousands in under a second. The coalescer's 512-message batch cap and the drop-oldest channel handle it without a stall. Worth an explicit integration test (§15).

**Dedupe.** With overlapping subscriptions (`#` and `devices/+/state`), MQTT 3.1.1 delivers the message once per matching subscription. Deduplicate at ingest on `(topic, arrival window)` or use MQTT 5 subscription identifiers to attribute a delivery to exactly one subscription. Otherwise counts double and users file bugs.

---

## 12. Error and reconnection strategy

### 12.1 Delegate to autopaho, translate to state

autopaho already implements connect-with-backoff and automatic reconnect. Do not reimplement it. Wire its callbacks:

| autopaho callback | Emitted `mqtt.Event` |
|---|---|
| `OnConnectionUp` | `EventUp` + **re-issue the full desired subscription set** |
| `OnConnectError` | `EventError` → `StateReconnecting` with attempt count |
| `OnServerDisconnect` | `EventDown` + DISCONNECT reason code |
| `OnClientError` | `EventDown` |

**Re-subscribing belongs in `OnConnectionUp`, not at startup.** It fires on every successful connection, including reconnects. Subscribing once after the initial connect means that after any network blip you are connected, the UI says "Connected", and no messages ever arrive again. This is the classic MQTT reconnection bug and it is subtle because everything *looks* fine.

With `clean_start: false` and a session expiry, the broker may report `SessionPresent: true`, in which case subscriptions survived and re-subscribing is redundant but harmless. Re-issue anyway — idempotent and cheap.

### 12.2 Classify, then decide whether to retry

Not all failures deserve a retry loop:

| Cause | Action |
|---|---|
| Network unreachable, connection refused, timeout | Retry with backoff → `StateReconnecting` |
| CONNACK `0x85` client ID invalid, `0x86` bad credentials, `0x87` not authorised | **Stop.** → `StateFailed`, prompt for credentials |
| CONNACK `0x84` unsupported protocol version | Retry once with 3.1.1 if `protocol: auto`, else `StateFailed` |
| TLS certificate verification failure | **Stop.** → `StateFailed` with the specific `x509` error |
| DNS resolution failure | Retry, slower backoff |

Backoff: 1s → 2s → 4s → 8s → 16s → 30s cap, with ±20% jitter. The UI shows `reconnecting… attempt 4, next try in 7s` with a live countdown driven by the 1 Hz `TickMsg`, and `r` forces an immediate retry.

### 12.3 Error surfacing

Three tiers:

1. **Toast** — transient, bottom-right, 4s TTL. Publish succeeded, subscription granted at a lower QoS, payload copied.
2. **Status bar** — persistent condition. Connection state, drop counter, `insecure_skip_verify` warning.
3. **Logs panel** (`ctrl+l`) — full history with timestamps and levels, scrollable, `y` to copy. Backed by the same slog ring buffer as `--log-file`.

Nothing writes to stdout or stderr while the alt screen is active. Ever.

`recover()` in every goroutine the app spawns, converting the panic into a `Fatal: true` `ErrorMsg` rather than a crash that leaves the terminal in raw mode with the alt screen active. `main` also recovers, calls `program.Kill()` to restore the terminal, *then* prints the stack trace.

---

## 13. CLI design

### 13.1 MVP

```
lazymqtt [flags]

  --broker, -b <name|url>   broker profile name, or a URL (tcp://host:1883, mqtts://…)
  --config, -c <path>       config file path
  --topic,  -t <filter>     subscribe on connect (repeatable, overrides profile)
  --log-file <path>         debug log destination
  --debug                   log level = debug
  --version                 print version, commit, build date
  --help
```

`--broker` accepting *either* a profile name or a bare URL is a deliberate affordance: `lazymqtt -b tcp://10.0.0.5:1883` needs zero setup, which is what you want when you are debugging someone else's broker at 2am.

### 13.2 Post-MVP subcommands

```
lazymqtt brokers                        list configured profiles
lazymqtt config init                    write a commented starter config (mode 0600)
lazymqtt config check                   validate and print resolved config
lazymqtt pub <topic> <payload> [-q -r]  one-shot publish, stdin if payload is "-"
lazymqtt sub <filter> [-q --json]       one-shot subscribe, line-delimited to stdout
```

`pub`/`sub` are worth building *specifically because they are scriptable* — they turn the project from a TUI into a toolkit and reuse `internal/mqtt` with zero UI code. `sub --json` emitting NDJSON makes it pipeable into `jq`, which is a genuinely useful thing to have.

### 13.3 Argument parsing

stdlib `flag` with a subcommand switch on `os.Args[1]`. Each subcommand gets its own `flag.FlagSet`. This is ~60 lines and has no dependencies.

The trigger to adopt Cobra: **more than four subcommands, or a request for shell completions.** Write that rule in `CONTRIBUTING.md` so the decision is not relitigated in every PR.

---

## 14. Testing strategy

### 14.1 Layers

**Pure domain — fast, exhaustive, no I/O.**

- `mqtt.MatchTopic(filter, topic)` — table test with every example from the MQTT 3.1.1 and 5.0 specs: `sport/#` matches `sport`; `sport/+` does not match `sport`; `+/tennis/#`; `$SYS` topics are not matched by `#` or `+` at the first level. This function is small and gets subtly wrong constantly; test it hard. A fuzz target (`FuzzMatchTopic`) is cheap and worthwhile.
- `mqtt.ValidateFilter` — `#` only as the final segment, `+` only as a whole segment, empty segments legal, length limits.
- `store.Ring[T]` — wraparound, eviction reporting, `Slice` across the wrap boundary, capacity 1, capacity 0.
- `store.Tree` — insert creating intermediate nodes, insert into existing, flatten with collapsed ancestors, flatten under a filter, LRU eviction pruning empty parents, cursor-stability helper.
- `store.Store.Ingest` — caps enforced, byte accounting, stats.

**Port layer — with `mqtttest.Fake`.**

- Subscription set is re-issued on every `EventUp`.
- `StateFailed` after an auth-rejection CONNACK, and *no* retry.
- Backoff schedule with an injected clock.
- Message channel drops oldest and increments the counter when full.

**Bridge/coalescer — with an injected ticker channel.**

- N messages in → ceil(N/512) or one-per-tick batches out.
- No `Send` when the batch is empty.
- Context cancellation drains and stops.
- `goleak.VerifyNone` after shutdown.

**TUI — `Update` is a pure function, so this is easier than it sounds.**

- Feed `tea.KeyPressMsg` sequences, assert on model state: `Tab` cycles focus; `j`/`k` move the cursor and clamp at bounds; `/` enters prompt mode and `q` then types a literal `q` instead of quitting; `Esc` exits every mode.
- Golden-file `View()` tests at fixed sizes (80×24, 120×40, 40×20). **Force the colour profile** (`tea.WithColorProfile(colorprofile.Ascii)`) or golden files will differ between a CI runner and a developer's iTerm2. This trips people up constantly.
- Cursor stability: ingest 100 new topics that sort before the selected node, assert the selection still points at the same topic.
- `charmbracelet/x/exp/teatest` for a handful of full-program tests. Note it is explicitly experimental — use it for smoke tests, not as the backbone.

### 14.2 Integration tests

`//go:build integration`, run against a real Mosquitto, `MQTT_BROKER_URL` from the environment (default `tcp://localhost:1883`).

- Connect, subscribe, publish, round-trip a message.
- Retained message delivered on subscribe.
- QoS 1 and 2 round-trip.
- **Reconnect:** kill the broker container, assert `StateReconnecting`, restart it, assert `StateConnected` *and* that messages flow again — this is the test that catches the re-subscribe bug in §12.1.
- **Retained flood:** publish 20,000 retained messages, subscribe to `#`, assert the app ingests without deadlock and respects `max_topics`.
- **Sustained rate:** `mqttload` at 10,000 msg/s for 30s; assert bounded memory (`runtime.ReadMemStats` under a threshold) and that the drop counter behaves as designed.
- TLS with the generated dev CA, and mTLS with a client certificate.

### 14.3 CI gates

`go test -race ./...`, `go vet`, `golangci-lint run`, `govulncheck ./...`, integration suite against a Mosquitto service container, `goleak` in the bridge tests. Coverage reported but **not gated on a number** — coverage targets on a TUI project produce test theatre around `View()` rather than tests of the things that break.

---

## 15. Local development environment

### 15.1 docker-compose

`deploy/docker-compose.yml`:

- **`mosquitto`** — `eclipse-mosquitto:2`, anonymous listener on 1883, TLS on 8883 using certs from `deploy/certs/`, websockets on 9001, `$SYS` enabled. Config mounted read-only.
- **`mosquitto-auth`** — a second instance on 1884 with a password file, for testing the credential chain.
- **`seed`** — a one-shot container publishing ~200 retained messages across a realistic topic hierarchy (`home/*`, `devices/*`, `factory/line1/*`) so a fresh checkout shows a populated, interesting tree within seconds of `make dev`. Small detail, disproportionate effect on how the project feels to a new contributor.

### 15.2 `cmd/mqttload`

A dev-only load generator, in Go, in this repo:

```
mqttload --rate 10000 --topics 500 --payload 256 --qos 0 --duration 60s
mqttload --pattern sawtooth --rate 5000       # bursty, to exercise the coalescer
mqttload --pattern retained --count 20000     # retained flood
```

Better than a `mosquitto_pub` loop: precise rates, controllable topic cardinality, reproducible payload sizes, and it doubles as the driver for the performance tests in §14.2. Excluded from release builds.

### 15.3 Makefile

```
make build           # CGO_ENABLED=0, -trimpath, version ldflags
make run             # build + run against the local broker
make dev             # compose up -d + seed + run
make test            # unit tests, -race
make test-int        # compose up + integration tests + compose down
make lint            # golangci-lint + gofumpt -l
make fmt             # gofumpt -w
make vuln            # govulncheck
make certs           # generate dev CA + server + client certs
make loadgen RATE=10000
make snapshot        # goreleaser --snapshot --clean
make clean
```

Makefile over Taskfile: no dependency, and every Go contributor already has `make`.

---

## 16. Logging strategy

The constraint that shapes everything: **any write to stdout or stderr while the alt screen is active corrupts the display.**

- **`log/slog`.** Default handler discards. `--log-file` enables a `slog.NewJSONHandler` over an `os.OpenFile` in append mode.
- **Fan-out handler** implementing `slog.Handler`, writing to (a) the file handler if configured and (b) a `Ring[Entry]` of the last 500 entries backing the in-app logs panel. One `slog.Logger` for the whole app; two sinks.
- **Redirect the standard `log` package**: `log.SetOutput(io.Discard)` (or into slog). Some dependency, somewhere, will eventually call `log.Printf`, and it will land in the middle of your tree panel.
- **Wire paho's logger.** `paho.golang` exposes logger hooks (`Errors`, `Debug`). If you leave them at their defaults or point them at stderr, a debug-level MQTT session will shred the UI. Adapt them to slog in `internal/logging/paho.go`. **This is a concrete bug you will hit within the first hour of using `--debug` if you skip it.**
- **In-app errors** use the three-tier scheme in §12.3.
- **Panic path:** recover → `program.Kill()` (restores the terminal) → print the stack to the real stderr → exit 1. Getting this wrong leaves users with a broken terminal and a reputation problem.
- `--debug` sets level to debug; `LAZYMQTT_DEBUG=1` as an equivalent env var.

---

## 17. CI/CD

**`.github/workflows/ci.yml`** — on push and PR:

| Job | Content |
|---|---|
| `lint` | `golangci-lint`, `gofumpt -l` (fail if non-empty), `go mod tidy` diff check |
| `test` | matrix: `ubuntu-latest` × `macos-latest`, Go `stable` + `oldstable`; `go test -race ./...` |
| `integration` | Mosquitto service container; `go test -tags=integration ./test/...` |
| `vuln` | `govulncheck ./...` |
| `build` | cross-compile check for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 |

**`.github/workflows/release.yml`** — on tag `v*`: goreleaser → archives + checksums + GitHub release + Homebrew tap. Optionally cosign signing and SBOM.

Dependabot for Go modules and Actions, grouped weekly.

---

## 18. Build and release

- `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -X .../version.Version=… -X .../version.Commit=… -X .../version.Date=…"`.
- Targets: `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`. Windows is not a stated requirement but comes almost free — the main risk is terminal behaviour, so ship it as best-effort/unsupported rather than claiming support you have not tested. (Note that the OS keyring path, if adopted, is the one genuinely platform-specific piece.)
- Distribution: GitHub Releases (primary), Homebrew tap, `go install github.com/<you>/lazymqtt/cmd/lazymqtt@latest`, AUR later. Expect a Nix package contributed by someone else within a week if the project gets traction.
- Semantic versioning; `v0.x` until the config schema is stable, and say so in the README so nobody builds tooling on an unstable schema.
- Licence: MIT or Apache-2.0. Note that both Paho libraries are EPL-2.0/EDL-1.0, which is fine for linking under either.

---

## 19. MVP scope

**In:**

- Connect to one broker at a time, from config profile, `--broker` URL, or interactive prompt
- Subscribe / unsubscribe at runtime; subscriptions panel showing requested vs granted QoS
- Topic tree: hierarchical, expand/collapse, per-node counts, live updates, LRU-capped
- Message list per selected topic, bounded, autoscroll
- Detail pane: topic, QoS, retain, dup, size, local receive time, raw payload (sanitised)
- Publish modal: topic, payload, QoS, retain
- Incremental filter over topics and payloads
- Pause / follow / autoscroll toggles
- Copy topic and payload via OSC52
- Clear topic / clear all
- Automatic reconnect with visible state, backoff and countdown; correct re-subscribe
- Bounded memory with all four caps enforced and drop counter displayed
- Config file: brokers, TLS, credentials via `password_cmd`/env/prompt, limits, UI
- File logging, in-app logs panel, `--debug`
- Help overlay
- Single static binary, Linux + macOS

**Out (post-MVP):**

Multiple simultaneous broker connections; MQTT 5 property editing on publish; JSON pretty-printing; hex viewer; message export; saved subscription sets; command palette; mouse; plugins; scripting; metrics/charts; latency measurement; topic aliases.

**MVP success test:** point it at a broker with 500 active topics at 5,000 msg/s, leave it running for an hour, and it stays under 100 MB RSS with input latency you cannot perceive. If that holds, the architecture is right and everything else is features.

---

## 20. Post-MVP roadmap

**v0.2 — payload comprehension.** JSON pretty-print and syntax highlight (async, via `FormatDoneMsg`); hex/binary viewer with an ASCII gutter; auto-detect by MQTT 5 `ContentType` then by sniffing; `w` to toggle wrap; CBOR/MsgPack detection.

**v0.3 — MQTT 5.** Full property display; publish with user properties, response topic, correlation data, content type, message expiry; subscription options (no-local, retain-as-published, retain-handling); request/response correlation view. Plus the 3.1.1 adapter and `protocol: auto` negotiation.

**v0.4 — workflow.** Command palette (`:`) with fuzzy matching; saved subscription sets; export selected/filtered messages to NDJSON or CSV; message search across history; repeat-publish; publish from file.

**v0.5 — multi-broker.** Multiple simultaneous connections; broker switcher; cross-broker topic comparison (surprisingly useful for staging-vs-prod diffing); per-broker colour coding.

**v0.6 — observability.** Per-topic rate sparklines; message size distribution; `$SYS` dashboard; connection latency via a ping-topic round trip; session statistics on exit.

**v1.0 — stability.** Config schema frozen; documented; plugin or scripting hook (an embedded expression language for filters is more valuable and far cheaper than a full plugin system); mouse support; theming.

---

## 21. Architectural pitfalls and how to avoid them

Ordered roughly by how much damage each does.

**1. One `tea.Msg` per MQTT message.** Saturates the single-threaded update loop; the app freezes under load. → The coalescer (§6). *Non-negotiable.*

**2. Blocking inside a paho callback.** `OnPublishReceived` runs on the client's read loop. Block it — on a full channel, a mutex, a render — and you stall ack processing until the broker drops you for being unresponsive. The symptom is baffling: the broker disconnects a client that is doing nothing wrong. → Non-blocking send with a `default:` branch. Never call into `store` or the UI from a callback.

**3. Unbounded topic cardinality.** `sensors/{uuid}/telemetry` on `#` generates unbounded nodes and OOMs. → `max_topics` + LRU eviction (§11).

**4. Re-subscribing only at startup instead of in `OnConnectionUp`.** After a reconnect the UI shows "Connected" and no messages ever arrive. Silent, and it only reproduces when the network blips. → §12.1, plus the integration test that kills the broker container.

**5. Rendering the whole tree or the whole message buffer every frame.** 5,000 nodes × 20 fps × string building = a pinned core. → Flatten with a cache, render `flat[offset:offset+height]` only, `Ring.Slice` for the message list.

**6. Allocating styles inside `View()`.** `lipgloss.NewStyle().Foreground(...)...` per row per frame is enormous GC pressure. → Package-level style vars in `theme`, built once.

**7. Rendering untrusted payload bytes directly.** *This is a security issue, not a cosmetic one.* An MQTT payload is attacker-controllable and goes straight into a terminal. Raw ANSI escape sequences in a payload can reposition the cursor, rewrite the screen, change the window title, and on some terminals trigger clipboard writes or response injection. → A `sanitize` package applied to *every* payload and topic before display: strip C0 (except where you deliberately handle `\n`), strip C1, strip `ESC`, replace with visible placeholders (`␛`, `·`), normalise wide characters. Do this at the render boundary, keep raw bytes in the store for copy/export.

**8. Huge payloads freezing the UI.** A 14 MB JSON payload arrives; pretty-printing it inside `Update` blocks input for a second. → Truncate at ingest; format asynchronously in a `tea.Cmd`; render only the visible slice.

**9. Anything writing to stdout/stderr while the TUI runs.** Corrupts the display. Sources: your own `fmt.Println` debugging, the stdlib `log` package, paho's default logger, an unrecovered panic. → §16, all four cases.

**10. `q` quitting while the user is typing.** They type a topic filter containing `q` and the app exits. → Mode-based key routing (§7.2), tested explicitly.

**11. Terminal left broken after a panic.** Raw mode, alt screen, hidden cursor. → `recover` + `program.Kill()` before printing anything.

**12. Goroutine leaks across reconnect cycles.** Each reconnect spawns; nothing reaps. Invisible for an hour, then the process has 4,000 goroutines. → One `context.Context` per connection attempt, a `sync.WaitGroup` at shutdown, `goleak` in tests.

**13. Cursor jitter under load.** New topics shift the flattened slice; the selection tracks the *index* and appears to move on its own. → Track the selected topic string, re-resolve the index after each flatten (§7.3).

**14. Duplicate counting with overlapping subscriptions.** → Dedupe at ingest, or use MQTT 5 subscription identifiers (§11).

**15. Big value types in the Bubble Tea model.** The model is copied on every `Update`; an inline array or a large embedded struct makes that expensive. → Slices, maps, and pointers only; the store is explicitly a pointer.

**16. Assuming `#` sees everything.** `$SYS` and other `$`-prefixed topics are not matched by `#` or `+` at the first level. Users will report "$SYS is missing" as a bug. → Subscribe to `$SYS/#` separately if requested; document it.

**17. Timestamp confusion.** MQTT carries no publish timestamp; what you show is local arrival time. → Label it, and consider showing MQTT 5 `MessageExpiry` where present.

**18. Config rewriting destroying user comments.** → Never rewrite `config.yaml`; UI state goes in a separate state file (§9.4).

**19. Golden-file view tests that fail on a different terminal.** → Force the colour profile and window size in tests (§14.1).

**20. Over-abstracting early.** A `MessageRepository` interface with one implementation, a plugin system before v1, an event bus for a single-process app. → The only interface earning its place in the MVP is `mqtt.Client`, and it earns it because a second implementation is already scheduled for phase 8.

---

## 22. Implementation phases

Ordered by dependency. Each phase leaves the repository in a working, committable state.

---

### Phase 0 — Repository and toolchain

**Build:** `go.mod`, directory skeleton, `Makefile`, `.golangci.yml` (including the `depguard` rule forbidding `charm.land/*` imports inside `internal/mqtt` and `internal/store`), `.github/workflows/ci.yml`, `internal/version`, MIT/Apache licence, README skeleton, `cmd/lazymqtt/main.go` printing `--version`.

**Why first:** every later phase gets linted and tested from its first commit. Retrofitting CI is always worse.

**Files:** `go.mod`, `Makefile`, `.golangci.yml`, `.github/workflows/ci.yml`, `internal/version/version.go`, `cmd/lazymqtt/main.go`

**Tests:** `version` returns ldflags-injected values.

**Done when:** `make build lint test` is green in CI on Linux and macOS; `./lazymqtt --version` prints version, commit and date.

---

### Phase 1 — Domain core

**Build:** `mqtt.Message`, `Properties`, `Subscription`, `ConnState`/`ConnStatus`; topic segment splitting, `MatchTopic`, `ValidateFilter`; `store.Ring[T]`; `store.TopicNode` with insert/expand/flatten; `store.Limits` with LRU eviction; `store.Store` with `Ingest` and `Stats`.

**Why here:** zero dependencies, entirely pure, and everything else builds on it. Also the highest-density bug surface in the project, so it deserves to be nailed before anything can obscure it.

**Files:** `internal/mqtt/{client,state,topic}.go`, `internal/store/{store,tree,ring,limits,filter}.go`

**Tests:** spec-derived table tests for `MatchTopic` plus a fuzz target; `ValidateFilter` edge cases; `Ring` wraparound and `Slice` across the boundary; tree insert/flatten/collapse; LRU eviction pruning empty parents; `Ingest` enforcing all four caps; byte accounting.

**Done when:** >90% coverage on `internal/store` and `internal/mqtt` (the one place a coverage number is meaningful, because it is pure logic); fuzz target runs 60s clean; a synthetic 1M-message ingest holds resident memory under the calculated ceiling.

---

### Phase 2 — Configuration

**Build:** schema structs; discovery chain; `goccy/go-yaml` parsing with strict unknown-field detection; defaults merging via pointer fields; semantic validation with aggregated errors; file-permission check for literal passwords; credential resolution chain (`password_cmd`, env, prompt hook); TLS config construction; path expansion (`~`, env vars).

**Why here:** the MQTT adapter needs a resolved `Options` struct, and building that before the adapter means the adapter never learns about files or secrets.

**Files:** `internal/config/{config,load,validate,secret}.go`, `internal/mqtt/tls.go`

**Tests:** golden config → expected struct; every validation error triggered independently and all reported together; defaults override precedence; `password_cmd` success, failure, timeout; permission check rejecting mode 0644 with a literal password; `~` expansion; unknown field produces an error naming the field and line.

**Done when:** a hand-written realistic `config.yaml` parses and validates; a deliberately broken one produces a readable multi-error report a user could act on without reading the source.

---

### Phase 3 — MQTT adapter, headless

**Build:** `paho5.Adapter` implementing `mqtt.Client`; connect/disconnect/subscribe/unsubscribe/publish; `OnPublishReceived` → truncate → assign `Seq` → non-blocking send; callbacks → `Event`; state machine with reason-code classification; re-subscribe on `OnConnectionUp`; paho logger → slog; `mqtttest.Fake`.

**Plus a temporary `--headless` mode** in `main.go` that connects, subscribes and prints one line per message to stdout. Throwaway, ~40 lines, and it proves the entire MQTT layer against a real broker *before a single line of UI exists*.

**Why here:** this is the riskiest external dependency. Find out now whether autopaho behaves as expected, while there is no UI to confuse the diagnosis.

**Files:** `internal/mqtt/paho5/adapter.go`, `internal/mqtt/mqtttest/fake.go`, `internal/logging/paho.go`, `deploy/docker-compose.yml`, `deploy/mosquitto/`

**Tests:** unit with `Fake` (re-subscribe on reconnect, drop-oldest on full channel, `StateFailed` on auth rejection, no retry from `StateFailed`); integration against Mosquitto (round-trip, retained, QoS 1/2, TLS, mTLS, broker kill → reconnect → **messages flow again**).

**Done when:** `lazymqtt --headless -b tcp://localhost:1883 -t '#'` streams live messages; killing and restarting the broker container results in restored message flow with no restart; `goleak` clean after disconnect.

---

### Phase 4 — Bridge and coalescer

**Build:** `app.Bridge` — the ingest channel, the drain/batch loop, ticker flush, batch cap, drop accounting, context-driven shutdown; the `tea.Msg` catalogue; `tea.Cmd` constructors for connect/subscribe/unsubscribe/publish; `app.App` wiring config → client → bridge.

**Why here:** it is the seam, it is pure concurrency logic, and it is far easier to test now than through a UI later.

**Files:** `internal/app/{bridge,events,commands,app.go}`

**Tests:** injected ticker; N in → expected batch shape out; no send on empty; batch cap respected; drop counter propagated; cancellation drains cleanly; `goleak.VerifyNone`; a race-detector test hammering the bridge from multiple producers.

**Done when:** `mqttload --rate 50000` through the bridge produces ≤20 batches/sec with a stable drop counter, `-race` clean, no goroutine leaks.

---

### Phase 5 — TUI skeleton

**Build:** root model; layout computation from `WindowSizeMsg`; four static panels with borders and titles; focus cycling (`Tab`, `shift+Tab`, `1`–`4`); mode enum and key routing; `keys` registry of `key.Binding`s; `theme` package; status header and footer keybar; help overlay via `bubbles/help`; `q`/`ctrl+c` quit; alt screen via `tea.View.AltScreen`.

Everything renders **static placeholder data**. No store, no MQTT.

**Why here:** decouples layout and focus bugs from data bugs. Both are fiddly; debugging them simultaneously is miserable.

**Files:** `internal/ui/{root,layout}.go`, `internal/ui/keys/keys.go`, `internal/ui/theme/theme.go`, `internal/ui/panel/{status,help}.go`

**Tests:** key sequences drive focus as expected; layout arithmetic at 80×24, 120×40 and a degenerate 30×10 (panels must degrade, not panic); golden-file views with a forced colour profile; `?` toggles help and `Esc` closes it.

**Done when:** the app runs, panels render correctly at three sizes, resizing is clean, help works, quitting restores the terminal.

---

### Phase 6 — Live data

**Build:** wire the store into the root model; topic tree panel (flatten, viewport, indent, expand/collapse with `h`/`l`/`Enter`, counts, cursor stability, activity indicator); message list panel (`Ring.Slice`, autoscroll, timestamp/QoS/retain columns); detail panel with sanitisation; `BatchMsg` handling; live stats in the header; `sanitize` package.

**Why here:** this is the first moment the app is genuinely useful, and it is where the performance characteristics of §6 and §7.4 get validated for real.

**Files:** `internal/ui/panel/{topics,messages,detail}.go`, `internal/ui/sanitize/sanitize.go`, `internal/ui/root.go`

**Tests:** tree navigation over a synthetic 5,000-node store; cursor stability under insertion; only the visible window is rendered (assert via a render-call counter or benchmark); sanitisation strips ANSI, C0/C1 and handles wide characters; golden views with data.

**Done when:** connect to the seeded dev broker and the tree populates live; `mqttload --rate 10000` for 5 minutes stays under 100 MB RSS with responsive input; profiling shows no per-frame style allocation.

---

### Phase 7 — Actions

**Build:** subscribe prompt with filter validation; unsubscribe on the selected subscription; publish modal (topic prefilled from selection, `textarea` payload, QoS selector, retain toggle); broker picker; connect/disconnect; toast system; subscriptions panel with granted-QoS display; `PubResultMsg`/`SubResultMsg` handling.

**Why here:** needs both the UI shell (5) and the command layer (4), and mode routing (5) must already be correct or the modals will fight the global keymap.

**Files:** `internal/ui/panel/{publish,prompt,brokers}.go`, `internal/ui/root.go`, `internal/app/commands.go`

**Tests:** invalid filter rejected with an inline message and no MQTT call; publish modal produces the expected `PublishRequest`; `Esc` cancels every modal; `q` inside a text field inserts a literal `q`; QoS downgrade surfaces a toast.

**Done when:** the app subscribes, unsubscribes and publishes against a live broker entirely from the keyboard, with all failures visible in the UI and none on stdout.

---

### Phase 8 — Search, clipboard, control

**Build:** incremental filter (`/`) over topics and payloads with `n`/`N` navigation and `Esc` to clear; `y` copy payload / `Y` copy topic via `tea.SetClipboard`; `Space` pause; `f` follow; `a` autoscroll; `x` clear topic / `X` clear all with confirmation; logs panel (`ctrl+l`); reconnect countdown UI; the persistent `insecure_skip_verify` banner.

**Why here:** pure UI polish over stable foundations, and it converts the app from "works" to "replaces your GUI client".

**Files:** `internal/ui/panel/logs.go`, `internal/store/filter.go`, `internal/ui/root.go`

**Tests:** filter narrows the flattened tree correctly and restores on clear; pause stops view updates while `Stats.Received` keeps climbing; clear-topic empties the ring but preserves the node; confirmation required for clear-all.

**Done when:** every MVP keybinding in §7 works and is documented in the help overlay, which is generated from the same `key.Binding` registry the app dispatches on — so the help cannot drift out of date.

---

### Phase 9 — Hardening

**Build:** panic recovery everywhere with terminal restoration; error classification completed; `goleak` across the suite; a profiling pass (`pprof` under `mqttload`) with fixes; degenerate-terminal handling; state file persistence; README with screenshots (asciinema or VHS); `CONTRIBUTING.md` recording the Cobra rule and the layering rules.

**Why here:** you can only harden something that exists, and the perf pass needs the real render path.

**Files:** across the tree; `README.md`, `CONTRIBUTING.md`, `test/integration/*`

**Tests:** the full integration matrix; a soak test (1 hour at 5,000 msg/s, asserting bounded RSS and goroutine count); chaos-ish tests (broker restart loop, malformed payloads, 10 MB payloads, payloads full of ANSI escapes, topics with wide/emoji characters).

**Done when:** the §19 MVP success test passes; no known way to leave the terminal broken; a new contributor can go from clone to running against the dev broker with `make dev`.

---

### Phase 10 — Release

**Build:** `.goreleaser.yaml`; release workflow; Homebrew tap; installation docs; a documented config reference; `v0.1.0`.

**Done when:** `brew install <tap>/lazymqtt` and `go install …@latest` both yield a working binary on Linux and macOS, arm64 and amd64.

---

## Appendix A — Proposed keymap

Evaluated against your initial list; changes are annotated.

**Global**

| Key | Action | Note |
|---|---|---|
| `?` | Help overlay | as proposed |
| `Tab` / `S-Tab` | Cycle panels | as proposed |
| `1`–`4` | Jump to panel | **added** — lazygit convention, faster than cycling |
| `Esc` | Back / close / clear | **added** — essential; `q` alone is not enough |
| `q` | Close overlay, else quit | **changed** — context-sensitive, not an unconditional quit |
| `ctrl+c` | Force quit | always |
| `ctrl+l` | Logs panel | **added** |
| `Space` | Pause view | **added** — critical for high-rate topics |

**Navigation**

| Key | Action |
|---|---|
| `j` / `k`, `↓` / `↑` | Move (as proposed) |
| `h` / `l` | Collapse / expand node (**added** — vim/nerdtree muscle memory) |
| `g` / `G` | Top / bottom (**added**) |
| `ctrl+d` / `ctrl+u` | Half page (**added**) |
| `Enter` | Select topic / open (as proposed) |

**Actions**

| Key | Action | Note |
|---|---|---|
| `s` | Subscribe | as proposed |
| `u` | Unsubscribe | as proposed |
| `p` | Publish (topic prefilled from selection) | as proposed |
| `c` | Connect / switch broker | as proposed |
| `/` | Filter | as proposed |
| `n` / `N` | Next / previous match | **added** |
| `y` / `Y` | Copy payload / copy topic | **split** — both are wanted, and OSC52 makes it work over SSH |
| `t` | Toggle retained-only | **changed from `r`** — `r` reads as "refresh/reload" in every other TUI; rebinding it to a filter toggle will surprise people. `r` is reserved for "force reconnect", which is what users will reach for. |
| `f` | Follow mode | **added** |
| `a` | Autoscroll toggle | **added** |
| `x` / `X` | Clear topic / clear all | **added** |
| `w` | Toggle payload wrap | **added** (v0.2) |
| `:` | Command palette | post-MVP |

All bindings are declared once as `key.Binding` values in `internal/ui/keys` and consumed by both the dispatcher and `bubbles/help`, so the footer, the help overlay and actual behaviour cannot diverge.

---

## Appendix B — Dependency summary

| Module | Purpose | Licence | Risk |
|---|---|---|---|
| `charm.land/bubbletea/v2` | TUI framework | MIT | Low — v2.0.x, heavily used in production |
| `charm.land/lipgloss/v2` | Styling | MIT | Low |
| `charm.land/bubbles/v2` | Components | MIT | Low |
| `github.com/eclipse/paho.golang` | MQTT 5 client | EPL-2.0 / EDL-1.0 | **Medium — v0.x, breaking changes between minors.** Pin exactly; isolated behind `mqtt.Client`; upgrade deliberately |
| `github.com/eclipse/paho.mqtt.golang` | MQTT 3.1.1 client (phase 8) | EPL-2.0 / EDL-1.0 | Low — stable v1 API |
| `github.com/goccy/go-yaml` | Config | MIT | Low — actively maintained successor to the archived go-yaml |
| `github.com/google/go-cmp` | Test diffs | BSD-3 | Test-only |
| `go.uber.org/goleak` | Leak detection | MIT | Test-only |

The only dependency needing active management is `paho.golang`. Pin it, read the release notes before bumping, and keep the adapter thin enough that a breaking change is a one-file fix.
