package azuredevops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sidiney/pm-mcp/internal/mcp"
)

// ---------- date helpers (calendar dates in the user's local time) ----------

var hhmmRe = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

func today() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

func parseDate(field, v string) (time.Time, error) {
	t, err := time.Parse(dateLayout, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s deve estar no formato AAAA-MM-DD (recebido %q)", field, v)
	}
	return t, nil
}

func dateRange(start, end string, defStart, defEnd time.Time, maxDays int) (time.Time, time.Time, error) {
	from, to := defStart, defEnd
	var err error
	if start != "" {
		if from, err = parseDate("startDate", start); err != nil {
			return from, to, err
		}
	}
	if end != "" {
		if to, err = parseDate("endDate", end); err != nil {
			return from, to, err
		}
	}
	if to.Before(from) {
		return from, to, errors.New("endDate é anterior a startDate")
	}
	if to.Sub(from) > time.Duration(maxDays)*24*time.Hour {
		return from, to, fmt.Errorf("intervalo máximo é de %d dias", maxDays)
	}
	return from, to, nil
}

func weekStart(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return t.AddDate(0, 0, -(wd - 1))
}

var weekdayPT = [...]string{"domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"}

func validateEntryDate(d time.Time) error {
	t := today()
	if d.After(t.AddDate(0, 0, 31)) {
		return fmt.Errorf("data %s está mais de 31 dias no futuro", d.Format(dateLayout))
	}
	if d.Before(t.AddDate(-1, 0, 0)) {
		return fmt.Errorf("data %s está mais de 1 ano no passado", d.Format(dateLayout))
	}
	return nil
}

func validateHours(field string, h float64) error {
	if h <= 0 || h > 24 {
		return fmt.Errorf("%s deve ser maior que 0 e no máximo 24", field)
	}
	if hoursToSeconds(h) == 0 {
		return fmt.Errorf("%s menor que 1 minuto", field)
	}
	return nil
}

