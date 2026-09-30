// Package session measures how long this server process has been running
// (in most harnesses, one process per session) and which other sessions ran
// at the same time, so the time to log can be suggested to the user.
// It never logs time itself.
//
// Each process keeps a small record in a directory shared by all of them
// (by default ~/.devpulse/sessions), refreshed by a heartbeat. A record whose
// heartbeat stopped belongs to a process that ended without saying so.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	Heartbeat  = time.Minute
	staleAfter = 3 * Heartbeat
	keepFor    = 7 * 24 * time.Hour
	maxSince   = 7 * 24 * time.Hour
)

// seq keeps record names unique within a process.
var seq atomic.Int64

// Record is what a process writes about itself.
type Record struct {
	PID      int        `json:"pid"`
	Start    time.Time  `json:"start"`
	LastSeen time.Time  `json:"lastSeen"`
	End      *time.Time `json:"end,omitempty"`
	Dir      string     `json:"dir,omitempty"`
}

// Tracker records this process and reads the others.
type Tracker struct {
	dir, file string // dir == "": in memory only, no other sessions
	now       func() time.Time

	mu  sync.Mutex
	rec Record
}

// Start begins tracking. dir is where records are shared ("" disables
// sharing); now is the clock (nil = time.Now). Problems with the directory
// never stop the server: the tracker falls back to memory.
func Start(dir string, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	t := &Tracker{dir: dir, now: now}
	start := now()
	wd, _ := os.Getwd()
	t.rec = Record{PID: os.Getpid(), Start: start, LastSeen: start, Dir: wd}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.dir = ""
			return t
		}
		t.file = filepath.Join(dir, fmt.Sprintf("%d-%d-%d.json", t.rec.PID, start.UnixNano(), seq.Add(1)))
		t.prune()
		if t.write() != nil {
			t.dir = ""
		}
	}
	return t
}

// Run refreshes the heartbeat until ctx is done, then marks the end.
func (t *Tracker) Run(ctx context.Context) {
	tick := time.NewTicker(Heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-tick.C:
			t.Beat()
		}
	}
}

// Beat refreshes the heartbeat.
func (t *Tracker) Beat() {
	t.mu.Lock()
	t.rec.LastSeen = t.now()
	t.mu.Unlock()
	_ = t.write()
}

// Stop marks this session as ended.
func (t *Tracker) Stop() {
	t.mu.Lock()
	end := t.now()
	t.rec.LastSeen, t.rec.End = end, &end
	t.mu.Unlock()
	_ = t.write()
}

func (t *Tracker) write() error {
	if t.dir == "" {
		return nil
	}
	t.mu.Lock()
	b, err := json.Marshal(t.rec)
	t.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := t.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.file)
}

// prune removes records that ended more than a week ago.
func (t *Tracker) prune() {
	for _, o := range t.others() {
		if t.now().Sub(o.until) > keepFor {
			_ = os.Remove(o.file)
		}
	}
}

type other struct {
	Record
	file   string
	until  time.Time // end, or the last heartbeat if the process vanished
	active bool
}

func (t *Tracker) others() []other {
	if t.dir == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(t.dir, "*.json"))
	var out []other
	now := t.now()
	for _, f := range files {
		if f == t.file {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue // being replaced right now; it is read next time
		}
		var r Record
		if json.Unmarshal(b, &r) != nil || r.Start.IsZero() {
			continue
		}
		o := other{Record: r, file: f, until: r.LastSeen}
		switch {
		case r.End != nil:
			o.until = *r.End
		case now.Sub(r.LastSeen) <= staleAfter:
			o.until, o.active = now, true
		}
		out = append(out, o)
	}
	return out
}

// Other is another session that overlaps the reported period.
type Other struct {
	Start          string `json:"start"`
	End            string `json:"end,omitempty"`
	Active         bool   `json:"active"`
	Dir            string `json:"dir,omitempty"`
	OverlapMinutes int    `json:"overlapMinutes"`
}

// Report is the time of this session and what overlapped it.
type Report struct {
	From          string  `json:"from"`
	To            string  `json:"to"`
	FromSource    string  `json:"fromSource"` // "session start" or "since"
	Elapsed       string  `json:"elapsed"`
	ElapsedHours  float64 `json:"elapsedHours"`
	OtherSessions []Other `json:"otherSessions"`
	// OverlapHours is how much of the period had at least one other
	// session running (overlaps between the others are not double counted).
	OverlapHours float64 `json:"overlapHours"`
	Note         string  `json:"note"`
}

// Report measures from the session start, or from since when not zero.
func (t *Tracker) Report(since time.Time) (*Report, error) {
	now := t.now()
	t.mu.Lock()
	from, src := t.rec.Start, "session start"
	t.mu.Unlock()
	if !since.IsZero() {
		if since.After(now) {
			return nil, errors.New("since está no futuro")
		}
		if now.Sub(since) > maxSince {
			return nil, errors.New("since pode voltar no máximo 7 dias")
		}
		from, src = since, "since"
	}
	r := &Report{
		From: from.Format(time.RFC3339), To: now.Format(time.RFC3339), FromSource: src,
		Elapsed: human(now.Sub(from)), ElapsedHours: hours(now.Sub(from)), OtherSessions: []Other{},
	}
	var spans [][2]time.Time
	for _, o := range t.others() {
		s, e := maxTime(o.Start, from), minTime(o.until, now)
		if !e.After(s) {
			continue
		}
		spans = append(spans, [2]time.Time{s, e})
		x := Other{Start: o.Start.Format(time.RFC3339), Active: o.active, Dir: o.Dir, OverlapMinutes: int(e.Sub(s).Minutes())}
		if !o.active {
			x.End = o.until.Format(time.RFC3339)
		}
		r.OtherSessions = append(r.OtherSessions, x)
	}
	sort.Slice(r.OtherSessions, func(i, j int) bool { return r.OtherSessions[i].Start < r.OtherSessions[j].Start })
	r.OverlapHours = hours(union(spans))
	r.Note = "Sugestão, não lançamento: confirme com o usuário quanto registrar."
	if len(r.OtherSessions) > 0 {
		r.Note += fmt.Sprintf(" %s deste período coincidem com outras sessões; somar as sessões contaria esse tempo mais de uma vez.", human(time.Duration(r.OverlapHours*float64(time.Hour))))
	}
	return r, nil
}

// ParseSince accepts HH:MM (today, local time), AAAA-MM-DDTHH:MM (local) or
// RFC 3339.
func ParseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, now.Location()); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("15:04", s, now.Location()); err == nil {
		return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location()), nil
	}
	return time.Time{}, fmt.Errorf("since inválido: %q (use HH:MM, AAAA-MM-DDTHH:MM ou RFC 3339)", s)
}

func union(spans [][2]time.Time) time.Duration {
	sort.Slice(spans, func(i, j int) bool { return spans[i][0].Before(spans[j][0]) })
	var total time.Duration
	var cur [2]time.Time
	for i, s := range spans {
		switch {
		case i == 0:
			cur = s
		case !s[0].After(cur[1]):
			cur[1] = maxTime(cur[1], s[1])
		default:
			total += cur[1].Sub(cur[0])
			cur = s
		}
	}
	if len(spans) > 0 {
		total += cur[1].Sub(cur[0])
	}
	return total
}

func hours(d time.Duration) float64 { return math.Round(d.Hours()*100) / 100 }

func human(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	if m < 60 {
		return fmt.Sprintf("%dmin", m)
	}
	return fmt.Sprintf("%dh%02d", m/60, m%60)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
