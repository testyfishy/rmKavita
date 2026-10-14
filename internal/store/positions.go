package store

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Pos is where the reader was in one chapter. It stores ids and a title,
// never a URL (a URL carries the API key) and never an image.
type Pos struct {
	Library int    `json:"l"`
	Series  int    `json:"s"`
	Volume  int    `json:"v"`
	Chapter int    `json:"c"`
	Title   string `json:"t"`
	Page    int    `json:"p"` // zero-based index of the page last shown
	Count   int    `json:"n"`
	Y       int    `json:"y,omitempty"` // offset inside that page, in thousandths of its fitted extent
	Kind    string `json:"k,omitempty"` // document kind ("pdf", "comic"), "" = unknown
	At      int64  `json:"at"`
}

type bucket struct {
	Last     int            `json:"last"`
	Chapters map[string]Pos `json:"chapters"`
}

// fileFormat is the whole state file: reading positions and nothing else,
// grouped per account ("user@host:port"). Chapters/Last at the top level are the
// version-1 layout (one anonymous group); the first account that signs in
// adopts them. (Older files carried a "prefs" object; it is ignored and dropped
// on the next save: the settings file is the one place settings live.)
type fileFormat struct {
	Version  int                `json:"v"`
	Last     int                `json:"last,omitempty"`
	Chapters map[string]Pos     `json:"chapters,omitempty"`
	Owners   map[string]*bucket `json:"owners,omitempty"`
}

const maxEntries = 300

// OwnerKey builds the grouping key of an account: user name + server address.
// The scheme is left out so the same server reached as http or https shares
// its positions.
func OwnerKey(server, user string) string {
	s := server
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimRight(s, "/")
	return strings.ToLower(strings.TrimSpace(user)) + "@" + strings.ToLower(s)
}

// Positions is the on-disk reading-position store.
type Positions struct {
	mu       sync.Mutex
	path     string
	data     fileFormat
	owner    string
	dirty    bool
	lastSave time.Time
	Now      func() time.Time
}

// OpenPositions loads path (a missing or corrupt file starts empty; corrupt
// content is kept aside as .bad so nothing is silently destroyed).
func OpenPositions(path string) *Positions {
	p := &Positions{path: path, Now: time.Now}
	p.data = fileFormat{Version: 2, Chapters: map[string]Pos{}, Owners: map[string]*bucket{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	var f fileFormat
	if err := json.Unmarshal(b, &f); err != nil || (f.Chapters == nil && f.Owners == nil) {
		_ = os.Rename(path, path+".bad")
		return p
	}
	if f.Chapters == nil {
		f.Chapters = map[string]Pos{}
	}
	if f.Owners == nil {
		f.Owners = map[string]*bucket{}
	}
	f.Version = 2
	p.data = f
	return p
}

// SetOwner selects the account whose positions are read and written ("" = the
// anonymous group, as in version 1). The first account to be selected adopts
// the anonymous positions of an older install.
func (p *Positions) SetOwner(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.owner = key
	if key == "" {
		return
	}
	b := p.data.Owners[key]
	if b == nil {
		b = &bucket{Chapters: map[string]Pos{}}
		p.data.Owners[key] = b
	}
	if len(p.data.Chapters) > 0 && len(p.data.Owners) == 1 && len(b.Chapters) == 0 {
		for k, v := range p.data.Chapters {
			b.Chapters[k] = v
		}
		b.Last = p.data.Last
		p.data.Chapters, p.data.Last = map[string]Pos{}, 0
		p.dirty = true
	}
}

// cur returns the group in use. Caller holds the lock.
func (p *Positions) cur() (map[string]Pos, *int) {
	if p.owner == "" {
		return p.data.Chapters, &p.data.Last
	}
	b := p.data.Owners[p.owner]
	if b == nil {
		b = &bucket{Chapters: map[string]Pos{}}
		p.data.Owners[p.owner] = b
	}
	if b.Chapters == nil {
		b.Chapters = map[string]Pos{}
	}
	return b.Chapters, &b.Last
}

// Get returns the stored position of a chapter.
func (p *Positions) Get(chapter int) (Pos, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, _ := p.cur()
	v, ok := m[strconv.Itoa(chapter)]
	return v, ok
}

// Set records a position and marks the store dirty.
func (p *Positions) Set(v Pos) {
	if v.Chapter == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	m, last := p.cur()
	v.At = p.Now().Unix()
	old, had := m[strconv.Itoa(v.Chapter)]
	if had && old.Page == v.Page && old.Count == v.Count && old.Title == v.Title && old.Y == v.Y && old.Kind == v.Kind {
		*last = v.Chapter
		return
	}
	m[strconv.Itoa(v.Chapter)] = v
	*last = v.Chapter
	p.dirty = true
	if len(m) > maxEntries {
		type kv struct {
			k string
			t int64
		}
		var all []kv
		for k, x := range m {
			all = append(all, kv{k, x.At})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].t < all[j].t })
		for _, e := range all[:len(all)-maxEntries] {
			delete(m, e.k)
		}
	}
}

// Last returns the most recently read chapter.
func (p *Positions) Last() (Pos, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, last := p.cur()
	v, ok := m[strconv.Itoa(*last)]
	return v, ok
}

// Dirty reports unsaved changes.
func (p *Positions) Dirty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dirty
}

// FlushIfDue saves when dirty and at least minGap since the last save.
func (p *Positions) FlushIfDue(minGap time.Duration) error {
	p.mu.Lock()
	due := p.dirty && p.Now().Sub(p.lastSave) >= minGap
	p.mu.Unlock()
	if !due {
		return nil
	}
	return p.Flush()
}

// Flush writes the file atomically: temp file, fsync, rename.
func (p *Positions) Flush() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dirty {
		return nil
	}
	if p.path == "" {
		return errors.New("no path")
	}
	out := p.data
	out.Version = 2
	// Empty groups are not written.
	out.Owners = map[string]*bucket{}
	for k, b := range p.data.Owners {
		if b != nil && len(b.Chapters) > 0 {
			out.Owners[k] = b
		}
	}
	if len(p.data.Chapters) == 0 {
		out.Chapters, out.Last = nil, 0
	}
	if len(out.Owners) == 0 {
		out.Owners = nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(p.path, b, 0o600); err != nil {
		return err
	}
	p.dirty = false
	p.lastSave = p.Now()
	return nil
}
