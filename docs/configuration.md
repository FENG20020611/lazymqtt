# Configuration reference

lazymqtt runs with no configuration at all — `lazymqtt -b tcp://localhost:1883`
needs nothing on disk. A config file exists to save you from retyping broker
details and to keep credentials out of your shell history.

**lazymqtt only ever reads this file.** It is never rewritten, so your comments
and formatting are safe; UI state goes to a separate `state.json`
([below](#files-lazymqtt-writes)).

- [Where the file lives](#where-the-file-lives)
- [What overrides what](#what-overrides-what)
- [A complete file](#a-complete-file)
- [`version`](#version)
- [`defaults`](#defaults)
- [`limits`](#limits)
- [`ui`](#ui)
- [`logging`](#logging)
- [`brokers`](#brokers)
- [Addresses, schemes and ports](#addresses-schemes-and-ports)
- [Credentials](#credentials)
- [TLS](#tls)
- [Validation](#validation)
- [Accepted but not yet implemented](#accepted-but-not-yet-implemented)
- [Environment variables](#environment-variables)
- [Files lazymqtt writes](#files-lazymqtt-writes)

## Where the file lives

```sh
lazymqtt config init     # write a commented starter file, mode 0600
lazymqtt config check    # validate it and print the resolved brokers
```

The first of these that exists is used:

1. `--config` / `-c`
2. `$LAZYMQTT_CONFIG`
3. `$XDG_CONFIG_HOME/lazymqtt/config.yaml`
4. the platform config directory — `~/Library/Application Support/lazymqtt/config.yaml`
   on macOS, `%AppData%\lazymqtt\config.yaml` on Windows
5. `~/.config/lazymqtt/config.yaml`

Nothing found is not an error: you get the built-in defaults.

`config init` writes to the first of `$LAZYMQTT_CONFIG`,
`$XDG_CONFIG_HOME/lazymqtt/config.yaml`, or the platform config directory. It
refuses to overwrite an existing file.

Any value that is a path accepts a leading `~` and `$VAR` references.

## What overrides what

Later wins:

1. built-in defaults
2. the `defaults:` block
3. the individual `brokers.<name>:` entry
4. command-line flags

Concretely: `--broker` beats the remembered profile from the last session,
and `--topic` (repeatable) replaces the profile's `subscriptions:` entirely
rather than adding to it.

## A complete file

Every key, with its default value. You need approximately none of this — see
[`brokers`](#brokers) for the useful minimum.

```yaml
version: 1                          # required, must be 1

defaults:
  client_id: "lazymqtt-{{hostname}}-{{pid}}"
  keepalive: 30s
  connect_timeout: 10s
  clean_start: true
  protocol: auto                    # auto | 5 | 3.1.1
  subscriptions:
    - filter: "#"
      qos: 0

limits:
  max_topics: 5000
  per_topic_history: 50
  stream_history: 2000
  max_payload_bytes: 1048576
  ingest_buffer: 4096

ui:
  refresh_ms: 50
  timestamp_format: "15:04:05.000"
  theme: auto                       # not yet implemented
  start_panel: topics
  mouse: false                      # not yet implemented

logging:
  level: warn                       # debug | info | warn | error
  file: ""                          # empty = no file logging, never stdout
  redact_payloads: true

brokers:
  local:
    host: localhost
    port: 1883
```

**Unknown keys are an error, not a warning.** A typo like `keepalive_secs:`
would otherwise be silently ignored, and you would spend the afternoon
wondering why it had no effect.

## `version`

| Key | Type | Default | Notes |
|---|---|---|---|
| `version` | int | — | **Required.** Must be `1`. |

Checked before anything else, so a future schema bump produces one clear
message rather than a wall of unknown-field errors. Omitting it is an error
even though every other key is optional: a file with no version cannot be
migrated later without guessing what it was written against.

## `defaults`

Applied to every broker that does not override them.

| Key | Type | Default | Notes |
|---|---|---|---|
| `client_id` | string | `lazymqtt-{{hostname}}-{{pid}}` | See below. |
| `keepalive` | duration | `30s` | |
| `connect_timeout` | duration | `10s` | |
| `clean_start` | bool | `true` | `false` resumes a persistent session. |
| `protocol` | string | `auto` | `auto`, `5`, `3.1.1`. Not yet implemented — see [below](#accepted-but-not-yet-implemented). |
| `subscriptions` | list | `[{filter: "#", qos: 0}]` | Subscribed on connect. |

**Durations are strings.** `30s`, `1m`, `500ms`. A bare `keepalive: 30` is an
error rather than being read as nanoseconds or seconds — the ambiguity is not
worth the convenience.

**`client_id` templates.** Exactly three substitutions, deliberately not a
general template language:

| | |
|---|---|
| `{{hostname}}` | short hostname, truncated at the first `.` |
| `{{pid}}` | process ID |
| `{{user}}` | `$USER`, `$LOGNAME` or `$USERNAME` |

The result is truncated to 64 bytes. Note that MQTT 3.1.1 brokers may reject
client IDs longer than 23 bytes, so a long hostname plus a PID can be rejected
by an old broker even though it is fine for MQTT 5.

Two clients sharing a client ID will disconnect each other in a loop, which is
why the default includes the PID: two lazymqtt windows against one broker have
to work.

**`subscriptions`** entries are `{filter, qos}`. Filters are validated against
the MQTT wildcard rules, so `sensors/#/temp` is rejected at load rather than by
the broker at connect. Note that `#` does **not** match `$SYS/...` — `$`-prefixed
topics need their own subscription.

## `limits`

The four caps that make memory bounded, plus the ingest buffer. Every one of
these is a deliberate ceiling: an MQTT viewer pointed at `#` on a busy broker
will otherwise consume everything the machine has.

| Key | Type | Default | Notes |
|---|---|---|---|
| `max_topics` | int | `5000` | Tree leaves before the least-recently-updated is evicted. |
| `per_topic_history` | int | `50` | Messages kept per topic. |
| `stream_history` | int | `2000` | The global firehose ring, also the pause backlog cap. |
| `max_payload_bytes` | int | `1048576` | Payloads are truncated **at ingest**, not at display. |
| `ingest_buffer` | int | `4096` | Channel depth between the MQTT read loop and the UI. |

`max_topics` is what stops `sensors/{uuid}/telemetry` from exhausting memory:
past the cap, the least recently updated topic is evicted and counted, and the
header shows the eviction count so you know the tree is not the whole picture.

`max_payload_bytes` truncates before the message enters the pipeline. Truncating
at render time would mean the memory cost had already been paid. The detail pane
marks a truncated payload and shows its original size.

`ingest_buffer` is the drop-oldest boundary. When the UI cannot keep up the
excess is dropped and counted rather than queued without limit — the header
shows the drop count, because dropping is correct for a viewer but dropping
*silently* is not. Raising it buys a longer burst tolerance, not a higher
sustained rate.

> **`0` does not mean "default" here.** An explicit `per_topic_history: 0` or
> `stream_history: 0` is clamped to **1**, not to the default, so you get a
> store that keeps a single message and looks broken. `max_topics: 0` and
> `max_payload_bytes: 0` *do* fall back to their defaults. Omit a key to get
> its default; do not set it to zero.

## `ui`

| Key | Type | Default | Notes |
|---|---|---|---|
| `refresh_ms` | int | `50` | Coalescer flush interval. Valid range 10–2000. |
| `timestamp_format` | string | `15:04:05.000` | Go reference-time layout. |
| `theme` | string | `auto` | `auto`, `dark`, `light`. Not yet implemented. |
| `start_panel` | string | `topics` | `topics`, `messages`, `detail`, `subscriptions`. |
| `mouse` | bool | `false` | Not yet implemented. |

`refresh_ms` is the single most important performance knob, and it is a frame
budget rather than a poll interval: messages accumulate in a batch and are
delivered to the UI as **one** update per tick, however many arrived. This is
what makes 5,000 msg/s survivable — one `tea.Msg` per MQTT message saturates
the update loop and the app freezes. Raising it to `100` halves the render work
at the cost of visibly laggier updates; lowering it below `20` mostly buys
render cost.

`timestamp_format` uses Go's reference time, not `strftime`: the layout is
written as `15:04:05.000` because that *is* the reference time, not because
those are placeholders. `2006-01-02 15:04:05` gives a full date.

The displayed time is **local arrival time**. MQTT carries no publish
timestamp, so it cannot be anything else — a message delayed in a queue for an
hour is timestamped when lazymqtt saw it.

## `logging`

A file is the only destination. Writing to stdout or stderr while the alt
screen is up corrupts the display, so there is no option to do it.

| Key | Type | Default | Notes |
|---|---|---|---|
| `level` | string | `warn` | `debug`, `info`, `warn`, `error`. |
| `file` | path | `""` | Empty disables file logging. Created mode 0600. |
| `redact_payloads` | bool | `true` | Currently vacuous — see below. |

Logs are always visible in-app on the logs panel (`L`) regardless of `file`,
held in a bounded ring. `--debug` or `LAZYMQTT_DEBUG=1` forces `level: debug`.

`redact_payloads` is honoured trivially today: **nothing writes a payload to
the log at any level**, so there is nothing to redact. The key exists so that
the intent is on record — a payload is attacker-controlled data and a log file
outlives the session that produced it.

## `brokers`

A map of profile name to connection settings. The name is what you pass to
`--broker`, what the `c` picker lists, and what is remembered between sessions.

The useful minimum:

```yaml
version: 1
brokers:
  local:
    host: localhost
```

Something realistic:

```yaml
brokers:
  production:
    host: mqtt.example.com
    port: 8883
    tls:
      enabled: true
      ca_file: ~/.config/lazymqtt/certs/ca.pem
    username: ops
    password_cmd: "pass show mqtt/production"
    subscriptions:
      - filter: "devices/+/state"
        qos: 1
```

| Key | Type | Default | Notes |
|---|---|---|---|
| `host` | string | — | Required unless `url` is set. |
| `port` | int | by scheme | 0–65535. |
| `scheme` | string | `tcp` | `tcp`, `mqtt`, `tls`, `ssl`, `mqtts`, `ws`, `wss`. |
| `url` | string | — | Replaces `host`/`port`/`scheme` entirely. Mutually exclusive with `host`. |
| `client_id` | string | from `defaults` | |
| `keepalive` | duration | from `defaults` | |
| `connect_timeout` | duration | from `defaults` | |
| `clean_start` | bool | from `defaults` | |
| `session_expiry` | uint32 | `0` | MQTT 5 session expiry, in seconds. |
| `protocol` | string | from `defaults` | Not yet implemented. |
| `username` | string | — | |
| `password` | string | — | Plaintext. See [Credentials](#credentials). |
| `password_env` | string | — | Name of an environment variable. |
| `password_cmd` | string | — | Shell command; stdout is the password. |
| `tls` | block | — | See [TLS](#tls). |
| `subscriptions` | list | from `defaults` | Replaces the default set; not merged. |

`subscriptions` on a broker **replaces** `defaults.subscriptions` rather than
adding to it, so a profile that sets one narrow filter does not also get `#`.

## Addresses, schemes and ports

Write the address either as `host` plus optional `port` and `scheme`, or as a
single `url`. Setting both is an error.

These spellings are accepted and normalised:

| You write | It becomes |
|---|---|
| `mqtt://host` | `tcp://host` |
| `mqtts://host` | `tls://host` |
| `ssl://host` | `tls://host` |
| `host:1883` (in `url`) | `tcp://host:1883` |

Default ports, when neither `port` nor a port in `url` is given:

| Scheme | Port |
|---|---|
| `tcp`, `mqtt` | 1883 |
| `tls`, `ssl`, `mqtts` | 8883 |
| `ws` | 8083 |
| `wss` | 8084 |

`--broker` accepts a profile name or a bare URL, so `lazymqtt -b
tcp://10.0.0.5:1883` needs no config at all. With exactly one profile
configured, a bare `lazymqtt` uses it.

Credentials in a `--broker` URL — `tcp://user:pass@host` — are **not** parsed.
Anything needing authentication needs a `brokers:` entry.

## Credentials

Resolved per broker, first match wins:

1. **`password_cmd`** — the command's stdout, trailing newline trimmed. Runs
   through `$SHELL -c` with a 10 second timeout, so a hung `op read` fails the
   connect instead of hanging the app. Works with `pass`, `gopass`, `op read`,
   `bw get`, `security find-generic-password`, `gcloud secrets`. **Prefer
   this.**
2. **`password_env`** — the named environment variable. Not being set is an
   error rather than an empty password.
3. **`password`** — a literal, with `${VAR}` expansion.
4. **An interactive prompt**, if `username` is set with no password source.

Setting more than one of the three `password*` keys is an error rather than a
silent precedence surprise.

A literal `password` produces a warning, and **lazymqtt refuses to load a
config containing one if the file is group- or world-readable**, the way
OpenSSH refuses a private key with loose permissions:

```
config file with a literal password is group- or world-readable:
  /home/you/.config/lazymqtt/config.yaml has mode 0644; run: chmod 600 …
```

Passwords are held in memory only. Nothing is written to the config file, the
state file, or the log.

The interactive prompt is collected **before** the TUI starts, because reading
stdin while Bubble Tea owns the terminal in raw mode corrupts the display. One
consequence: switching to a password-protected profile with the in-app `c`
picker cannot prompt, so profiles reached that way need `password_cmd` or
`password_env`.

## TLS

| Key | Type | Default | Notes |
|---|---|---|---|
| `enabled` | bool | `false` | |
| `ca_file` | path | — | For a private CA. |
| `cert_file` | path | — | Client certificate, for mTLS. Requires `key_file`. |
| `key_file` | path | — | Requires `cert_file`. |
| `server_name` | string | the host | SNI / verification name override. |
| `insecure_skip_verify` | bool | `false` | Disables verification. |

**`enabled: true` rewrites the scheme**, upgrading `tcp`/`mqtt` to `tls` and
`ws` to `wss`. This is not cosmetic: the client library decides whether to open
an encrypted connection from the scheme alone and ignores the TLS settings
entirely on a plaintext scheme. Without the rewrite, a profile written as
`url: mqtt://host:1883` with `tls.enabled: true` would build a TLS
configuration, never use it, and connect in the clear.

TLS on the conventionally plaintext port 1883 is unusual but entirely legal and
fully supported.

`insecure_skip_verify: true` works and paints a **permanent banner** for the
whole session rather than a warning that scrolls away, because the difference
between "I am testing against a self-signed cert" and "I have been silently
downgraded" is worth being unable to ignore.

## Validation

`lazymqtt config check` reports everything at once — fixing a config one error
per run is miserable. Errors prevent startup; warnings do not.

**Errors**

- `version` missing, or not `1`
- an unknown key anywhere
- a broker with neither `host` nor `url`, or with both
- `port` outside 0–65535
- an invalid `scheme` or `protocol`
- an invalid subscription filter, or `qos` above 2
- more than one of `password`, `password_env`, `password_cmd`
- `cert_file` without `key_file`, or the reverse
- `max_topics` non-zero and below 100
- `ingest_buffer` non-zero and below 64
- `max_payload_bytes` negative
- `refresh_ms` non-zero and outside 10–2000
- an invalid `theme`, `start_panel` or `logging.level`
- a config file holding a literal `password` that is group- or world-readable

**Warnings**

- a literal `password` (`password_cmd` is safer)
- certificate paths set while `tls.enabled` is `false`
- port 8883, or a `tls://`/`mqtts://` URL, while `tls.enabled` is `false`
- `insecure_skip_verify` enabled

## Accepted but not yet implemented

These parse and validate, so a config using them loads cleanly, but they have
no effect yet. They are listed here rather than removed because removing them
would be a breaking schema change and each is planned.

| Key | Status |
|---|---|
| `defaults.protocol`, `brokers.*.protocol` | Carried through to the client but ignored; the MQTT 5 adapter is always selected. The 3.1.1 adapter and `auto` negotiation are planned. |
| `ui.theme` | Validated but unread; the dark palette is always used. |
| `ui.mouse` | Unread. Mouse support is post-1.0. |
| `logging.redact_payloads` | Vacuous: nothing logs payloads today. |

## Environment variables

| Variable | Effect |
|---|---|
| `LAZYMQTT_CONFIG` | Config file path; beats the discovery chain, loses to `--config`. |
| `LAZYMQTT_DEBUG=1` | Same as `--debug`. |
| `XDG_CONFIG_HOME` | Config directory root. |
| `XDG_STATE_HOME` | State directory root. |
| `SHELL` | Used to run `password_cmd`; falls back to `/bin/sh`. |
| `USER`, `LOGNAME`, `USERNAME` | Source for `{{user}}` in `client_id`. |

Any `$VAR` in a path value, or in a literal `password`, is expanded.

## Files lazymqtt writes

| Path | Contents |
|---|---|
| `$XDG_STATE_HOME/lazymqtt/state.json` | Last broker profile, which tree nodes were open, recent publish payloads. Written on exit, mode 0600. Safe to delete. Never contains credentials. |
| `logging.file`, if set | JSON log records, mode 0600. |

The config file itself is **never** written except by `lazymqtt config init`,
which refuses to overwrite.
