package ui

// zone is what a tap on the page means.
type zone int

const (
	zoneLeft   zone = iota // left column: page turn (direction decides which way)
	zoneRight              // right column: page turn
	zoneReveal             // middle column: show or hide the top and bottom bars
)

// zoneAt is the tap-zone table: three columns across the whole height of the
// screen. The left and the right column turn pages, the middle one (middlePct
// percent of the width, 40 by default; hand-edit zone_middle_pct, 20-60) opens
// and closes the bars. A column is easy to hit; an exact midline is not.
//
//	+--------+----------------+--------+
//	|  left  |     middle     | right  |
//	|  turn  |   bars on/off  |  turn  |
//	|  30 %  |      40 %      |  30 %  |
//	+--------+----------------+--------+
//
// y and h are unused: the columns run the full height.
func zoneAt(x, y, w, h, middlePct int) zone {
	if middlePct < 20 {
		middlePct = 20
	}
	if middlePct > 60 {
		middlePct = 60
	}
	side := w * (100 - middlePct) / 200 // width of one side column
	switch {
	case x < side:
		return zoneLeft
	case x >= w-side:
		return zoneRight
	}
	return zoneReveal
}
