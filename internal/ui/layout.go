package ui

// Layout is the computed geometry for one frame. Keeping the arithmetic in
// one pure function makes the degenerate cases testable without a terminal.
type Layout struct {
	Width, Height int

	HeaderH int
	BannerH int
	FooterH int
	BodyH   int

	LeftW  int
	RightW int

	TopicsH   int
	SubsH     int
	MessagesH int
	DetailH   int

	// Stacked is true on a terminal too narrow for two columns; only the
	// focused panel is drawn, full width. Panels must degrade, not panic.
	Stacked bool
	// TooSmall is true when even a single panel cannot be drawn.
	TooSmall bool
}

// Layout thresholds.
const (
	minTwoColumnWidth = 64
	minLeftWidth      = 22
	maxLeftWidth      = 44
	minPanelHeight    = 5
)

// ComputeLayout derives the frame geometry from the terminal size.
func ComputeLayout(w, h int, banner bool) Layout {
	l := Layout{Width: w, Height: h, HeaderH: 1, FooterH: 1}
	if banner {
		l.BannerH = 1
	}
	if w < 20 || h < 6 {
		l.TooSmall = true
		return l
	}

	l.BodyH = h - l.HeaderH - l.FooterH - l.BannerH
	if l.BodyH < 3 {
		l.BodyH = 3
	}

	if w < minTwoColumnWidth || l.BodyH < 2*minPanelHeight {
		l.Stacked = true
		l.LeftW, l.RightW = w, 0
		l.TopicsH, l.MessagesH, l.DetailH, l.SubsH = l.BodyH, l.BodyH, l.BodyH, l.BodyH
		return l
	}

	l.LeftW = w * 3 / 10
	if l.LeftW < minLeftWidth {
		l.LeftW = minLeftWidth
	}
	if l.LeftW > maxLeftWidth {
		l.LeftW = maxLeftWidth
	}
	l.RightW = w - l.LeftW

	// The subscriptions panel takes a quarter of the left column, bounded so
	// it neither vanishes nor crowds out the tree.
	l.SubsH = l.BodyH / 4
	if l.SubsH < minPanelHeight {
		l.SubsH = minPanelHeight
	}
	if l.SubsH > 12 {
		l.SubsH = 12
	}
	l.TopicsH = l.BodyH - l.SubsH
	if l.TopicsH < minPanelHeight {
		l.TopicsH = minPanelHeight
		l.SubsH = l.BodyH - l.TopicsH
	}

	l.MessagesH = l.BodyH / 2
	if l.MessagesH < minPanelHeight {
		l.MessagesH = minPanelHeight
	}
	l.DetailH = l.BodyH - l.MessagesH
	if l.DetailH < minPanelHeight {
		l.DetailH = minPanelHeight
		l.MessagesH = l.BodyH - l.DetailH
	}
	return l
}
