package ponto

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

var brt = time.FixedZone("BRT", -3*60*60)

func at(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, brt) }

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func day(t *testing.T, r *Report, date string) Day {
	t.Helper()
	for _, d := range r.Days {
		if d.Date == date {
			return d
		}
	}
	t.Fatalf("dia %s ausente: %+v", date, r.Days)
	return Day{}
}

func TestReport(t *testing.T) {
	dir := t.TempDir()
	// out of order, with seconds, written with a BOM (PowerShell 5.1 style)
	write(t, dir, "2026-09-29.json", "\uFEFF"+`{"date":"2026-09-29","timeZone":"-03:00","punches":["13:30:41","08:00:59","14:30:47","17:00:55","17:30:36","18:00:20"],"source":"senior-x","syncedAt":"2026-10-02T21:00:00-03:00"}`)
	write(t, dir, "2026-09-30.json", `{"date":"2026-09-30","punches":["08:30:05","12:30:07","13:30:00"],"source":"senior-x"}`)
	write(t, dir, "2026-10-01.json", `{"date":"2026-10-01","punches":[],"source":"senior-x"}`)
	write(t, dir, "2026-10-02.json", `{"date":"2026-10-02","punches":["08:00:46","12:00:55","13:00:18"],"source":"senior-x","syncedAt":"2026-10-02T15:00:00-03:00"}`)
	write(t, dir, "2026-09-27.json", `{"date":"2026-09-26","punches":[]}`)
	write(t, dir, "2026-09-26.json", `{"date":"2026-09-26","punches":["8h"]}`)

	r := New(dir, func() time.Time { return at(2, 20, 30) })
	from, to, err := r.ParseRange("2026-09-25", "2026-10-02")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := r.Report(from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Days) != 8 {
		t.Fatalf("dias = %d", len(rep.Days))
	}

	d := day(t, rep, "2026-09-29")
	if d.Status != Complete || d.Weekday != "terça" || len(d.Intervals) != 3 || d.Worked != "8h30" || d.WorkedHours != 8.5 {
		t.Errorf("29/09 = %+v", d)
	}
	if strings.Join(d.Punches, " ") != "08:00 13:30 14:30 17:00 17:30 18:00" || d.Note != "" {
		t.Errorf("batidas = %v / %q", d.Punches, d.Note)
	}

	d = day(t, rep, "2026-09-30")
	if d.Status != MissingPunch || d.Worked != "4h00" || !d.Intervals[1].Open || d.Intervals[1].End != "" || !strings.Contains(d.Note, "ímpar") {
		t.Errorf("30/09 = %+v", d)
	}

	if d = day(t, rep, "2026-10-01"); d.Status != NoPunches || d.Worked != "0h00" {
		t.Errorf("01/10 = %+v", d)
	}

	// today, 3 punches at 20:30: the last interval is open and counted until now
	d = day(t, rep, "2026-10-02")
	if d.Status != Open || d.Worked != "11h30" {
		t.Errorf("hoje = %+v", d)
	}
	last := d.Intervals[len(d.Intervals)-1]
	if !last.Open || last.Start != "13:00" || last.End != "20:30" || last.Minutes != 450 {
		t.Errorf("intervalo aberto = %+v", last)
	}
	if !strings.Contains(d.Note, "em aberto desde 13:00") || !strings.Contains(d.Note, "sincronização às 15:00") {
		t.Errorf("nota de hoje = %q", d.Note)
	}

	if d = day(t, rep, "2026-09-25"); d.Status != NotSynced {
		t.Errorf("25/09 = %+v", d)
	}
	if d = day(t, rep, "2026-09-26"); d.Status != Invalid || !strings.Contains(d.Note, "8h") {
		t.Errorf("26/09 = %+v", d)
	}
	if d = day(t, rep, "2026-09-27"); d.Status != Invalid || !strings.Contains(d.Note, "2026-09-26") {
		t.Errorf("27/09 (data trocada) = %+v", d)
	}

	// 8h30 + 4h00 + 11h30
	if rep.TotalWorked != "24h00" || rep.TotalHours != 24 {
		t.Errorf("total = %s / %v", rep.TotalWorked, rep.TotalHours)
	}
}

func TestParseRange(t *testing.T) {
	r := New(t.TempDir(), func() time.Time { return at(2, 9, 0) })
	ok := func(start, end, wantFrom, wantTo string) {
		t.Helper()
		from, to, err := r.ParseRange(start, end)
		if err != nil || from.Format(dateLayout) != wantFrom || to.Format(dateLayout) != wantTo {
			t.Errorf("(%q,%q) = %s..%s %v", start, end, from.Format(dateLayout), to.Format(dateLayout), err)
		}
	}
	ok("", "", "2026-10-02", "2026-10-02")
	ok("2026-09-28", "", "2026-09-28", "2026-09-28")
	ok("", "2026-10-01", "2026-10-01", "2026-10-01")
	ok("2026-09-02", "2026-10-02", "2026-09-02", "2026-10-02") // 31 days
	for _, bad := range [][2]string{
		{"2026-10-03", ""},           // future
		{"2026-10-01", "2026-09-30"}, // reversed
		{"2026-09-01", "2026-10-02"}, // 32 days
		{"02/10/2026", ""},
	} {
		if _, _, err := r.ParseRange(bad[0], bad[1]); err == nil {
			t.Errorf("%v deveria falhar", bad)
		}
	}
}

func TestMissingDirAndTool(t *testing.T) {
	r := New(filepath.Join(t.TempDir(), "nada"), nil)
	s := mcp.NewServer("t", "0", "")
	r.Register(s)
	tr := mcptest.CallTool(t, s, "get_punches", map[string]any{})
	if !tr.IsError || !strings.Contains(tr.Text(), "integrations/senior-ponto") {
		t.Errorf("sem pasta = %s", tr.Text())
	}
	if tr := mcptest.CallTool(t, s, "get_punches", map[string]any{"date": "2026-10-02"}); !tr.IsError {
		t.Error("argumento desconhecido deveria falhar")
	}

	dir := t.TempDir()
	write(t, dir, "2026-10-01.json", `{"date":"2026-10-01","punches":["08:00:52","12:00:12","13:00:57","17:00:24"]}`)
	s = mcp.NewServer("t", "0", "")
	New(dir, func() time.Time { return at(2, 9, 0) }).Register(s)
	tr = mcptest.CallTool(t, s, "get_punches", map[string]any{"startDate": "2026-10-01"})
	if tr.IsError {
		t.Fatal(tr.Text())
	}
	var rep Report
	if err := json.Unmarshal(tr.StructuredContent, &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Days) != 1 || rep.Days[0].Status != Complete || rep.TotalWorked != "8h00" {
		t.Errorf("tool = %+v", rep)
	}
}
