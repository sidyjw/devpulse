package azuredevops

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sidyjw/devpulse/internal/httpx"
)

// 7pace Timetracker REST API v3.
// Docs: https://github.com/7pace/timetracker-rest-api-samplecode
const sevenPaceAPIVersion = "3.2"

var guidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type SevenPace struct {
	c *httpx.Client

	mu        sync.Mutex
	actCache  []ActivityType
	actExpiry time.Time
}

func NewSevenPace(apiRoot, token string, hc *http.Client) *SevenPace {
	return &SevenPace{c: &httpx.Client{
		HTTP:    hc,
		Root:    strings.TrimRight(apiRoot, "/") + "/rest",
		Auth:    "Bearer " + token,
		Secrets: []string{token},
		Name:    "7pace",
	}}
}

// ---------- API shapes ----------

type envelope[T any] struct {
	Data T `json:"data"`
}

type ActivityType struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type spUser struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	UniqueName string `json:"uniqueName"`
}

type apiWorklog struct {
	ID             string        `json:"id"`
	Timestamp      string        `json:"timestamp"`
	Length         int64         `json:"length"` // seconds
	BillableLength *int64        `json:"billableLength"`
	WorkItemID     *int          `json:"workItemId"`
	Comment        string        `json:"comment"`
	ActivityType   *ActivityType `json:"activityType"`
	User           *spUser       `json:"user"`
}

// Worklog is what the tools return — hours already converted.
type Worklog struct {
	ID            string   `json:"id"`
	Date          string   `json:"date"`
	Time          string   `json:"time,omitempty"`
	Hours         float64  `json:"hours"`
	Duration      string   `json:"duration"`
	BillableHours *float64 `json:"billableHours,omitempty"`
	WorkItemID    *int     `json:"workItemId,omitempty"`
	Comment       string   `json:"comment,omitempty"`
	ActivityType  string   `json:"activityType,omitempty"`
	User          string   `json:"user,omitempty"`
}

type Me struct {
	User                spUser        `json:"user"`
	DefaultActivityType *ActivityType `json:"defaultActivityType"`
	TimeZone            any           `json:"timeZone"`
}

// ---------- calls ----------

func (s *SevenPace) path(p string, q ...string) string {
	var b strings.Builder
	b.WriteString(p)
	b.WriteString("?api-version=")
	b.WriteString(sevenPaceAPIVersion)
	for i := 0; i+1 < len(q); i += 2 {
		// keys like "$fromTimestamp" are kept literal; values are escaped.
		b.WriteByte('&')
		b.WriteString(q[i])
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(q[i+1]))
	}
	return b.String()
}

func (s *SevenPace) Me(ctx context.Context) (*Me, error) {
	var env envelope[Me]
	if err := s.c.Do(ctx, http.MethodGet, s.path("/me"), nil, &env); err != nil {
		return nil, err
	}
	return &env.Data, nil
}

func (s *SevenPace) ActivityTypes(ctx context.Context) ([]ActivityType, error) {
	s.mu.Lock()
	if s.actCache != nil && time.Now().Before(s.actExpiry) {
		out := s.actCache
		s.mu.Unlock()
		return out, nil
	}
	s.mu.Unlock()

	var env envelope[json.RawMessage]
	if err := s.c.Do(ctx, http.MethodGet, s.path("/activityTypes"), nil, &env); err != nil {
		return nil, err
	}
	// The API returns {"data":{"enabled":..,"activityTypes":[..]}}; accept a
	// bare array too, just in case.
	var list []ActivityType
	var obj struct {
		ActivityTypes []ActivityType `json:"activityTypes"`
	}
	if err := json.Unmarshal(env.Data, &obj); err == nil && obj.ActivityTypes != nil {
		list = obj.ActivityTypes
	} else if err := json.Unmarshal(env.Data, &list); err != nil {
		return nil, fmt.Errorf("formato inesperado de /activityTypes")
	}

	s.mu.Lock()
	s.actCache = list
	s.actExpiry = time.Now().Add(5 * time.Minute)
	s.mu.Unlock()
	return list, nil
}

// ResolveActivityType accepts a GUID or a (case-insensitive) name. Unlike
// the original project it never silently drops an unknown value.
func (s *SevenPace) ResolveActivityType(ctx context.Context, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	list, err := s.ActivityTypes(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range list {
		if strings.EqualFold(a.ID, v) || strings.EqualFold(strings.TrimSpace(a.Name), v) {
			return a.ID, nil
		}
	}
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, a.Name)
	}
	return "", fmt.Errorf("tipo de atividade %q não existe; opções: %s", v, strings.Join(names, ", "))
}

type WorklogQuery struct {
	From, To    time.Time // inclusive calendar dates
	WorkItemIDs []int
}

const (
	pageSize = 500 // API maximum
	maxPages = 20
)

