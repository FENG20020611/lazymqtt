package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

func newTestModel(t *testing.T, w, h int) Model {
	t.Helper()
	cfg := config.Default()
	m := New(Options{App: app.New(cfg, logging.Discard()), Config: cfg})
	return m.resize(w, h)
}

// press feeds a key by its string form, the same way a terminal would.
func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.Update(keyMsg(k))
		var ok bool
		m, ok = next.(Model)
		if !ok {
			t.Fatalf("Update returned %T", next)
		}
	}
	return m
}

func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+l":
		return tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func ingest(m Model, topics ...string) Model {
	msgs := make([]*mqtt.Message, 0, len(topics))
	for i, tp := range topics {
		msgs = append(msgs, &mqtt.Message{
			Seq:        uint64(i + 1),
			Topic:      tp,
			Payload:    []byte(fmt.Sprintf("value-%d", i)),
			ReceivedAt: time.Unix(0, 0).UTC(),
		})
	}
	next, _ := m.Update(app.BatchMsg{Msgs: msgs})
	return next.(Model)
}

func TestTabCyclesFocus(t *testing.T) {
	m := newTestModel(t, 120, 40)
	want := []Focus{FocusMessages, FocusDetail, FocusSubs, FocusTopics}
	for _, w := range want {
		m = press(t, m, "tab")
		if m.Focused() != w {
			t.Fatalf("after tab focus = %v, want %v", m.Focused(), w)
		}
	}
	m = press(t, m, "shift+tab")
	if m.Focused() != FocusSubs {
		t.Fatalf("shift+tab focus = %v, want Subscriptions", m.Focused())
	}
	for i, key := range []string{"1", "2", "3", "4"} {
		if m = press(t, m, key); m.Focused() != Focus(i) {
			t.Fatalf("key %q focused %v", key, m.Focused())
		}
	}
}

func TestCursorMovesAndClampsAtBounds(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = ingest(m, "a/1", "a/2", "a/3")
	m = press(t, m, "1", "g")
	if got := m.topics.Cursor(); got != 0 {
		t.Fatalf("g put the cursor at %d", got)
	}
	m = press(t, m, "k", "k")
	if got := m.topics.Cursor(); got != 0 {
		t.Fatalf("k past the top moved to %d; it must clamp", got)
	}
	m = press(t, m, "G")
	last := len(m.store.Flatten()) - 1
	if got := m.topics.Cursor(); got != last {
		t.Fatalf("G put the cursor at %d, want %d", got, last)
	}
	m = press(t, m, "j", "j")
	if got := m.topics.Cursor(); got != last {
		t.Fatalf("j past the bottom moved to %d; it must clamp", got)
	}
}

// The single most common TUI bug: a literal q while typing quits the app.
func TestQInsideAPromptIsALiteralQ(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = press(t, m, "/")
	if m.Mode() != ModePrompt {
		t.Fatalf("/ did not open a prompt, mode = %v", m.Mode())
	}
	m = press(t, m, "q", "u", "e", "u", "e")
	if m.Mode() != ModePrompt {
		t.Fatal("typing q closed the prompt — the app would have quit")
	}
	if got := m.prompt.Value(); got != "queue" {
		t.Fatalf("prompt value = %q, want \"queue\"", got)
	}
	if _, cmd := m.Update(keyMsg("q")); cmd != nil {
		if msg := cmd(); msg != nil {
			if _, quit := msg.(tea.QuitMsg); quit {
				t.Fatal("q in a prompt produced a quit")
			}
		}
	}
}

func TestEscExitsEveryMode(t *testing.T) {
	cases := []struct {
		open string
		want Mode
	}{
		{"/", ModePrompt},
		{"s", ModePrompt},
		{"p", ModePublish},
		{"?", ModeHelp},
		{"X", ModeConfirm},
		{"ctrl+l", ModeLogs},
	}
	for _, c := range cases {
		m := newTestModel(t, 120, 40)
		m = press(t, m, c.open)
		if m.Mode() != c.want {
			t.Fatalf("%q opened mode %v, want %v", c.open, m.Mode(), c.want)
		}
		m = press(t, m, "esc")
		if m.Mode() != ModeNormal {
			t.Fatalf("esc did not close the mode opened by %q", c.open)
		}
	}
}

func TestCtrlCQuitsFromEveryMode(t *testing.T) {
	for _, open := range []string{"/", "p", "?", "ctrl+l"} {
		m := newTestModel(t, 120, 40)
		m = press(t, m, open)
		_, cmd := m.Update(keyMsg("ctrl+c"))
		if cmd == nil {
			t.Fatalf("ctrl+c in mode opened by %q produced no command", open)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("ctrl+c in mode opened by %q did not quit", open)
		}
	}
}

