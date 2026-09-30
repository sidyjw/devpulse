package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sidyjw/devpulse/internal/mcp"
	"github.com/sidyjw/devpulse/internal/mcp/mcptest"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func at(h, m int) time.Time          { return time.Date(2026, 9, 30, h, m, 0, 0, time.Local) }
func writeRecord(t *testing.T, dir string, r Record) {
	t.Helper()
	b, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(dir, r.Start.Format("150405")+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReportOverlapsWithoutDoubleCounting(t *testing.T) {
	dir := t.TempDir()
	c := &clock{at(9, 0)}
	me := Start(dir, c.now)
	end := at(10, 0)
	// ended 09:00–10:00 and active since 09:30: together they cover 09:00–now
	writeRecord(t, dir, Record{Start: at(8, 0), LastSeen: end, End: &end, Dir: "/a"})
	c.add(2 * time.Hour) // 11:00
	writeRecord(t, dir, Record{Start: at(9, 30), LastSeen: c.t.Add(-time.Minute), Dir: "/b"})
	// vanished at 09:10 without an end: counted until its last heartbeat
	writeRecord(t, dir, Record{Start: at(8, 30), LastSeen: at(9, 10)})
	// before this session: not an overlap
	early := at(8, 45)
	writeRecord(t, dir, Record{Start: at(8, 15), LastSeen: early, End: &early})

	r, err := me.Report(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if r.ElapsedHours != 2 || r.Elapsed != "2h00" || r.FromSource != "session start" {
		t.Errorf("elapsed = %+v", r)
	}
	if len(r.OtherSessions) != 3 {
		t.Fatalf("outras = %+v", r.OtherSessions)
	}
	byStart := map[string]Other{}
	for _, o := range r.OtherSessions {
		byStart[o.Start[11:16]] = o
	}
	if o := byStart["08:00"]; o.Active || o.OverlapMinutes != 60 || o.End == "" {
		t.Errorf("encerrada = %+v", o)
	}
	if o := byStart["09:30"]; !o.Active || o.OverlapMinutes != 90 || o.End != "" {
		t.Errorf("ativa = %+v", o)
	}
	if o := byStart["08:30"]; o.Active || o.OverlapMinutes != 10 {
		t.Errorf("sumida = %+v", o)
	}
	if r.OverlapHours != 2 || !strings.Contains(r.Note, "2h00 deste período") {
		t.Errorf("sobreposição = %v / %s", r.OverlapHours, r.Note)
	}

	// since narrows the period
	r, _ = me.Report(at(10, 30))
	if r.ElapsedHours != 0.5 || r.FromSource != "since" || len(r.OtherSessions) != 1 || r.OverlapHours != 0.5 {
		t.Errorf("since = %+v", r)
	}
	if _, err := me.Report(c.t.Add(time.Hour)); err == nil {
		t.Error("since no futuro deveria falhar")
	}
	if _, err := me.Report(c.t.Add(-8 * 24 * time.Hour)); err == nil {
		t.Error("since muito antigo deveria falhar")
	}
}

func TestHeartbeatStopAndPrune(t *testing.T) {
	dir := t.TempDir()
	c := &clock{at(9, 0)}
	old := at(9, 0).Add(-8 * 24 * time.Hour)
	writeRecord(t, dir, Record{Start: old, LastSeen: old, End: &old})
	a := Start(dir, c.now)
	if files, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(files) != 1 {
		t.Errorf("registros antigos deveriam ser apagados: %v", files)
	}
	b := Start(dir, c.now)

	c.add(10 * time.Minute) // a heartbeat keeps a session active
	a.Beat()
	r, _ := b.Report(time.Time{})
	if len(r.OtherSessions) != 1 || !r.OtherSessions[0].Active {
		t.Fatalf("a deveria estar ativa: %+v", r.OtherSessions)
	}
	c.add(5 * time.Minute)
	a.Stop()
	c.add(time.Hour)
	r, _ = b.Report(time.Time{})
	if o := r.OtherSessions[0]; o.Active || o.OverlapMinutes != 15 {
		t.Errorf("a encerrada = %+v", o)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(files) != 0 {
		t.Errorf("arquivos temporários sobraram: %v", files)
	}
}

func TestMemoryOnlyAndBadDir(t *testing.T) {
	c := &clock{at(9, 0)}
	blocker := filepath.Join(t.TempDir(), "arquivo")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"", filepath.Join(blocker, "sessions")} {
		tr := Start(dir, c.now)
		c.add(45 * time.Minute)
		tr.Beat()
		r, err := tr.Report(time.Time{})
		if err != nil || r.Elapsed != "45min" || len(r.OtherSessions) != 0 {
			t.Errorf("%q: %+v %v", dir, r, err)
		}
	}
}

func TestParseSince(t *testing.T) {
	now := at(15, 0)
	for in, want := range map[string]time.Time{
		"09:30":                     at(9, 30),
		"2026-09-29T18:05":          time.Date(2026, 9, 29, 18, 5, 0, 0, time.Local),
		"2026-09-30T12:00:00-03:00": time.Date(2026, 9, 30, 12, 0, 0, 0, time.FixedZone("", -3*3600)),
	} {
		got, err := ParseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("%s → %v %v", in, got, err)
		}
	}
	if got, err := ParseSince("", now); err != nil || !got.IsZero() {
		t.Errorf("vazio → %v %v", got, err)
	}
	if _, err := ParseSince("ontem", now); err == nil {
		t.Error("deveria falhar")
	}
}

func TestTool(t *testing.T) {
	c := &clock{at(9, 0)}
	tr := Start("", c.now)
	c.add(90 * time.Minute)
	s := mcp.NewServer("t", "0", "")
	tr.Register(s)
	res := mcptest.CallTool(t, s, "session_time", map[string]any{"since": "10:00"})
	if res.IsError {
		t.Fatal(res.Text())
	}
	var r Report
	_ = json.Unmarshal(res.StructuredContent, &r)
	if r.Elapsed != "30min" || !strings.Contains(r.Note, "Sugestão") {
		t.Errorf("report = %+v", r)
	}
	if res := mcptest.CallTool(t, s, "session_time", map[string]any{"since": "amanhã"}); !res.IsError {
		t.Error("since inválido deveria falhar")
	}
}