func (s *SevenPace) Worklogs(ctx context.Context, q WorklogQuery) ([]Worklog, error) {
	// $fromTimestamp / $toTimestamp are exclusive ("greater/less than").
	from := q.From.Add(-time.Second).Format(tsLayout)
	to := q.To.AddDate(0, 0, 1).Format(tsLayout)

	var ids string
	if len(q.WorkItemIDs) > 0 {
		parts := make([]string, len(q.WorkItemIDs))
		for i, id := range q.WorkItemIDs {
			parts[i] = strconv.Itoa(id)
		}
		ids = strings.Join(parts, ",")
	}

	var out []Worklog
	for page := 0; page < maxPages; page++ {
		args := []string{
			"$fromTimestamp", from,
			"$toTimestamp", to,
			"$count", strconv.Itoa(pageSize),
			"$skip", strconv.Itoa(page * pageSize),
		}
		if ids != "" {
			args = append(args, "$workItemIds", ids)
		}
		var env envelope[[]apiWorklog]
		if err := s.c.Do(ctx, http.MethodGet, s.path("/workLogs", args...), nil, &env); err != nil {
			return nil, err
		}
		for _, w := range env.Data {
			wl := toWorklog(w)
			// Defensive client-side filter in case the server ignores a param.
			if d, err := time.Parse(dateLayout, wl.Date); err == nil {
				if d.Before(q.From) || d.After(q.To) {
					continue
				}
			}
			out = append(out, wl)
		}
		if len(env.Data) < pageSize {
			sort.SliceStable(out, func(i, j int) bool {
				return out[i].Date+out[i].Time < out[j].Date+out[j].Time
			})
			return out, nil
		}
	}
	return nil, fmt.Errorf("mais de %d lançamentos no período; reduza o intervalo", maxPages*pageSize)
}

type NewWorklog struct {
	Timestamp      string `json:"timestamp"`
	Length         int64  `json:"length"`
	BillableLength *int64 `json:"billableLength,omitempty"`
	WorkItemID     int    `json:"workItemId"`
	Comment        string `json:"comment"`
	ActivityTypeID string `json:"activityTypeId,omitempty"`
}

func (s *SevenPace) CreateWorklog(ctx context.Context, w NewWorklog) (*Worklog, error) {
	var env envelope[apiWorklog]
	if err := s.c.Do(ctx, http.MethodPost, s.path("/workLogs"), w, &env); err != nil {
		return nil, err
	}
	if env.Data.ID == "" {
		return nil, fmt.Errorf("7pace não retornou o id do lançamento criado; verifique antes de tentar de novo")
	}
	wl := toWorklog(env.Data)
	return &wl, nil
}

// WorklogPatch carries only the fields to change (PATCH semantics).
type WorklogPatch struct {
	Timestamp      *string `json:"timestamp,omitempty"`
	Length         *int64  `json:"length,omitempty"`
	BillableLength *int64  `json:"billableLength,omitempty"`
	WorkItemID     *int    `json:"workItemId,omitempty"`
	Comment        *string `json:"comment,omitempty"`
	ActivityTypeID *string `json:"activityTypeId,omitempty"`
}

func (s *SevenPace) UpdateWorklog(ctx context.Context, id string, p WorklogPatch) (*Worklog, error) {
	if !guidRe.MatchString(id) {
		return nil, fmt.Errorf("worklogId inválido: %q", id)
	}
	var env envelope[apiWorklog]
	if err := s.c.Do(ctx, http.MethodPatch, s.path("/workLogs/"+id), p, &env); err != nil {
		return nil, err
	}
	if env.Data.ID == "" {
		return nil, nil
	}
	wl := toWorklog(env.Data)
	return &wl, nil
}

func (s *SevenPace) DeleteWorklog(ctx context.Context, id string) error {
	if !guidRe.MatchString(id) {
		return fmt.Errorf("worklogId inválido: %q", id)
	}
	return s.c.Do(ctx, http.MethodDelete, s.path("/workLogs/"+id), nil, nil)
}

// ---------- helpers ----------

const (
	dateLayout = "2006-01-02"
	tsLayout   = "2006-01-02T15:04:05"
)

var tsLayouts = []string{
	"2006-01-02T15:04:05.9999999",
	"2006-01-02T15:04:05",
	time.RFC3339Nano,
	"2006-01-02T15:04:05.9999999Z07:00",
}

func toWorklog(w apiWorklog) Worklog {
	out := Worklog{
		ID:         w.ID,
		Hours:      secondsToHours(w.Length),
		Duration:   fmtDuration(w.Length),
		WorkItemID: w.WorkItemID,
		Comment:    w.Comment,
	}
	for _, l := range tsLayouts {
		if t, err := time.Parse(l, w.Timestamp); err == nil {
			out.Date = t.Format(dateLayout)
			out.Time = t.Format("15:04")
			break
		}
	}
	if out.Date == "" && len(w.Timestamp) >= 10 {
		out.Date = w.Timestamp[:10]
	}
	if w.BillableLength != nil {
		h := secondsToHours(*w.BillableLength)
		out.BillableHours = &h
	}
	if w.ActivityType != nil && w.ActivityType.Name != "" && w.ActivityType.Name != "[Not Set]" {
		out.ActivityType = w.ActivityType.Name
	}
	if w.User != nil {
		out.User = w.User.Name
	}
	return out
}

// hoursToSeconds rounds to whole minutes, which is what 7pace displays.
func hoursToSeconds(h float64) int64 {
	return int64(math.Round(h*60)) * 60
}

func secondsToHours(s int64) float64 {
	return math.Round(float64(s)/3600*100) / 100
}

func fmtDuration(sec int64) string {
	m := (sec + 30) / 60
	return fmt.Sprintf("%dh%02d", m/60, m%60)
}