func normComment(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// ---------- 7pace tools ----------

type logEntryArgs struct {
	WorkItemID     int      `json:"workItemId"`
	Date           string   `json:"date"`
	Hours          float64  `json:"hours"`
	Comment        string   `json:"comment"`
	ActivityType   string   `json:"activityType,omitempty"`
	StartTime      string   `json:"startTime,omitempty"`
	BillableHours  *float64 `json:"billableHours,omitempty"`
	AllowDuplicate bool     `json:"allowDuplicate,omitempty"`
}

var logEntryProps = map[string]any{
	"workItemId":     mcp.IntP("ID do work item no Azure DevOps (ex.: 1234)"),
	"date":           mcp.StrP("Data do trabalho, AAAA-MM-DD"),
	"hours":          mcp.NumP("Horas trabalhadas (ex.: 1.5 = 1h30). Arredondado ao minuto."),
	"comment":        mcp.StrP("Descrição do que foi feito"),
	"activityType":   mcp.StrP("Opcional: nome ou ID do tipo de atividade (veja list_activity_types)"),
	"startTime":      mcp.StrP("Opcional: hora de início HH:MM (padrão 09:00)"),
	"billableHours":  mcp.NumP("Opcional: horas faturáveis, se a organização usa"),
	"allowDuplicate": mcp.BoolP("Opcional: true para lançar mesmo se já existir lançamento idêntico no dia"),
}

type validEntry struct {
	args logEntryArgs
	date time.Time
	nw   NewWorklog
}

func (e logEntryArgs) validate() (*validEntry, error) {
	if e.WorkItemID <= 0 {
		return nil, errors.New("workItemId deve ser um inteiro positivo")
	}
	d, err := parseDate("date", e.Date)
	if err != nil {
		return nil, err
	}
	if err := validateEntryDate(d); err != nil {
		return nil, err
	}
	if err := validateHours("hours", e.Hours); err != nil {
		return nil, err
	}
	c := strings.TrimSpace(e.Comment)
	if c == "" {
		return nil, errors.New("comment é obrigatório")
	}
	if len(c) > 2000 {
		return nil, errors.New("comment com mais de 2000 caracteres")
	}
	st := strings.TrimSpace(e.StartTime)
	if st == "" {
		st = "09:00"
	}
	if !hhmmRe.MatchString(st) {
		return nil, fmt.Errorf("startTime deve ser HH:MM (recebido %q)", e.StartTime)
	}
	nw := NewWorklog{
		Timestamp:  d.Format(dateLayout) + "T" + st + ":00",
		Length:     hoursToSeconds(e.Hours),
		WorkItemID: e.WorkItemID,
		Comment:    c,
	}
	if e.BillableHours != nil {
		if *e.BillableHours < 0 || *e.BillableHours > 24 {
			return nil, errors.New("billableHours deve estar entre 0 e 24")
		}
		b := hoursToSeconds(*e.BillableHours)
		nw.BillableLength = &b
	}
	return &validEntry{args: e, date: d, nw: nw}, nil
}

func isDuplicate(existing []Worklog, nw NewWorklog, date string) *Worklog {
	for i := range existing {
		w := &existing[i]
		if w.Date == date && w.WorkItemID != nil && *w.WorkItemID == nw.WorkItemID &&
			hoursToSeconds(w.Hours) == nw.Length && normComment(w.Comment) == normComment(nw.Comment) {
			return w
		}
	}
	return nil
}

func dayTotal(ws []Worklog, date string) float64 {
	var s int64
	for _, w := range ws {
		if w.Date == date {
			s += hoursToSeconds(w.Hours)
		}
	}
	return secondsToHours(s)
}

func registerSevenPaceTools(s *mcp.Server, sp *SevenPace, az *AzDO, cfg *sevenPaceConfig) {
	s.AddTool(&mcp.Tool{
		Name:        "sevenpace_whoami",
		Title:       "7pace: quem sou eu",
		Description: "Mostra o usuário do 7pace associado ao token e o tipo de atividade padrão. Use para testar a conexão.",
		InputSchema: mcp.Obj(map[string]any{}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		return sp.Me(ctx)
	})

	s.AddTool(&mcp.Tool{
		Name:        "list_activity_types",
		Title:       "7pace: tipos de atividade",
		Description: "Lista os tipos de atividade (Development, Testing, …) configurados no 7pace, com nome e ID.",
		InputSchema: mcp.Obj(map[string]any{}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		list, err := sp.ActivityTypes(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"activityTypes": list}, nil
	})

	s.AddTool(&mcp.Tool{
		Name:  "get_worklogs",
		Title: "7pace: listar lançamentos",
		Description: "Lista os SEUS lançamentos de horas no 7pace num intervalo de datas (padrão: últimos 7 dias, máx. 92). " +
			"Cada item traz id (necessário para update/delete), data, hora, horas, work item, atividade e comentário.",
		InputSchema: mcp.Obj(map[string]any{
			"startDate":   mcp.StrP("AAAA-MM-DD (padrão: hoje - 6 dias)"),
			"endDate":     mcp.StrP("AAAA-MM-DD (padrão: hoje)"),
			"workItemIds": mcp.ArrP("integer", "Opcional: filtrar por work items"),
		}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			StartDate   string `json:"startDate"`
			EndDate     string `json:"endDate"`
			WorkItemIDs []int  `json:"workItemIds"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		t := today()
		from, to, err := dateRange(a.StartDate, a.EndDate, t.AddDate(0, 0, -6), t, 92)
		if err != nil {
			return nil, err
		}
		if len(a.WorkItemIDs) > 100 {
			return nil, errors.New("no máximo 100 workItemIds")
		}
		ws, err := sp.Worklogs(ctx, WorklogQuery{From: from, To: to, WorkItemIDs: a.WorkItemIDs})
		if err != nil {
			return nil, err
		}
		var total int64
		for _, w := range ws {
			total += hoursToSeconds(w.Hours)
		}
		return map[string]any{
			"startDate":  from.Format(dateLayout),
			"endDate":    to.Format(dateLayout),
			"count":      len(ws),
			"totalHours": secondsToHours(total),
			"worklogs":   ws,
		}, nil
	})

	s.AddTool(&mcp.Tool{
		Name:  "time_summary",
		Title: "7pace: resumo e lacunas",
		Description: "Resumo das suas horas por dia e por work item num período (padrão: semana atual até hoje). " +
			"Aponta dias úteis com horas faltando em relação à meta diária. Use antes de lançar para saber o que falta.",
		InputSchema: mcp.Obj(map[string]any{
			"startDate":           mcp.StrP("AAAA-MM-DD (padrão: segunda-feira desta semana)"),
			"endDate":             mcp.StrP("AAAA-MM-DD (padrão: hoje)"),
			"expectedHoursPerDay": mcp.NumP("Meta de horas por dia útil (padrão 8; 0 desativa)"),
		}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			StartDate string   `json:"startDate"`
			EndDate   string   `json:"endDate"`
			Expected  *float64 `json:"expectedHoursPerDay"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		t := today()
		from, to, err := dateRange(a.StartDate, a.EndDate, weekStart(t), t, 92)
		if err != nil {
			return nil, err
		}
		expected := 8.0
		if a.Expected != nil {
			if *a.Expected < 0 || *a.Expected > 24 {
				return nil, errors.New("expectedHoursPerDay deve estar entre 0 e 24")
			}
			expected = *a.Expected
		}
		ws, err := sp.Worklogs(ctx, WorklogQuery{From: from, To: to})
		if err != nil {
			return nil, err
		}
		return buildSummary(ctx, ws, from, to, expected, az), nil
	})

	if cfg.ReadOnly {
		return
	}

	s.AddTool(&mcp.Tool{
		Name:  "log_time",
		Title: "7pace: lançar horas",
		Description: "Lança um período de trabalho no 7pace para um work item. " +
			"Recusa lançamentos idênticos (mesmo dia, work item, duração e comentário) a menos que allowDuplicate=true. " +
			"Confirme com o usuário antes de lançar. Retorna o id criado e o total do dia.",
		InputSchema: mcp.Obj(logEntryProps, "workItemId", "date", "hours", "comment"),
		Annotations: mcp.WriteTool,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a logEntryArgs
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		v, err := a.validate()
		if err != nil {
			return nil, err
		}
		if v.nw.ActivityTypeID, err = sp.ResolveActivityType(ctx, a.ActivityType); err != nil {
			return nil, err
		}
		day := v.date.Format(dateLayout)
		existing, err := sp.Worklogs(ctx, WorklogQuery{From: v.date, To: v.date})
		if err != nil {
			return nil, fmt.Errorf("não foi possível checar duplicidade: %w", err)
		}
		if d := isDuplicate(existing, v.nw, day); d != nil && !a.AllowDuplicate {
			return nil, fmt.Errorf("já existe lançamento idêntico em %s (id %s, %s no #%d). Nada foi lançado. Use allowDuplicate=true se for intencional",
				day, d.ID, d.Duration, *d.WorkItemID)
		}
		created, err := sp.CreateWorklog(ctx, v.nw)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"message":       fmt.Sprintf("lançado %s no #%d em %s", fmtDuration(v.nw.Length), a.WorkItemID, day),
			"worklog":       created,
			"dayTotalHours": secondsToHours(hoursToSeconds(dayTotal(existing, day)) + v.nw.Length),
		}, nil
	})

	s.AddTool(&mcp.Tool{
		Name:  "log_time_batch",
		Title: "7pace: lançar várias horas",
		Description: "Lança vários períodos de uma vez (até 60), ex.: a semana inteira. Valida TUDO antes de enviar o primeiro; " +
			"depois envia em ordem e para no primeiro erro, informando o que foi e o que não foi lançado. " +
			"Mostre a lista ao usuário e confirme antes de chamar.",
		InputSchema: mcp.Obj(map[string]any{
			"entries": map[string]any{
				"type":        "array",
				"description": "Lançamentos",
				"minItems":    1,
				"maxItems":    60,
				"items":       mcp.Obj(logEntryProps, "workItemId", "date", "hours", "comment"),
			},
		}, "entries"),
		Annotations: mcp.WriteTool,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Entries []logEntryArgs `json:"entries"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		if len(a.Entries) == 0 || len(a.Entries) > 60 {
			return nil, errors.New("envie entre 1 e 60 lançamentos")
		}
		var vs []*validEntry
		minD, maxD := time.Time{}, time.Time{}
		for i, e := range a.Entries {
			v, err := e.validate()
			if err != nil {
				return nil, fmt.Errorf("lançamento %d: %w (nada foi lançado)", i+1, err)
			}
			if v.nw.ActivityTypeID, err = sp.ResolveActivityType(ctx, e.ActivityType); err != nil {
				return nil, fmt.Errorf("lançamento %d: %w (nada foi lançado)", i+1, err)
			}
			if minD.IsZero() || v.date.Before(minD) {
				minD = v.date
			}
			if v.date.After(maxD) {
				maxD = v.date
			}
			vs = append(vs, v)
		}
		if maxD.Sub(minD) > 92*24*time.Hour {
			return nil, errors.New("os lançamentos de um lote devem caber em 92 dias")
		}
		existing, err := sp.Worklogs(ctx, WorklogQuery{From: minD, To: maxD})
		if err != nil {
			return nil, fmt.Errorf("não foi possível checar duplicidade: %w", err)
		}
		for i, v := range vs {
			day := v.date.Format(dateLayout)
			if d := isDuplicate(existing, v.nw, day); d != nil && !v.args.AllowDuplicate {
				return nil, fmt.Errorf("lançamento %d duplica o existente %s em %s (#%d, %s). Nada foi lançado",
					i+1, d.ID, day, v.args.WorkItemID, d.Duration)
			}
			for j := 0; j < i; j++ {
				o := vs[j]
				if o.date.Equal(v.date) && o.nw.WorkItemID == v.nw.WorkItemID && o.nw.Length == v.nw.Length &&
					normComment(o.nw.Comment) == normComment(v.nw.Comment) && !v.args.AllowDuplicate {
					return nil, fmt.Errorf("lançamentos %d e %d são idênticos. Nada foi lançado", j+1, i+1)
				}
			}
		}
		type item struct {
			Index int      `json:"index"`
			ID    string   `json:"id,omitempty"`
			Date  string   `json:"date"`
			Item  int      `json:"workItemId"`
			Dur   string   `json:"duration"`
			Error string   `json:"error,omitempty"`
			WL    *Worklog `json:"-"`
		}
		var created []item
		var failed *item
		for i, v := range vs {
			w, err := sp.CreateWorklog(ctx, v.nw)
			it := item{Index: i + 1, Date: v.date.Format(dateLayout), Item: v.nw.WorkItemID, Dur: fmtDuration(v.nw.Length)}
			if err != nil {
				it.Error = err.Error()
				failed = &it
				break
			}
			it.ID = w.ID
			created = append(created, it)
			existing = append(existing, Worklog{Date: it.Date, Hours: secondsToHours(v.nw.Length)})
		}
		totals := map[string]float64{}
		for _, v := range vs {
			d := v.date.Format(dateLayout)
			totals[d] = dayTotal(existing, d)
		}
		res := map[string]any{
			"created":       created,
			"createdCount":  len(created),
			"dayTotalHours": totals,
		}
		if failed != nil {
			res["failed"] = failed
			res["notAttempted"] = len(vs) - len(created) - 1
			res["message"] = fmt.Sprintf("parou no lançamento %d; %d lançados antes do erro", failed.Index, len(created))
		} else {
			res["message"] = fmt.Sprintf("%d lançamentos criados", len(created))
		}
		return res, nil
	})

	s.AddTool(&mcp.Tool{
		Name:  "update_worklog",
		Title: "7pace: corrigir lançamento",
		Description: "Altera um lançamento existente (use o id de get_worklogs). Informe só os campos que mudam. " +
			"Para mudar a hora de início informe date e startTime juntos.",
		InputSchema: mcp.Obj(map[string]any{
			"worklogId":     mcp.StrP("ID (GUID) do lançamento"),
			"workItemId":    mcp.IntP("Novo work item"),
			"date":          mcp.StrP("Nova data AAAA-MM-DD"),
			"startTime":     mcp.StrP("Nova hora de início HH:MM (exige date)"),
			"hours":         mcp.NumP("Nova duração em horas"),
			"billableHours": mcp.NumP("Novas horas faturáveis"),
			"comment":       mcp.StrP("Novo comentário"),
			"activityType":  mcp.StrP("Novo tipo de atividade (nome ou ID)"),
		}, "worklogId"),
		Annotations: mcp.WriteTool,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			WorklogID     string   `json:"worklogId"`
			WorkItemID    *int     `json:"workItemId"`
			Date          *string  `json:"date"`
			StartTime     *string  `json:"startTime"`
			Hours         *float64 `json:"hours"`
			BillableHours *float64 `json:"billableHours"`
			Comment       *string  `json:"comment"`
			ActivityType  *string  `json:"activityType"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		var p WorklogPatch
		changed := false
		if a.WorkItemID != nil {
			if *a.WorkItemID <= 0 {
				return nil, errors.New("workItemId deve ser positivo")
			}
			p.WorkItemID, changed = a.WorkItemID, true
		}
		if a.StartTime != nil && a.Date == nil {
			return nil, errors.New("para mudar startTime informe também date")
		}
		if a.Date != nil {
			d, err := parseDate("date", *a.Date)
			if err != nil {
				return nil, err
			}
			if err := validateEntryDate(d); err != nil {
				return nil, err
			}
			st := "09:00"
			if a.StartTime != nil {
				st = strings.TrimSpace(*a.StartTime)
			}
			if !hhmmRe.MatchString(st) {
				return nil, errors.New("startTime deve ser HH:MM")
			}
			ts := d.Format(dateLayout) + "T" + st + ":00"
			p.Timestamp, changed = &ts, true
		}
		if a.Hours != nil {
			if err := validateHours("hours", *a.Hours); err != nil {
				return nil, err
			}
			l := hoursToSeconds(*a.Hours)
			p.Length, changed = &l, true
		}
		if a.BillableHours != nil {
			if *a.BillableHours < 0 || *a.BillableHours > 24 {
				return nil, errors.New("billableHours deve estar entre 0 e 24")
			}
			b := hoursToSeconds(*a.BillableHours)
			p.BillableLength, changed = &b, true
		}
		if a.Comment != nil {
			c := strings.TrimSpace(*a.Comment)
			if c == "" || len(c) > 2000 {
				return nil, errors.New("comment deve ter entre 1 e 2000 caracteres")
			}
			p.Comment, changed = &c, true
		}
		if a.ActivityType != nil {
			id, err := sp.ResolveActivityType(ctx, *a.ActivityType)
			if err != nil {
				return nil, err
			}
			if id == "" {
				id = "00000000-0000-0000-0000-000000000000"
			}
			p.ActivityTypeID, changed = &id, true
		}
		if !changed {
			return nil, errors.New("informe ao menos um campo para alterar")
		}
		w, err := sp.UpdateWorklog(ctx, strings.TrimSpace(a.WorklogID), p)
		if err != nil {
			return nil, err
		}
		return map[string]any{"message": "lançamento atualizado", "worklog": w}, nil
	})

	if !cfg.EnableDelete {
		return
	}
	s.AddTool(&mcp.Tool{
		Name:        "delete_worklog",
		Title:       "7pace: excluir lançamento",
		Description: "Exclui DEFINITIVAMENTE um lançamento do 7pace. Sempre confirme com o usuário, mostrando o lançamento, antes de chamar.",
		InputSchema: mcp.Obj(map[string]any{"worklogId": mcp.StrP("ID (GUID) do lançamento")}, "worklogId"),
		Annotations: mcp.Destructive,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			WorklogID string `json:"worklogId"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		if err := sp.DeleteWorklog(ctx, strings.TrimSpace(a.WorklogID)); err != nil {
			return nil, err
		}
		return map[string]any{"message": "lançamento " + a.WorklogID + " excluído"}, nil
	})
}

// ---------- summary ----------

type daySummary struct {
	Date         string  `json:"date"`
	Weekday      string  `json:"weekday"`
	Hours        float64 `json:"hours"`
	Duration     string  `json:"duration"`
	Entries      int     `json:"entries"`
	MissingHours float64 `json:"missingHours,omitempty"`
}

type itemSummary struct {
	WorkItemID int     `json:"workItemId"`
	Title      string  `json:"title,omitempty"`
	Hours      float64 `json:"hours"`
	Entries    int     `json:"entries"`
}

func buildSummary(ctx context.Context, ws []Worklog, from, to time.Time, expected float64, az *AzDO) map[string]any {
	perDay := map[string]int64{}
	cntDay := map[string]int{}
	perItem := map[int]int64{}
	cntItem := map[int]int{}
	var total int64
	for _, w := range ws {
		sec := hoursToSeconds(w.Hours)
		perDay[w.Date] += sec
		cntDay[w.Date]++
		id := 0
		if w.WorkItemID != nil {
			id = *w.WorkItemID
		}
		perItem[id] += sec
		cntItem[id]++
		total += sec
	}
	var days []daySummary
	var gaps []string
	var missingSec int64
	expSec := hoursToSeconds(expected)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		k := d.Format(dateLayout)
		ds := daySummary{Date: k, Weekday: weekdayPT[d.Weekday()], Hours: secondsToHours(perDay[k]), Duration: fmtDuration(perDay[k]), Entries: cntDay[k]}
		weekend := d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
		if expected > 0 && !weekend && perDay[k] < expSec {
			ds.MissingHours = secondsToHours(expSec - perDay[k])
			missingSec += expSec - perDay[k]
			gaps = append(gaps, fmt.Sprintf("%s (%s): faltam %s", k, ds.Weekday, fmtDuration(expSec-perDay[k])))
		}
		if weekend && perDay[k] == 0 {
			continue
		}
		days = append(days, ds)
	}
	var items []itemSummary
	var ids []int
	for id, sec := range perItem {
		items = append(items, itemSummary{WorkItemID: id, Hours: secondsToHours(sec), Entries: cntItem[id]})
		if id > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Hours > items[j].Hours })
	if az != nil && len(ids) > 0 {
		if wis, err := az.WorkItemsByID(ctx, ids); err == nil {
			titles := map[int]string{}
			for _, wi := range wis {
				titles[wi.ID] = wi.Type + ": " + wi.Title
			}
			for i := range items {
				items[i].Title = titles[items[i].WorkItemID]
			}
		}
	}
	return map[string]any{
		"startDate":           from.Format(dateLayout),
		"endDate":             to.Format(dateLayout),
		"totalHours":          secondsToHours(total),
		"expectedHoursPerDay": expected,
		"missingHours":        secondsToHours(missingSec),
		"gaps":                gaps,
		"days":                days,
		"byWorkItem":          items,
	}
}
