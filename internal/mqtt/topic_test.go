package mqtt

import "testing"

func TestMatchTopic(t *testing.T) {
	// Cases drawn from the MQTT 3.1.1 and 5.0 specifications.
	cases := []struct {
		filter, topic string
		want          bool
	}{
		{"sport/tennis/player1", "sport/tennis/player1", true},
		{"sport/tennis/player1", "sport/tennis/player2", false},

		{"sport/tennis/player1/#", "sport/tennis/player1", true},
		{"sport/tennis/player1/#", "sport/tennis/player1/ranking", true},
		{"sport/tennis/player1/#", "sport/tennis/player1/score/wimbledon", true},

		{"sport/#", "sport", true},
		{"sport/#", "sport/tennis", true},
		{"#", "sport/tennis/player1", true},
		{"#", "a", true},

		{"sport/tennis/+", "sport/tennis/player1", true},
		{"sport/tennis/+", "sport/tennis/player1/ranking", false},
		{"sport/+", "sport", false},
		{"sport/+", "sport/", true},
		{"+/tennis/#", "sport/tennis/player1", true},
		{"+", "sport", true},
		{"+/+", "/finance", true},
		{"/+", "/finance", true},
		{"+", "/finance", false},

		// $-prefixed topics are invisible to a leading wildcard.
		{"#", "$SYS/broker/uptime", false},
		{"+/monitor/Clients", "$SYS/monitor/Clients", false},
		{"$SYS/#", "$SYS/broker/uptime", true},
		{"$SYS/monitor/+", "$SYS/monitor/Clients", true},

		{"sport/tennis#", "sport/tennis", false},
		{"", "sport", false},
		{"sport", "", false},
	}
	for _, c := range cases {
		if got := MatchTopic(c.filter, c.topic); got != c.want {
			t.Errorf("MatchTopic(%q, %q) = %v, want %v", c.filter, c.topic, got, c.want)
		}
	}
}

func TestValidateFilter(t *testing.T) {
	valid := []string{"#", "+", "sport/#", "sport/+/player1", "a/b/c", "/", "//", "$SYS/#"}
	for _, f := range valid {
		if err := ValidateFilter(f); err != nil {
			t.Errorf("ValidateFilter(%q) = %v, want nil", f, err)
		}
	}
	invalid := []string{"", "sport/#/ranking", "sport/tennis#", "sport+", "#/a", "a/b+c"}
	for _, f := range invalid {
		if err := ValidateFilter(f); err == nil {
			t.Errorf("ValidateFilter(%q) = nil, want an error", f)
		}
	}
	if err := ValidateFilter(string(make([]byte, MaxTopicBytes+1))); err == nil {
		t.Error("over-long filter accepted")
	}
}

func TestValidateTopic(t *testing.T) {
	if err := ValidateTopic("a/b/c"); err != nil {
		t.Errorf("ValidateTopic(a/b/c) = %v", err)
	}
	for _, bad := range []string{"", "a/#", "a/+/b", "a\x00b"} {
		if err := ValidateTopic(bad); err == nil {
			t.Errorf("ValidateTopic(%q) = nil, want an error", bad)
		}
	}
}

// FuzzMatchTopic asserts the invariants that hold regardless of input: a
// valid filter never panics, and an exact non-wildcard filter matches only
// itself.
func FuzzMatchTopic(f *testing.F) {
	f.Add("sport/#", "sport/tennis")
	f.Add("+/+", "a/b")
	f.Add("#", "$SYS/x")
	f.Fuzz(func(t *testing.T, filter, topic string) {
		got := MatchTopic(filter, topic)
		if !HasWildcard(filter) && got != (filter == topic && filter != "") {
			t.Fatalf("exact filter %q vs %q = %v", filter, topic, got)
		}
	})
}
