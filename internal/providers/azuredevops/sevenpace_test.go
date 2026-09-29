package azuredevops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sidiney/devpulse/internal/httpx"
	"github.com/sidiney/devpulse/internal/mcp"
	"github.com/sidiney/devpulse/internal/mcp/mcptest"
)

const token = "super-secret-token-123"

var actTypes = map[string]any{"data": map[string]any{
	"enabled": true,
	"activityTypes": []map[string]any{
		{"id": "00000000-0000-0000-0000-000000000000", "name": "[Not Set]"},
		{"id": "d0e030e2-b8b4-e711-8418-00155d0a6b50", "name": "Development"},
		{"id": "d4e030e2-b8b4-e711-8418-00155d0a6b50", "name": "Testing"},
	},
}}

func sevenPaceFake(existing []map[string]any) *mcptest.Fake {
	return &mcptest.Fake{H: func(w http.ResponseWriter, r *http.Request, body []byte) {
		switch {
		case r.URL.Path == "/api/rest/activityTypes":
			mcptest.WriteJSON(w, actTypes)
		case r.URL.Path == "/api/rest/me":
			mcptest.WriteJSON(w, map[string]any{"data": map[string]any{"user": map[string]any{"name": "Sidiney", "uniqueName": "s@x.com"}}})
		case r.URL.Path == "/api/rest/workLogs" && r.Method == http.MethodGet:
			mcptest.WriteJSON(w, map[string]any{"data": existing})
		case r.URL.Path == "/api/rest/workLogs" && r.Method == http.MethodPost:
			var in map[string]any
			_ = json.Unmarshal(body, &in)
			in["id"] = "11111111-2222-3333-4444-555555555555"
			mcptest.WriteJSON(w, map[string]any{"data": in})
		case strings.HasPrefix(r.URL.Path, "/api/rest/workLogs/") && r.Method == http.MethodPatch:
			mcptest.WriteJSON(w, map[string]any{"data": map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/api/rest/workLogs/"), "timestamp": "2026-01-01T09:00:00", "length": 3600}})
		case strings.HasPrefix(r.URL.Path, "/api/rest/workLogs/") && r.Method == http.MethodDelete:
			mcptest.WriteJSON(w, map[string]any{"data": nil})
		default:
			http.NotFound(w, r)
		}
	}}
}

func newSPServer(t *testing.T, f *mcptest.Fake, cfg *sevenPaceConfig) (*mcp.Server, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	if cfg == nil {
		cfg = &sevenPaceConfig{}
	}
	sp := NewSevenPace(ts.URL+"/api", token, httpx.NewHTTPClient(5*time.Second))
	s := mcp.NewServer("t", "0", "")
	registerSevenPaceTools(s, sp, nil, cfg)
	return s, ts
}

func day(offset int) string { return today().AddDate(0, 0, offset).Format(dateLayout) }
func TestStrictArguments(t *testing.T) {
	s, _ := newSPServer(t, sevenPaceFake(nil), nil)
	tr := mcptest.CallTool(t, s, "log_time", map[string]any{"workItemId": 1, "date": day(0), "hours": 1, "comment": "x", "token": "abc"})
	if !tr.IsError || !strings.Contains(tr.Text(), "unknown field") {
		t.Errorf("campo desconhecido deveria ser rejeitado: %s", tr.Text())
	}
}

// ---------- 7pace ----------

func TestLogTimeSendsCorrectPayload(t *testing.T) {
	f := sevenPaceFake(nil)
	s, _ := newSPServer(t, f, nil)
	d := day(-1)
	tr := mcptest.CallTool(t, s, "log_time", map[string]any{
		"workItemId": 1234, "date": d, "hours": 1.5, "comment": "Revisão de PR",
		"activityType": "development", "startTime": "14:30",
	})
	if tr.IsError {
		t.Fatalf("erro: %s", tr.Text())
	}
	posts := f.Find(http.MethodPost, "/workLogs")
	if len(posts) != 1 {
		t.Fatalf("esperava 1 POST, veio %d", len(posts))
	}
	p := posts[0]
	if p.Auth != "Bearer "+token {
		t.Errorf("Authorization = %q", p.Auth)
	}
	if !strings.Contains(p.RawQuery, "api-version=3.2") {
		t.Errorf("query = %q", p.RawQuery)
	}
	var body map[string]any
	_ = json.Unmarshal(p.Body, &body)
	want := map[string]any{
		"timestamp": d + "T14:30:00", "length": float64(5400), "workItemId": float64(1234),
		"comment": "Revisão de PR", "activityTypeId": "d0e030e2-b8b4-e711-8418-00155d0a6b50",
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%s] = %v, quero %v", k, body[k], v)
		}
	}
	if _, ok := body["billableLength"]; ok {
		t.Errorf("billableLength não deveria ser enviado sem billableHours")
	}
	// the duplicate check queried exactly that day, with the documented params
	gets := f.Find(http.MethodGet, "/workLogs")
	if len(gets) == 0 || !strings.Contains(gets[0].RawQuery, "$fromTimestamp="+day(-2)+"T23%3A59%3A59") ||
		!strings.Contains(gets[0].RawQuery, "$toTimestamp="+day(0)+"T00%3A00%3A00") {
		t.Errorf("query de listagem inesperada: %v", gets)
	}
}

