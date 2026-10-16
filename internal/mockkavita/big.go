package mockkavita

import (
	"fmt"
	"sort"
	"strings"
)

// BigOptions shape the big synthetic library of AddBigLibrary.
type BigOptions struct {
	Series      int  // number of series (900+ is the interesting range)
	ArticleSort bool // sort as a server's sort name does: a leading The / A / An does not count
	Unsorted    bool // leave the series in generation order (not alphabetical)
}

var bigAdj = []string{"Silent", "Crimson", "Hidden", "Iron", "Paper", "Midnight", "Golden", "Frozen", "Wandering", "Broken",
	"Sleepy", "Electric", "Velvet", "Hollow", "Distant", "Gentle", "Rusty", "Lunar", "Wild", "Quiet", "Brave", "Scarlet",
	"Amber", "Ivory", "Cobalt", "Jade", "Misty", "Stormy", "Sunny", "Tiny", "Ancient", "Clockwork", "Neon", "Salt", "Bitter",
	"Fading", "Rising", "Falling", "Secret", "Last"}

var bigNoun = []string{"Garden", "Kingdom", "Harbor", "Lantern", "Compass", "Orchard", "Cathedral", "Voyage", "Archive",
	"Bridge", "Tower", "Meadow", "Station", "Circus", "Library", "Engine", "Theatre", "Fortress", "Village", "Mirror",
	"Canyon", "Lighthouse", "Market", "Palace", "Island", "Forest", "Workshop", "Observatory", "Diary", "Orchestra",
	"Railway", "Sanctuary", "Festival", "Academy", "Bakery", "Detective", "Pilgrim", "Captain", "Gardener", "Alchemist"}

// BigName is the generated title of series i of the big library: two words from
// the lists above, with a few awkward ones mixed in on purpose (a leading
// article, an accent, a digit, a symbol, Japanese) so that the letter index has
// something to classify. The names are made up; no real title appears.
func BigName(i int) string {
	a := bigAdj[(i*7+i/len(bigAdj))%len(bigAdj)]
	n := bigNoun[(i*13+i/len(bigNoun))%len(bigNoun)]
	base := fmt.Sprintf("%s %s", a, n)
	switch {
	case i%53 == 7:
		return "The " + base
	case i%97 == 11:
		return "A " + base
	case i%131 == 5:
		return "Éclat of the " + n
	case i%89 == 3:
		return fmt.Sprintf("%d %s", 100+i%900, base)
	case i%149 == 9:
		return "[" + base + "]"
	case i%173 == 1:
		return "日本の" + n
	case i%61 == 2:
		return "Ōkami " + n
	}
	return base
}

func foldKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'é', 'è', 'ê', 'ë':
			r = 'e'
		case 'ō', 'ö', 'ó', 'ô':
			r = 'o'
		case 'å', 'ä', 'à', 'á', 'â':
			r = 'a'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func sortName(name string, article bool) string {
	k := foldKey(strings.TrimLeft(name, "[ "))
	if article {
		for _, p := range []string{"the ", "an ", "a "} {
			if strings.HasPrefix(k, p) {
				return k[len(p):]
			}
		}
	}
	return k
}

// AddBigLibrary adds a library "Big Library" with o.Series one-chapter series
// (three small pages each) and returns its id. The existing libraries and their
// order are untouched, so a test that does not ask for it sees the old catalogue.
func (s *Server) AddBigLibrary(o BigOptions) int {
	if o.Series <= 0 {
		o.Series = 900
	}
	id := len(s.libs) + 1
	lib := &libT{id: id, name: "Big Library"}
	nextSeries, nextChap := 1000, 100000
	for _, sr := range s.seriesBy {
		if sr.id >= nextSeries {
			nextSeries = sr.id + 1
		}
	}
	for _, c := range s.chapBy {
		if c.id >= nextChap {
			nextChap = c.id + 1
		}
	}
	names := make([]string, 0, o.Series)
	seen := map[string]bool{}
	for i := 0; len(names) < o.Series; i++ {
		n := BigName(i)
		if seen[n] {
			n = fmt.Sprintf("%s %d", n, i)
		}
		seen[n] = true
		names = append(names, n)
	}
	if !o.Unsorted {
		article := o.ArticleSort
		sort.SliceStable(names, func(i, j int) bool { return sortName(names[i], article) < sortName(names[j], article) })
	}
	for _, name := range names {
		sr := &seriesT{id: nextSeries, lib: id, name: name, kind: "jpeg"}
		nextSeries++
		ch := &chapter{id: nextChap, vol: nextChap, series: sr.id, lib: id, title: name + " - Volume 1", pages: 3, w: 300, h: 450,
			kind: "jpeg", seed: int64(nextChap) * 7919}
		nextChap++
		sr.chapters = append(sr.chapters, ch)
		s.chapBy[ch.id] = ch
		s.seriesBy[sr.id] = sr
		lib.series = append(lib.series, sr)
	}
	s.libs = append(s.libs, lib)
	return id
}