func TestInvalidFilterIsRejectedInline(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = press(t, m, "s")
	m = press(t, m, "backspace")
	for _, r := range "a/#/b" {
		m = press(t, m, string(r))
	}
	next, cmd := m.Update(keyMsg("enter"))
	m = next.(Model)
	if m.Mode() != ModePrompt {
		t.Fatal("an invalid filter closed the prompt")
	}
	if m.prompt.Err == nil {
		t.Fatal("no inline error was shown for an invalid filter")
	}
	if cmd != nil {
		t.Fatal("an MQTT call was issued for an invalid filter")
	}
	if len(m.subscriptions) != 0 {
		t.Fatal("an invalid filter was recorded as a subscription")
	}
}

// Pause must actually stop the displayed data from changing. Every panel
// reads from the store, so a paused view means the store write is deferred.
func TestPauseFreezesTheViewAndResumeAppliesTheBacklog(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = ingest(m, "a/1")
	frozen := m.View().Content
	topicsBefore := len(m.store.Flatten())

	m = press(t, m, "space")
	if !m.Paused() {
		t.Fatal("space did not pause")
	}

	m = ingest(m, "b/1", "b/2", "c/1")
	if got := len(m.store.Flatten()); got != topicsBefore {
		t.Fatalf("the tree grew from %d to %d nodes while paused", topicsBefore, got)
	}
	if m.Buffered() != 3 {
		t.Fatalf("held %d messages, want 3", m.Buffered())
	}
	body := stripHeader(m.View().Content)
	if body != stripHeader(frozen) {
		t.Fatal("the panels changed while paused")
	}

	m = press(t, m, "space")
	if m.Paused() || m.Buffered() != 0 {
		t.Fatalf("resume left paused=%v buffered=%d", m.Paused(), m.Buffered())
	}
	if got := m.store.Stats().Received; got != 4 {
		t.Fatalf("Received = %d after resume, want 4 — the backlog must be applied, not discarded", got)
	}
	if len(m.store.Flatten()) <= topicsBefore {
		t.Fatal("the buffered topics never reached the tree")
	}
}

// A long pause must not grow without bound; the overflow is dropped and
// counted, never silently lost.
func TestPauseBacklogIsBounded(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.StreamHistory = 10
	m := New(Options{App: app.New(cfg, logging.Discard()), Config: cfg}).resize(120, 40)

	m = press(t, m, "space")
	topics := make([]string, 25)
	for i := range topics {
		topics[i] = fmt.Sprintf("t/%02d", i)
	}
	m = ingest(m, topics...)

	if got := m.Buffered(); got != 10 {
		t.Fatalf("held %d messages, want the stream_history cap of 10", got)
	}
	if got := m.store.Stats().Dropped; got != 15 {
		t.Fatalf("Dropped = %d, want 15", got)
	}
	if !strings.Contains(m.View().Content, "held") {
		t.Fatal("the header does not show that messages are being held")
	}
}

// The header keeps updating while paused: the whole point is to watch the
// counters climb while reading a frozen payload.
func TestPausedHeaderStillShowsActivity(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = press(t, m, "space")
	m = ingest(m, "a/1", "a/2")
	if !strings.Contains(m.View().Content, "paused") {
		t.Fatal("the paused indicator is missing")
	}
}

// stripHeader drops the first line, which carries the clock and counters and
// is expected to keep moving while paused.
func stripHeader(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Cursor stability: new topics that sort before the selection shift the
// flattened slice, and the selection must follow the node, not the index.
func TestCursorStabilityUnderInsertion(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = ingest(m, "zzz/leaf")
	m = press(t, m, "1", "l") // expand zzz, land on the leaf
	for m.topics.Selected() != "zzz/leaf" {
		m = press(t, m, "j")
		if m.topics.Cursor() >= len(m.store.Flatten())-1 {
			break
		}
	}
	if m.topics.Selected() != "zzz/leaf" {
		t.Fatalf("setup failed; selection is %q", m.topics.Selected())
	}
	beforeIdx := m.topics.Cursor()

	newTopics := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		newTopics = append(newTopics, fmt.Sprintf("aaa/%03d", i))
	}
	m = ingest(m, newTopics...)

	if got := m.topics.Selected(); got != "zzz/leaf" {
		t.Fatalf("selection drifted to %q after 100 insertions", got)
	}
	if m.topics.Cursor() == beforeIdx && len(m.store.Flatten()) > beforeIdx+1 {
		t.Fatal("the cursor index did not move even though the slice shifted")
	}
}