func TestLogTimeRejectsDuplicate(t *testing.T) {
	d := day(0)
	f := sevenPaceFake([]map[string]any{{
		"id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "timestamp": d + "T09:00:00",
		"length": 7200, "workItemId": 42, "comment": "Daily  meeting",
	}})
	s, _ := newSPServer(t, f, nil)
	tr := mcptest.CallTool(t, s, "log_time", map[string]any{"workItemId": 42, "date": d, "hours": 2, "comment": "daily meeting"})
	if !tr.IsError || !strings.Contains(tr.Text(), "idêntico") {
		t.Fatalf("deveria recusar duplicata: %s", tr.Text())
	}
	if n := len(f.Find(http.MethodPost, "/workLogs")); n != 0 {
		t.Fatalf("não deveria ter feito POST (%d)", n)
	}
	tr = mcptest.CallTool(t, s, "log_time", map[string]any{"workItemId": 42, "date": d, "hours": 2, "comment": "daily meeting", "allowDuplicate": true})
	if tr.IsError {
		t.Fatalf("allowDuplicate deveria permitir: %s", tr.Text())
	}
	if !strings.Contains(tr.Text(), `"dayTotalHours": 4`) {
		t.Errorf("total do dia deveria ser 4: %s", tr.Text())
	}
}

func TestLogTimeValidation(t *testing.T) {
	f := sevenPaceFake(nil)
	s, _ := newSPServer(t, f, nil)
	cases := []map[string]any{
		{"workItemId": 0, "date": day(0), "hours": 1, "comment": "x"},
		{"workItemId": 1, "date": "29/09/2026", "hours": 1, "comment": "x"},
		{"workItemId": 1, "date": day(0), "hours": 25, "comment": "x"},
		{"workItemId": 1, "date": day(0), "hours": 0, "comment": "x"},
		{"workItemId": 1, "date": day(0), "hours": 1, "comment": "  "},
		{"workItemId": 1, "date": day(60), "hours": 1, "comment": "x"},
		{"workItemId": 1, "date": day(0), "hours": 1, "comment": "x", "startTime": "25:00"},
		{"workItemId": 1, "date": day(0), "hours": 1, "comment": "x", "activityType": "Dormir"},
	}
	for i, c := range cases {
		if tr := mcptest.CallTool(t, s, "log_time", c); !tr.IsError {
			t.Errorf("caso %d deveria falhar: %v", i, c)
		}
	}
	if n := len(f.Find(http.MethodPost, "/workLogs")); n != 0 {
		t.Fatalf("nenhum POST deveria ter acontecido (%d)", n)
	}
}

