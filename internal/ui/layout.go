package ui

// Pure scrolling arithmetic. A page that does not fit the screen in the
// direction it scrolls is shown in screen-sized segments that overlap by a
// few percent, so the eye keeps its place. Nothing here knows about drawing.

// overlapPct is how much of the previous screen is repeated after a scroll.
const overlapPct = 8

// stepFor is how far one advance moves a viewport of view pixels.
func stepFor(view int) int {
	s := view - view*overlapPct/100
	if s < 1 {
		s = 1
	}
	return s
}

// segStarts returns the viewport start offsets (pixels along the scroll axis,
// in reading order) that cover an extent of ext pixels with a viewport of view
// pixels. The last one is clamped so the end of the page is shown exactly.
func segStarts(ext, view int) []int {
	if ext <= view || view <= 0 {
		return []int{0}
	}
	step := stepFor(view)
	last := ext - view
	var out []int
	for s := 0; s < last; s += step {
		out = append(out, s)
	}
	return append(out, last)
}

// segIndexFor returns the index of the last start at or before off.
func segIndexFor(starts []int, off int) int {
	idx := 0
	for i, s := range starts {
		if s <= off {
			idx = i
		}
	}
	return idx
}
