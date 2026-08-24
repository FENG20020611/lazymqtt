package theme

import "testing"

func TestForHonoursThePreference(t *testing.T) {
	cases := []struct {
		pref   string
		darkBG bool
		want   *Palette
	}{
		{"dark", false, Dark},    // an explicit choice beats the terminal
		{"light", true, Light},   // and in the other direction too
		{"auto", true, Dark},     // auto follows what the terminal reported
		{"auto", false, Light},   //
		{"", true, Dark},         // an unset theme is auto
		{"", false, Light},       //
		{"nonsense", true, Dark}, /* invalid values are caught by config validation */
	}
	for _, c := range cases {
		if got := For(c.pref, c.darkBG); got != c.want {
			t.Errorf("For(%q, %v) picked the wrong palette", c.pref, c.darkBG)
		}
	}
}

// The two palettes must actually differ. Deriving one from the other by hand
// makes it easy to leave a colour behind, and a light theme with the dark
// theme's foreground is unreadable rather than subtly off.
func TestLightAndDarkDiffer(t *testing.T) {
	if Dark.Base.GetForeground() == Light.Base.GetForeground() {
		t.Error("the light and dark palettes share a foreground colour")
	}
	if Dark.Selected.GetBackground() == Light.Selected.GetBackground() {
		t.Error("the light and dark palettes share a selection background")
	}
}
