// Package ponto reads the punch-clock records (marcações de ponto) that an
// integration writes to a local directory — by default ~/.devpulse/ponto,
// one AAAA-MM-DD.json per day (see integrations/senior-ponto) — and turns
// them into worked intervals. It never talks to the punch-clock system: the
// integration does, in the browser where the user is already signed in.
package ponto

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	dateLayout   = "2006-01-02"
	maxDays      = 31
	maxFileBytes = 64 << 10
	// staleAfter is how old today's file may be before the report warns
	// that later punches may be missing.
	staleAfter = time.Hour
)

// Day status.
const (
	Complete     = "complete"      // every interval closed (2 punches may be just the lunch break)
	Open         = "open"          // today, odd number of punches: counted until now
	MissingPunch = "missing_punch" // past day, odd number of punches
	NoPunches    = "no_punches"    // synchronized, but no punch that day
	NotSynced    = "not_synced"    // no file for that day
	Invalid      = "invalid"       // unreadable file
)

var (
	punchRe   = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)(:[0-5]\d)?$`)
	weekdayPT = [...]string{"domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"}
)

// file is what the integration writes.
type file struct {
	Date     string   `json:"date"`
	TimeZone *string  `json:"timeZone"`
	Punches  []string `json:"punches"`
	Source   string   `json:"source"`
	SyncedAt string   `json:"syncedAt"`
}

// Interval is a worked period between two punches.
type Interval struct {
	Start   string `json:"start"`
	End     string `json:"end,omitempty"`
	Minutes int    `json:"minutes"`
	Open    bool   `json:"open,omitempty"`
}

// Day is one calendar day of the report.
type Day struct {
	Date        string     `json:"date"`
	Weekday     string     `json:"weekday"`
	Status      string     `json:"status"`
	Punches     []string   `json:"punches"`
	Intervals   []Interval `json:"intervals"`
	Worked      string     `json:"worked"`
	WorkedHours float64    `json:"workedHours"`
	Source      string     `json:"source,omitempty"`
	SyncedAt    string     `json:"syncedAt,omitempty"`
	Note        string     `json:"note,omitempty"`
}

// Report covers a range of days.
type Report struct {
	StartDate   string  `json:"startDate"`
	EndDate     string  `json:"endDate"`
	Dir         string  `json:"dir"`
	Days        []Day   `json:"days"`
	TotalWorked string  `json:"totalWorked"`
	TotalHours  float64 `json:"totalHours"`
}

// Reader reads the punch files of one directory.
type Reader struct {
	dir string
	now func() time.Time
}

// New reads from dir ("" means unavailable); now is the clock (nil = time.Now).
func New(dir string, now func() time.Time) *Reader {
	if now == nil {
		now = time.Now
	}
	return &Reader{dir: dir, now: now}
}

// ParseRange reads startDate/endDate (AAAA-MM-DD, local calendar dates).
// Without startDate the range is a single day (endDate, or today); without
// endDate it ends at startDate. It may not end in the future or exceed
// maxDays.
func (r *Reader) ParseRange(start, end string) (time.Time, time.Time, error) {
	n := r.now()
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, n.Location())
	parse := func(field, v string) (time.Time, error) {
		t, err := time.ParseInLocation(dateLayout, strings.TrimSpace(v), n.Location())
		if err != nil {
			return t, fmt.Errorf("%s deve estar no formato AAAA-MM-DD (recebido %q)", field, v)
		}
		return t, nil
	}
	from, to := today, today
	var err error
	if strings.TrimSpace(start) != "" {
		if from, err = parse("startDate", start); err != nil {
			return from, to, err
		}
		to = from
	}
	if strings.TrimSpace(end) != "" {
		if to, err = parse("endDate", end); err != nil {
			return from, to, err
		}
		if strings.TrimSpace(start) == "" {
			from = to
		}
	}
	switch {
	case to.Before(from):
		return from, to, errors.New("endDate é anterior a startDate")
	case to.After(today):
		return from, to, errors.New("endDate está no futuro")
	case to.Sub(from) > time.Duration(maxDays-1)*24*time.Hour+time.Hour: // +1h: DST transitions
		return from, to, fmt.Errorf("intervalo máximo é de %d dias", maxDays)
	}
	return from, to, nil
}

// Report reads every day from..to (inclusive).
func (r *Reader) Report(from, to time.Time) (*Report, error) {
	if r.dir == "" {
		return nil, errors.New("não foi possível descobrir a pasta do ponto: defina DEVPULSE_PONTO_DIR")
	}
	if fi, err := os.Stat(r.dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("nenhuma batida sincronizada: a pasta %s não existe. Instale a integração de ponto "+
			"(integrations/senior-ponto, no repositório do devpulse) ou defina DEVPULSE_PONTO_DIR", r.dir)
	}
	rep := &Report{StartDate: from.Format(dateLayout), EndDate: to.Format(dateLayout), Dir: r.dir, Days: []Day{}}
	total := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		day := r.day(d)
		for _, iv := range day.Intervals {
			total += iv.Minutes
		}
		rep.Days = append(rep.Days, day)
	}
	rep.TotalWorked, rep.TotalHours = human(total), hours(total)
	return rep, nil
}

func (r *Reader) day(d time.Time) Day {
	date := d.Format(dateLayout)
	day := Day{Date: date, Weekday: weekdayPT[d.Weekday()], Punches: []string{}, Intervals: []Interval{}}
	finish := func(status, note string) Day {
		day.Status, day.Note = status, note
		m := 0
		for _, iv := range day.Intervals {
			m += iv.Minutes
		}
		day.Worked, day.WorkedHours = human(m), hours(m)
		return day
	}

	f, err := readFile(filepath.Join(r.dir, date+".json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return finish(NotSynced, "sem arquivo para este dia: a integração ainda não sincronizou este período")
	case err != nil:
		return finish(Invalid, err.Error())
	case f.Date != date:
		return finish(Invalid, fmt.Sprintf("o arquivo diz ser de %q", f.Date))
	}
	day.Source, day.SyncedAt = f.Source, f.SyncedAt

	mins := make([]int, 0, len(f.Punches))
	for _, p := range f.Punches {
		m := punchRe.FindStringSubmatch(strings.TrimSpace(p))
		if m == nil {
			return finish(Invalid, fmt.Sprintf("horário inválido: %q", p))
		}
		mins = append(mins, atoi(m[1])*60+atoi(m[2]))
	}
	sort.Ints(mins)
	for _, m := range mins {
		day.Punches = append(day.Punches, clock(m))
	}
	for i := 0; i+1 < len(mins); i += 2 {
		day.Intervals = append(day.Intervals, Interval{Start: clock(mins[i]), End: clock(mins[i+1]), Minutes: mins[i+1] - mins[i]})
	}

	now := r.now()
	isToday := date == now.Format(dateLayout)
	var notes []string
	status := Complete
	switch {
	case len(mins) == 0:
		status = NoPunches
	case len(mins)%2 == 1 && isToday:
		last := mins[len(mins)-1]
		cur := now.Hour()*60 + now.Minute()
		day.Intervals = append(day.Intervals, Interval{Start: clock(last), End: clock(cur), Minutes: max(cur-last, 0), Open: true})
		status = Open
		notes = append(notes, fmt.Sprintf("intervalo em aberto desde %s, contado até agora (%s)", clock(last), clock(cur)))
	case len(mins)%2 == 1:
		day.Intervals = append(day.Intervals, Interval{Start: clock(mins[len(mins)-1]), Open: true})
		status = MissingPunch
		notes = append(notes, "número ímpar de batidas: falta uma (o último intervalo não foi contado)")
	}
	if isToday {
		if t, err := time.Parse(time.RFC3339, f.SyncedAt); err == nil && now.Sub(t) > staleAfter {
			notes = append(notes, fmt.Sprintf("última sincronização às %s: batidas posteriores ainda não aparecem "+
				"(clique no ícone da extensão para sincronizar)", t.In(now.Location()).Format("15:04")))
		}
	}
	return finish(status, strings.Join(notes, "; "))
}

func readFile(path string) (*file, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
		return nil, errors.New("arquivo inválido ou grande demais")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = []byte(strings.TrimPrefix(string(b), "\uFEFF"))
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("JSON inválido: %v", err)
	}
	return &f, nil
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func clock(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

func hours(m int) float64 { return math.Round(float64(m)/60*100) / 100 }

func human(m int) string { return fmt.Sprintf("%dh%02d", m/60, m%60) }