func TestBatchValidatesEverythingFirst(t *testing.T) {
	f := sevenPaceFake(nil)
	s, _ := newSPServer(t, f, nil)
	tr := mcptest.CallTool(t, s, "log_time_batch", map[string]any{"entries": []map[string]any{
		{"workItemId": 1, "date": day(-1), "hours": 4, "comment": "a"},
		{"workItemId": 2, "date": day(-1), "hours": 4, "comment": "b", "activityType": "inexistente"},
	}})
	if !tr.IsError || !strings.Contains(tr.Text(), "nada foi lançado") {
		t.Fatalf("esperava erro de validação: %s", tr.Text())
	}
	if n := len(f.Find(http.MethodPost, "/workLogs")); n != 0 {
		t.Fatalf("lote inválido não pode lançar nada (%d POSTs)", n)
	}
	tr = mcptest.CallTool(t, s, "log_time_batch", map[string]any{"entries": []map[string]any{
		{"workItemId": 1, "date": day(-1), "hours": 4, "comment": "a"},
		{"workItemId": 1, "date": day(-1), "hours": 4, "comment": "a"},
	}})
	if !tr.IsError || !strings.Contains(tr.Text(), "idênticos") {
		t.Fatalf("duplicata dentro do lote deveria falhar: %s", tr.Text())
	}
	tr = mcptest.CallTool(t, s, "log_time_batch", map[string]any{"entries": []map[string]any{
		{"workItemId": 1, "date": day(-1), "hours": 4, "comment": "a"},
		{"workItemId": 2, "date": day(-1), "hours": 4, "comment": "b", "activityType": "Testing"},
	}})
	if tr.IsError {
		t.Fatalf("lote válido falhou: %s", tr.Text())
	}
	if n := len(f.Find(http.MethodPost, "/workLogs")); n != 2 {
		t.Fatalf("esperava 2 POSTs, veio %d", n)
	}
	if !strings.Contains(tr.Text(), `"`+day(-1)+`": 8`) {
		t.Errorf("total do dia deveria ser 8: %s", tr.Text())
	}
}

func TestUpdateUsesPatchWithOnlyChangedFields(t *testing.T) {
	f := sevenPaceFake(nil)
	s, _ := newSPServer(t, f, nil)
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	tr := mcptest.CallTool(t, s, "update_worklog", map[string]any{"worklogId": id, "hours": 0.25})
	if tr.IsError {
		t.Fatal(tr.Text())
	}
	p := f.Find(http.MethodPatch, "/workLogs/"+id)
	if len(p) != 1 {
		t.Fatalf("esperava PATCH, requests: %+v", f.All())
	}
	if string(p[0].Body) != `{"length":900}` {
		t.Errorf("body = %s", p[0].Body)
	}
	if tr := mcptest.CallTool(t, s, "update_worklog", map[string]any{"worklogId": "../../me", "hours": 1}); !tr.IsError {
		t.Errorf("id que não é GUID deveria ser rejeitado")
	}
	if tr := mcptest.CallTool(t, s, "update_worklog", map[string]any{"worklogId": id, "startTime": "10:00"}); !tr.IsError {
		t.Errorf("startTime sem date deveria falhar")
	}
}

func TestToolGating(t *testing.T) {
	list := func(cfg *sevenPaceConfig) string {
		s, _ := newSPServer(t, sevenPaceFake(nil), cfg)
		return mcptest.ListTools(t, s)
	}
	def := list(&sevenPaceConfig{})
	if !strings.Contains(def, `"log_time"`) || strings.Contains(def, `"delete_worklog"`) {
		t.Errorf("padrão: log_time sim, delete não")
	}
	if l := list(&sevenPaceConfig{EnableDelete: true}); !strings.Contains(l, `"delete_worklog"`) {
		t.Errorf("SEVENPACE_ENABLE_DELETE deveria expor delete_worklog")
	}
	ro := list(&sevenPaceConfig{ReadOnly: true, EnableDelete: true})
	for _, n := range []string{"log_time", "log_time_batch", "update_worklog", "delete_worklog"} {
		if strings.Contains(ro, `"`+n+`"`) {
			t.Errorf("read-only não deveria expor %s", n)
		}
	}
	if !strings.Contains(ro, `"get_worklogs"`) {
		t.Errorf("read-only deveria manter get_worklogs")
	}
}