func TestLayoutAtThreeSizes(t *testing.T) {
	for _, c := range []struct{ w, h int }{{80, 24}, {120, 40}, {30, 10}} {
		l := ComputeLayout(c.w, c.h, false)
		if l.TooSmall {
			continue
		}
		if l.Stacked {
			if l.LeftW != c.w {
				t.Errorf("%dx%d stacked layout left width = %d", c.w, c.h, l.LeftW)
			}
			continue
		}
		if l.LeftW+l.RightW != c.w {
			t.Errorf("%dx%d columns sum to %d, want %d", c.w, c.h, l.LeftW+l.RightW, c.w)
		}
		if l.TopicsH+l.SubsH != l.BodyH || l.MessagesH+l.DetailH != l.BodyH {
			t.Errorf("%dx%d rows do not fill the body: %+v", c.w, c.h, l)
		}
		if l.HeaderH+l.BannerH+l.BodyH+l.FooterH != c.h {
			t.Errorf("%dx%d frame height = %d, want %d", c.w, c.h,
				l.HeaderH+l.BannerH+l.BodyH+l.FooterH, c.h)
		}
	}
}

// Panels must degrade on a tiny terminal, not panic.
func TestRenderAtDegenerateSizes(t *testing.T) {
	for _, c := range []struct{ w, h int }{{80, 24}, {120, 40}, {30, 10}, {10, 4}, {1, 1}, {0, 0}} {
		m := newTestModel(t, c.w, c.h)
		m = ingest(m, "home/livingroom/temperature", "devices/a/state")
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("View panicked at %dx%d: %v", c.w, c.h, r)
				}
			}()
			_ = m.View()
		}()
	}
}

func TestViewFitsTheTerminal(t *testing.T) {
	for _, c := range []struct{ w, h int }{{80, 24}, {120, 40}} {
		m := newTestModel(t, c.w, c.h)
		m = ingest(m, "home/livingroom/temperature", "home/kitchen/humidity", "devices/a/state")
		out := m.View().Content
		lines := strings.Split(out, "\n")
		if len(lines) > c.h {
			t.Errorf("%dx%d rendered %d rows, want at most %d", c.w, c.h, len(lines), c.h)
		}
	}
}

func TestHelpOverlayListsEveryBinding(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = press(t, m, "?")
	out := m.View().Content
	for _, want := range []string{"subscribe", "publish", "pause", "reconnect", "copy payload"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help overlay does not document %q", want)
		}
	}
}

func TestFilterNarrowsAndEscRestores(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = ingest(m, "home/kitchen/temp", "devices/lamp")
	for _, n := range m.store.Flatten() {
		n.SetExpanded(true)
	}
	m.store.Invalidate()
	full := len(m.store.Flatten())

	m = press(t, m, "/")
	for _, r := range "kitchen" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "enter")
	if got := len(m.store.Flatten()); got >= full {
		t.Fatalf("filter did not narrow the tree: %d of %d nodes", got, full)
	}

	m = press(t, m, "esc")
	if got := len(m.store.Flatten()); got != full {
		t.Fatalf("esc did not restore the tree: %d of %d nodes", got, full)
	}
}

func TestClearAllRequiresConfirmation(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = ingest(m, "a/1", "a/2")
	m = press(t, m, "X")
	if m.Mode() != ModeConfirm {
		t.Fatal("X did not ask for confirmation")
	}
	m = press(t, m, "n")
	if m.store.Stats().Received == 0 {
		t.Fatal("declining the confirmation still cleared the store")
	}
	m = press(t, m, "X", "y")
	if m.store.Stats().Received != 0 || len(m.store.Flatten()) != 0 {
		t.Fatal("confirming did not clear the store")
	}
}

func TestClearTopicEmptiesTheRingButKeepsTheNode(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = ingest(m, "a/b", "a/b")
	m = press(t, m, "1", "l", "l")
	if m.topics.Selected() != "a/b" {
		t.Skipf("selection is %q, not the leaf", m.topics.Selected())
	}
	m = press(t, m, "x")
	n := m.store.Node("a/b")
	if n == nil {
		t.Fatal("clear-topic removed the node")
	}
	if n.History.Len() != 0 {
		t.Fatalf("history still holds %d messages", n.History.Len())
	}
}

func TestPublishModalProducesTheExpectedRequest(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = ingest(m, "home/lamp")
	m = press(t, m, "1", "l")
	m = press(t, m, "p")
	if m.Mode() != ModePublish {
		t.Fatal("p did not open the publish modal")
	}
	// Tab to the payload, type, then to QoS and retain.
	m = press(t, m, "tab")
	for _, r := range "on" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "tab", "2", "tab", "space")

	req := m.publish.Request()
	if string(req.Payload) != "on" {
		t.Fatalf("payload = %q", req.Payload)
	}
	if req.QoS != 2 || !req.Retain {
		t.Fatalf("qos = %d retain = %v", req.QoS, req.Retain)
	}
	if req.Topic == "" {
		t.Fatal("topic was not prefilled from the selection")
	}
}

