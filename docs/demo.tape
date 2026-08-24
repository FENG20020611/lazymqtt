# A VHS tape recording the README demo. https://github.com/charmbracelet/vhs
#
#   docker compose -f deploy/docker-compose.yml up -d   # the seeded broker
#   make build
#   vhs docs/demo.tape                                  # writes docs/demo.gif
#
# The tape drives the real binary against the real dev broker: the seed
# container publishes a retained tree under `home/`, `devices/` and `sensors/`,
# so the topic tree fills in on its own and nothing here has to fake data.
#
# It passes --config docs/demo-config.yaml on purpose. Left to the discovery
# chain it would pick up whatever is in ~/.config/lazymqtt, and a personal
# config that fails to parse records a GIF of the error message instead of the
# app.
#
# Keep it under about 30 seconds. A README GIF that loops before the reader has
# finished the paragraph next to it is the one that gets watched.

Output docs/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1400
Set Height 800
Set Padding 12
# The exact name matters: VHS matches its theme list literally, and
# `vhs validate` does not check it — only a real recording does.
Set Theme "TokyoNight"
Set TypingSpeed 60ms
# The GIF is decoration, not documentation: 12 fps and a modest window keep it
# small enough that GitHub will actually inline it.
Set Framerate 12

Hide
Type "./bin/lazymqtt --config docs/demo-config.yaml -b demo"
Enter
Sleep 3s
Show

# Let the seeded tree arrive and settle.
Sleep 2s

# Walk the topic tree: expand a branch, land on a leaf.
Down@400ms 3
Right
Sleep 800ms
Down@400ms 2
Sleep 1.5s

# The message list and the detail pane follow the selection. The seeded
# payloads are JSON, so this is where the pretty-printing shows.
Tab
Sleep 1s
Down@400ms 3
Sleep 2s

# Publish something and watch it come back on the subscription.
Escape
Type "p"
Sleep 800ms
# The modal seeds the topic from the tree selection and the payload from the
# last publish to that topic, so both fields are cleared before typing —
# otherwise the typing is appended and the topic comes out as nonsense.
Ctrl+U
Type "home/kitchen/lights/set"
Tab
Ctrl+U
# Single quotes: VHS has no escape sequences inside a double-quoted string,
# so a JSON payload has to be quoted the other way round.
Type '{"on": true, "brightness": 80}'
Sleep 1.5s
Ctrl+S
Sleep 2s

# That message comes straight back on the `#` subscription and is now the
# newest in the list. Jumping to it puts a JSON payload in the detail pane,
# which is where the pretty-printing and the syntax highlighting show.
Type "2"
Type "g"
Sleep 3s

# Filter, then clear it.
Type "/"
Type "kitchen"
Sleep 2s
Escape
Sleep 800ms

# The help overlay is the last frame: it tells the reader the app is
# keyboard-driven without a caption saying so.
Type "?"
Sleep 3s

Hide
# Escape closes the help overlay first: `q` inside a mode is a literal `q`,
# not a quit.
Escape
Type "q"
