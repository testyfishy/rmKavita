package ui

import (
	"fmt"
	"image"
	"regexp"
	"strings"
	"testing"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/mockkavita"
	"rmkavita/internal/store"
)

// ---- letters ---------------------------------------------------------------------------------

func TestLetterOfClassifiesTitles(t *testing.T) {
	for _, tc := range []struct {
		title string
		art   bool
		want  string
	}{
		{"Akira", false, "A"}, {"akira", false, "A"}, {"Zetsuen", false, "Z"},
		{"Éclat of the Garden", false, "E"}, {"Ōkami Village", false, "O"}, {"Ångström", false, "A"}, {"Çok Güzel", false, "C"},
		{"Straße", false, "S"}, {"Łódź", false, "L"}, {"Þór", false, "T"},
		{"100 Days", false, "#"}, {"7 Seeds", false, "#"}, {"日本の庭", false, "#"}, {"Привет", false, "#"},
		{"[Oshi no Ko]", false, "O"}, {"  - ...Hello", false, "H"}, {"!!!", false, "#"}, {"", false, "#"},
		{"The Silent Garden", false, "T"}, {"The Silent Garden", true, "S"},
		{"A Gentle Mirror", false, "A"}, {"A Gentle Mirror", true, "G"}, {"An Iron Tower", true, "I"},
		{"The", true, "T"}, {"Theatre of Dreams", true, "T"}, {"Another Day", true, "A"},
		{"the 100", true, "#"},
	} {
		if got := letterName(letterOf(tc.title, tc.art)); got != tc.want {
			t.Errorf("letterOf(%q, art=%v) = %s, want %s", tc.title, tc.art, got, tc.want)
		}
	}
}

func rowsOf(titles ...string) []row {
	rs := make([]row, len(titles))
	for i, t := range titles {
		rs[i] = row{kind: rowFolder, title: t, read: -1}
	}
	return rs
}

func TestAlphaIndexOrderAndFirstRows(t *testing.T) {
	x := buildAlphaIdx(rowsOf("7 Seeds", "Akira", "Alice", "Bleach", "Naruto", "One", "Zero"))
	if x.ordered != 1 || x.cnt != 6 {
		t.Fatalf("ordered=%v cnt=%d", x.ordered, x.cnt)
	}
	for letter, want := range map[string]int{"#": 0, "A": 1, "B": 3, "N": 4, "O": 5, "Z": 6, "C": -1} {
		i := 0
		if letter != "#" {
			i = int(letter[0]-'A') + 1
		}
		if x.first[i] != want {
			t.Errorf("first[%s] = %d, want %d", letter, x.first[i], want)
		}
	}
	// a server that sorts by name without the article: the keys follow it
	y := buildAlphaIdx(rowsOf("Akira", "The Beach", "Bleach", "The Cat", "Dog", "A Tale", "Zoo"))
	if !y.art {
		t.Errorf("article-skipping keys were not chosen: %+v", y)
	}
	if y.keys[1] != uint8(letterOf("Beach", false)) {
		t.Errorf("The Beach keyed as %d", y.keys[1])
	}
	// not alphabetical at all
	z := buildAlphaIdx(rowsOf("Zoo", "Apple", "Mango", "Berry", "Zulu", "Cat", "Yak", "Dog", "Kite"))
	if z.ordered > 0.7 {
		t.Errorf("a shuffled list is %.2f ordered", z.ordered)
	}
}