func TestQoSDowngradeSurfacesAToast(t *testing.T) {
	m := newTestModel(t, 120, 40)
	next, cmd := m.Update(app.SubResultMsg{Subs: []mqtt.Subscription{
		{Filter: "a/#", QoS: 2, GrantedQoS: 0, Active: true},
	}})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("a QoS downgrade produced no notification")
	}
	if len(m.subscriptions) != 1 {
		t.Fatal("the subscription was not recorded")
	}
	found := false
	collect(cmd, func(msg tea.Msg) {
		if tm, ok := msg.(app.ToastMsg); ok && strings.Contains(tm.Text, "QoS 0") {
			found = true
		}
	})
	if !found {
		t.Fatal("the downgrade toast does not mention the granted QoS")
	}
}

func TestDropCounterIsVisibleInTheHeader(t *testing.T) {
	m := newTestModel(t, 120, 40)
	next, _ := m.Update(app.BatchMsg{Msgs: nil, Dropped: 1204})
	m = next.(Model)
	if !strings.Contains(m.View().Content, "drop") {
		t.Fatal("a lossy view does not say so in the header")
	}
}

// collect walks a command's result, flattening batches.
func collect(cmd tea.Cmd, fn func(tea.Msg)) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			collect(c, fn)
		}
		return
	}
	fn(msg)
}

// The message list must hold its selection on a message, not on an index.
// Index 0 means "the newest", so every arrival reassigns every index and the
// selection slides onto its neighbour — which is what the user sees as the
// list shifting out from under them.
func TestMessageSelectionStaysOnTheSameMessage(t *testing.T) {
	m := newTestModel(t, 120, 40)
	for i := 0; i < 5; i++ {
		m = ingestTopic(m, "a/b", fmt.Sprintf("payload-%d", i))
	}
	m = press(t, m, "1", "l", "l")
	if m.messages.Topic() != "a/b" {
		t.Skipf("selection is %q, not the leaf", m.messages.Topic())
	}

	// Step off the newest entry, which also turns autoscroll off.
	m = press(t, m, "2", "j", "j")
	selected := m.messages.Current(m.context())
	if selected == nil {
		t.Fatal("nothing selected")
	}
	seq, payload := selected.Seq, string(selected.Payload)

	for i := 0; i < 4; i++ {
		m = ingestTopic(m, "a/b", fmt.Sprintf("newer-%d", i))
	}

	after := m.messages.Current(m.context())
	if after == nil {
		t.Fatal("the selection was lost when new messages arrived")
	}
	if after.Seq != seq {
		t.Fatalf("selection moved from seq %d (%s) to seq %d (%s)",
			seq, payload, after.Seq, after.Payload)
	}
	// The detail pane reads through the same selection, so it follows.
	if d := m.currentMessage(); d == nil || d.Seq != seq {
		t.Fatal("the detail pane drifted onto a different message")
	}
}

// With autoscroll on, the newest message is the selection by definition.
func TestAutoscrollKeepsFollowingTheNewestMessage(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = ingestTopic(m, "a/b", "first")
	m = press(t, m, "1", "l", "l", "2")
	m = ingestTopic(m, "a/b", "second")

	cur := m.messages.Current(m.context())
	if cur == nil || string(cur.Payload) != "second" {
		t.Fatalf("autoscroll did not follow the tail: %v", cur)
	}
}

// When the anchored message ages out of the ring the cursor must hold at the
// oldest surviving message rather than jumping to the newest.
func TestSelectionHoldsWhenTheAnchoredMessageAgesOut(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.PerTopicHistory = 4
	m := New(Options{App: app.New(cfg, logging.Discard()), Config: cfg}).resize(120, 40)
	for i := 0; i < 4; i++ {
		m = ingestTopic(m, "a/b", fmt.Sprintf("old-%d", i))
	}
	m = press(t, m, "1", "l", "l", "2", "j", "j", "j")

	for i := 0; i < 10; i++ {
		m = ingestTopic(m, "a/b", fmt.Sprintf("new-%d", i))
	}
	cur := m.messages.Current(m.context())
	if cur == nil {
		t.Fatal("the selection was lost entirely")
	}
	oldest := m.store.Node("a/b").History.At(0)
	if cur.Seq != oldest.Seq {
		t.Fatalf("selection is seq %d, want the oldest surviving seq %d", cur.Seq, oldest.Seq)
	}
}

var ingestSeq uint64

// ingestTopic delivers one message to a topic, with a fresh sequence number.
func ingestTopic(m Model, topic, payload string) Model {
	ingestSeq++
	next, _ := m.Update(app.BatchMsg{Msgs: []*mqtt.Message{{
		Seq:        ingestSeq,
		Topic:      topic,
		Payload:    []byte(payload),
		ReceivedAt: time.Unix(0, int64(ingestSeq)).UTC(),
	}}})
	return next.(Model)
}