func TestActivityTypesArrayShape(t *testing.T) {
	f := &mcptest.Fake{H: func(w http.ResponseWriter, r *http.Request, _ []byte) {
		mcptest.WriteJSON(w, map[string]any{"data": []map[string]any{{"id": "d0e030e2-b8b4-e711-8418-00155d0a6b50", "name": "Dev"}}})
	}}
	ts := httptest.NewServer(f)
	defer ts.Close()
	sp := NewSevenPace(ts.URL+"/api", token, httpx.NewHTTPClient(time.Second))
	id, err := sp.ResolveActivityType(context.Background(), "dev")
	if err != nil || id != "d0e030e2-b8b4-e711-8418-00155d0a6b50" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestWorklogPaginationAndHours(t *testing.T) {
	d := day(-1)
	f := &mcptest.Fake{}
	f.H = func(w http.ResponseWriter, r *http.Request, _ []byte) {
		skip := r.URL.Query().Get("$skip")
		n := pageSize
		if skip != "0" {
			n = 3
		}
		items := make([]map[string]any, n)
		for i := range items {
			items[i] = map[string]any{"id": fmt.Sprint(i), "timestamp": d + "T08:00:00.1234567", "length": 1800, "workItemId": 7}
		}
		mcptest.WriteJSON(w, map[string]any{"data": items})
	}
	ts := httptest.NewServer(f)
	defer ts.Close()
	sp := NewSevenPace(ts.URL+"/api", token, httpx.NewHTTPClient(time.Second))
	dd, _ := time.Parse(dateLayout, d)
	ws, err := sp.Worklogs(context.Background(), WorklogQuery{From: dd, To: dd})
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != pageSize+3 {
		t.Fatalf("len=%d", len(ws))
	}
	if ws[0].Hours != 0.5 || ws[0].Duration != "0h30" || ws[0].Date != d || ws[0].Time != "08:00" {
		t.Errorf("conversão errada: %+v", ws[0])
	}
}

func TestTimeSummaryGaps(t *testing.T) {
	mon := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC) // segunda
	ws := []Worklog{
		{Date: "2026-09-21", Hours: 8, WorkItemID: ptr(1)},
		{Date: "2026-09-22", Hours: 5.5, WorkItemID: ptr(2)},
		{Date: "2026-09-26", Hours: 1, WorkItemID: ptr(1)}, // sábado
	}
	sum := buildSummary(context.Background(), ws, mon, mon.AddDate(0, 0, 6), 8, nil)
	if sum["totalHours"].(float64) != 14.5 {
		t.Errorf("total = %v", sum["totalHours"])
	}
	if sum["missingHours"].(float64) != 26.5 { // 2.5 + 4*... (ter 2.5, qua/qui/sex 8)
		t.Errorf("faltando = %v", sum["missingHours"])
	}
	days := sum["days"].([]daySummary)
	if len(days) != 6 { // 5 dias úteis + sábado com lançamento
		t.Errorf("dias = %d", len(days))
	}
}

func ptr[T any](v T) *T { return &v }

// ---------- HTTP safety ----------

func TestRedirectsAreNotFollowed(t *testing.T) {
	var hit bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer evil.Close()
	f := &mcptest.Fake{H: func(w http.ResponseWriter, r *http.Request, _ []byte) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusFound)
	}}
	ts := httptest.NewServer(f)
	defer ts.Close()
	sp := NewSevenPace(ts.URL+"/api", token, httpx.NewHTTPClient(time.Second))
	_, err := sp.Me(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirecionamento bloqueado") {
		t.Fatalf("err = %v", err)
	}
	if hit {
		t.Fatal("o redirecionamento foi seguido!")
	}
}

func TestTokenNeverAppearsInErrors(t *testing.T) {
	f := &mcptest.Fake{H: func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":"bad token %s"}`, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}}
	ts := httptest.NewServer(f)
	defer ts.Close()
	s := mcp.NewServer("t", "0", "")
	registerSevenPaceTools(s, NewSevenPace(ts.URL+"/api", token, httpx.NewHTTPClient(time.Second)), nil, &sevenPaceConfig{})
	tr := mcptest.CallTool(t, s, "sevenpace_whoami", map[string]any{})
	if !tr.IsError || strings.Contains(tr.Text(), token) || !strings.Contains(tr.Text(), "[REDACTED]") {
		t.Fatalf("token vazou ou erro inesperado: %s", tr.Text())
	}
	if !strings.Contains(tr.Text(), "token inválido") {
		t.Errorf("mensagem deveria explicar 401: %s", tr.Text())
	}
}

func TestResponseSizeLimit(t *testing.T) {
	f := &mcptest.Fake{H: func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Write(bytes.Repeat([]byte("a"), httpx.MaxResponseBytes+10))
	}}
	ts := httptest.NewServer(f)
	defer ts.Close()
	sp := NewSevenPace(ts.URL+"/api", token, httpx.NewHTTPClient(5*time.Second))
	if _, err := sp.Me(context.Background()); err == nil || !strings.Contains(err.Error(), "excedeu") {
		t.Fatalf("err = %v", err)
	}
}