func TestViewDecisionMatrix(t *testing.T) {
	a := &App{Set: store.DefaultSettings()}
	long := &listView{url: "u", total: 900}
	for i := 0; i < 20; i++ {
		long.rows = append(long.rows, row{kind: rowFolder, title: fmt.Sprintf("%c title", 'A'+i)})
	}
	long.next = "n"
	short := &listView{url: "s", total: 25}
	for i := 0; i < 25; i++ {
		short.rows = append(short.rows, row{kind: rowFolder, title: fmt.Sprintf("%c title", 'A'+i%26)})
	}
	search := &listView{url: "q", total: 300, search: true, rows: long.rows}
	home := &listView{home: true, total: 900, rows: long.rows}
	libs := &listView{libs: true, total: 900, rows: long.rows}
	for _, tc := range []struct {
		name            string
		lists, alpha    string
		lv              *listView
		compact, alpha_ bool
	}{
		{"long auto", "auto", "auto", long, true, true},
		{"short auto", "auto", "auto", short, false, false},
		{"short forced", "compact", "on", short, true, true},
		{"long comfortable", "comfortable", "auto", long, false, true},
		{"long bar off", "auto", "off", long, true, false},
		{"search keeps the old view", "compact", "on", search, false, false},
		{"home keeps the old view", "compact", "on", home, false, false},
		{"libraries keep the old view", "compact", "on", libs, false, false},
	} {
		a.Set.Lists, a.Set.Alpha = tc.lists, tc.alpha
		tc.lv.alpha = nil
		if got := a.compactList(tc.lv); got != tc.compact {
			t.Errorf("%s: compact=%v want %v", tc.name, got, tc.compact)
		}
		if got := a.alphaShown(tc.lv); got != tc.alpha_ {
			t.Errorf("%s: bar=%v want %v", tc.name, got, tc.alpha_)
		}
	}
	// a finished list with only one letter is not worth a bar in auto
	one := &listView{url: "v", total: 60}
	for i := 0; i < 60; i++ {
		one.rows = append(one.rows, row{kind: rowChapter, title: fmt.Sprintf("Vol. %d", i+1)})
	}
	a.Set.Lists, a.Set.Alpha = "auto", "auto"
	if a.alphaShown(one) {
		t.Error("a list of volumes got a bar with one letter")
	}
	// an unordered long list: no bar in auto, a bar when asked for
	mixed := &listView{url: "m", total: 90, next: ""}
	for i := 0; i < 90; i++ {
		mixed.rows = append(mixed.rows, row{kind: rowFolder, title: fmt.Sprintf("%c%d", 'A'+(i*7)%26, i)})
	}
	if a.alphaShown(mixed) {
		t.Error("an unordered list got a bar in auto")
	}
	a.Set.Alpha = "on"
	mixed.alpha = nil
	if !a.alphaShown(mixed) {
		t.Error("alpha on did not show the bar")
	}
}

func TestAlphaGeometryStaysOutOfTheCornersAndTheFooter(t *testing.T) {
	for _, d := range []struct{ w, h, dpi int }{{1620, 2160, 229}, {954, 1696, 264}} {
		h := startOpts(t, hopts{w: d.w, h: d.h, dpi: d.dpi, big: 120})
		h.waitTag("list", 1)
		var y0, y1, cell, listTop, listBottom, corner, barW int
		h.onUI(func() {
			a := h.app
			y0, y1 = a.alphaSpan()
			cell = (y1 - y0) / alphaLetters
			listTop, listBottom, corner, barW = a.listTop(), a.listBottom(), a.u(260), a.alphaBarW()
		})
		if y0 < listTop || y1 > listBottom {
			t.Errorf("%dx%d: strip %d..%d is outside the list area %d..%d (corner zone %d)", d.w, d.h, y0, y1, listTop, listBottom, corner)
		}
		mm := float64(cell) * 25.4 / float64(d.dpi)
		wmm := float64(barW) * 25.4 / float64(d.dpi)
		t.Logf("%dx%d @%d dpi: 27 targets of %d px = %.1f mm tall, %.1f mm wide; y0=%d listTop=%d listBottom=%d barX=%d", d.w, d.h, d.dpi, cell, mm, wmm, y0, listTop, listBottom, d.w-barW/2)
		if mm < 3.5 || wmm < 6.5 {
			t.Errorf("%dx%d: targets %.1f x %.1f mm", d.w, d.h, wmm, mm)
		}
		h.stop()
	}
	_ = image.Pt
	_ = display.ModeUI
	_ = mockkavita.BigName
	_ = regexp.MustCompile
	_ = strings.ToLower
	_ = time.Second
}
