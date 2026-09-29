package azuredevops

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sidiney/devpulse/internal/httpx"
	"github.com/sidiney/devpulse/internal/mcp"
	"github.com/sidiney/devpulse/internal/mcp/mcptest"
)

const pat = "my-azdo-pat-xyz"

func azdoFake() *mcptest.Fake {
	return &mcptest.Fake{H: func(w http.ResponseWriter, r *http.Request, body []byte) {
		p := r.URL.EscapedPath()
		switch {
		case p == "/_apis/connectionData":
			mcptest.WriteJSON(w, map[string]any{"authenticatedUser": map[string]any{
				"id": "u1", "providerDisplayName": "Sidiney", "properties": map[string]any{"Account": map[string]any{"$value": "sidiney@corp.com"}}}})
		case p == "/_apis/projects/Meu%20Projeto":
			mcptest.WriteJSON(w, map[string]any{"defaultTeam": map[string]any{"name": "Meu Projeto Team"}})
		case p == "/Meu%20Projeto/Meu%20Projeto%20Team/_apis/work/teamsettings/iterations":
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{
				{"id": "i1", "name": "Sprint 41", "path": `Meu Projeto\Sprint 41`, "attributes": map[string]any{"startDate": "2026-09-07T00:00:00Z", "finishDate": "2026-09-18T00:00:00Z", "timeFrame": "past"}},
				{"id": "i2", "name": "Sprint 42", "path": `Meu Projeto\Sprint 42`, "attributes": map[string]any{"startDate": "2026-09-21T00:00:00Z", "finishDate": "2026-10-02T00:00:00Z", "timeFrame": "current"}},
			}})
		case strings.HasSuffix(p, "/_apis/wit/wiql"):
			mcptest.WriteJSON(w, map[string]any{"workItems": []map[string]any{{"id": 10}, {"id": 11}, {"id": 12}}})
		case p == "/_apis/wit/workitemsbatch":
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{
				{"id": 10, "fields": map[string]any{"System.WorkItemType": "User Story", "System.Title": "Login", "System.State": "Active", "System.BoardColumn": "Doing", "System.AssignedTo": map[string]any{"displayName": "Sidiney"}}},
				{"id": 11, "fields": map[string]any{"System.WorkItemType": "Task", "System.Title": "API", "System.State": "In Progress", "System.Parent": 10.0, "Microsoft.VSTS.Scheduling.RemainingWork": 3.0}},
				{"id": 12, "fields": map[string]any{"System.WorkItemType": "Task", "System.Title": "Tela", "System.State": "To Do", "System.Parent": 10.0, "Microsoft.VSTS.Scheduling.RemainingWork": 5.0}},
			}})
		case p == "/_apis/wit/workitems/11" && r.Method == http.MethodGet:
			mcptest.WriteJSON(w, map[string]any{"id": 11, "rev": 7,
				"fields":    map[string]any{"System.TeamProject": "Meu Projeto", "System.Title": "API", "System.Tags": "backend; api", "System.Description": "<div>Linha 1<br>Linha &amp; 2</div><ul><li>a</li></ul>"},
				"relations": []map[string]any{{"rel": "System.LinkTypes.Related", "url": "https://x/_apis/wit/workItems/99"}, {"rel": "System.LinkTypes.Hierarchy-Reverse", "url": "https://x/_apis/wit/workItems/10"}},
			})
		case (p == "/_apis/wit/workitems/11" && r.Method == http.MethodPatch) || strings.HasPrefix(p, "/Meu%20Projeto/_apis/wit/workitems/"):
			mcptest.WriteJSON(w, map[string]any{"id": 11, "rev": 8, "fields": map[string]any{"System.WorkItemType": "Task", "System.Title": "API"}})
		case strings.HasSuffix(p, "/comments"):
			mcptest.WriteJSON(w, map[string]any{"comments": []any{}})
		default:
			http.NotFound(w, r)
		}
	}}
}

func newAzServer(t *testing.T, f *mcptest.Fake, cfg *boardsConfig) (*mcp.Server, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	if cfg == nil {
		cfg = &boardsConfig{}
	}
	az := NewAzDO(ts.URL, pat, "Meu Projeto", "", httpx.NewHTTPClient(5*time.Second))
	s := mcp.NewServer("t", "0", "")
	registerAzDOTools(s, az, cfg)
	return s, ts
}

func TestAzDOAuthAndQueryEscaping(t *testing.T) {
	f := azdoFake()
	s, _ := newAzServer(t, f, nil)
	tr := mcptest.CallTool(t, s, "query_work_items", map[string]any{
		"types": []string{"User Story"}, "assignedTo": "me", "titleContains": "O'Brien\n) OR 1=1",
	})
	if tr.IsError {
		t.Fatal(tr.Text())
	}
	q := f.Find(http.MethodPost, "/_apis/wit/wiql")
	if len(q) != 1 {
		t.Fatalf("wiql requests: %d", len(q))
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+pat))
	if q[0].Auth != wantAuth {
		t.Errorf("auth = %q", q[0].Auth)
	}
	var body map[string]string
	_ = json.Unmarshal(q[0].Body, &body)
	wiql := body["query"]
	for _, want := range []string{
		"[System.TeamProject] = 'Meu Projeto'",
		"[System.WorkItemType] IN ('User Story')",
		"[System.AssignedTo] = @Me",
		"[System.Title] CONTAINS 'O''Brien) OR 1=1'",
		"NOT IN ('Closed', 'Removed', 'Done')",
	} {
		if !strings.Contains(wiql, want) {
			t.Errorf("WIQL sem %q:\n%s", want, wiql)
		}
	}
}

func TestSprintBoardGroupsByColumn(t *testing.T) {
	f := azdoFake()
	s, _ := newAzServer(t, f, nil)
	tr := mcptest.CallTool(t, s, "get_sprint_board", map[string]any{})
	if tr.IsError {
		t.Fatal(tr.Text())
	}
	var b SprintBoard
	if err := json.Unmarshal(tr.StructuredContent, &b); err != nil {
		t.Fatal(err)
	}
	if b.Sprint.Name != "Sprint 42" || b.Team != "Meu Projeto Team" {
		t.Errorf("sprint/time errados: %+v", b.Sprint)
	}
	if b.TotalItems != 3 || b.RemainingWork != 8 {
		t.Errorf("totais: %+v", b)
	}
	var cols []string
	for _, c := range b.Columns {
		cols = append(cols, c.Column)
	}
	if strings.Join(cols, ",") != "To Do,Doing,In Progress" {
		t.Errorf("colunas = %v", cols)
	}
	q := f.Find(http.MethodPost, "/_apis/wit/wiql")
	var body map[string]string
	_ = json.Unmarshal(q[0].Body, &body)
	if !strings.Contains(body["query"], `[System.IterationPath] = 'Meu Projeto\Sprint 42'`) {
		t.Errorf("WIQL: %s", body["query"])
	}
}

func TestCreateWorkItemWithParentAndSprint(t *testing.T) {
	f := azdoFake()
	s, ts := newAzServer(t, f, nil)
	tr := mcptest.CallTool(t, s, "create_work_item", map[string]any{
		"type": "User Story", "title": "Nova US", "parentId": 5, "sprint": "current",
		"assignedTo": "me", "description": "Primeira linha\n\n- item <b>1</b>", "tags": []string{"x", "X", "y"},
	})
	if tr.IsError {
		t.Fatal(tr.Text())
	}
	var req mcptest.Recorded
	for _, r := range f.All() {
		if r.Method == http.MethodPost && strings.Contains(r.Path, "/_apis/wit/workitems/") {
			req = r
		}
	}
	if req.Path == "" {
		t.Fatalf("POST de criação não encontrado: %+v", f.All())
	}
	if req.ContentType != "application/json-patch+json" {
		t.Errorf("content-type = %q", req.ContentType)
	}
	var ops []patchOp
	_ = json.Unmarshal(req.Body, &ops)
	got := map[string]any{}
	for _, o := range ops {
		got[o.Path] = o.Value
	}
	if got["/fields/System.Title"] != "Nova US" ||
		got["/fields/System.IterationPath"] != `Meu Projeto\Sprint 42` ||
		got["/fields/System.AssignedTo"] != "sidiney@corp.com" ||
		got["/fields/System.Tags"] != "x; y" {
		t.Errorf("ops = %v", got)
	}
	if d := got["/fields/System.Description"].(string); d != "<div>Primeira linha</div><ul><li>item &lt;b&gt;1&lt;/b&gt;</li></ul>" {
		t.Errorf("descrição = %s", d)
	}
	rel, _ := got["/relations/-"].(map[string]any)
	if rel["rel"] != "System.LinkTypes.Hierarchy-Reverse" || rel["url"] != ts.URL+"/_apis/wit/workItems/5" {
		t.Errorf("relação com pai = %v", rel)
	}
}

func TestUpdateWorkItemChangesParentSafely(t *testing.T) {
	f := azdoFake()
	s, _ := newAzServer(t, f, nil)
	tr := mcptest.CallTool(t, s, "update_work_item", map[string]any{"id": 11, "state": "Done", "parentId": 20, "addTags": []string{"urgente"}, "removeTags": []string{"API"}})
	if tr.IsError {
		t.Fatal(tr.Text())
	}
	p := f.Find(http.MethodPatch, "/_apis/wit/workitems/11")
	if len(p) != 1 {
		t.Fatal("PATCH não enviado")
	}
	var ops []patchOp
	_ = json.Unmarshal(p[0].Body, &ops)
	if ops[0].Op != "test" || ops[0].Path != "/rev" || ops[0].Value != float64(7) {
		t.Errorf("primeira op deveria testar a rev: %+v", ops[0])
	}
	var removed, added, tags bool
	for _, o := range ops {
		if o.Op == "remove" && o.Path == "/relations/1" {
			removed = true
		}
		if o.Path == "/relations/-" {
			added = true
		}
		if o.Path == "/fields/System.Tags" && o.Value == "backend; urgente" {
			tags = true
		}
	}
	if !removed || !added || !tags {
		t.Errorf("ops = %+v", ops)
	}
}

func TestAzDOReadOnlyAndFieldGuards(t *testing.T) {
	s, _ := newAzServer(t, azdoFake(), &boardsConfig{ReadOnly: true})
	l := mcptest.ListTools(t, s)
	for _, n := range []string{"create_work_item", "update_work_item", "add_work_item_comment"} {
		if strings.Contains(l, `"`+n+`"`) {
			t.Errorf("read-only expôs %s", n)
		}
	}
	s, _ = newAzServer(t, azdoFake(), nil)
	for _, args := range []map[string]any{
		{"id": 11, "fields": map[string]any{"System.Id": 1}},
		{"id": 11, "fields": map[string]any{"bad field": 1}},
		{"id": 11, "priority": 9},
		{"id": 11},
	} {
		if tr := mcptest.CallTool(t, s, "update_work_item", args); !tr.IsError {
			t.Errorf("deveria falhar: %v", args)
		}
	}
	if tr := mcptest.CallTool(t, s, "query_work_items", map[string]any{"wiql": "DELETE FROM x"}); !tr.IsError {
		t.Errorf("wiql que não começa com SELECT deveria falhar")
	}
}

func TestWorkItemDetailConvertsHTML(t *testing.T) {
	s, _ := newAzServer(t, azdoFake(), nil)
	tr := mcptest.CallTool(t, s, "get_work_item", map[string]any{"id": 11})
	if tr.IsError {
		t.Fatal(tr.Text())
	}
	var d WorkItemDetail
	_ = json.Unmarshal(tr.StructuredContent, &d)
	if d.Description != "Linha 1\nLinha & 2\n- a" {
		t.Errorf("descrição = %q", d.Description)
	}
	if d.Parent == nil || d.Parent.ID != 10 || len(d.OtherLinks) != 1 || d.OtherLinks[0].ID != 99 {
		t.Errorf("links: parent=%+v other=%+v", d.Parent, d.OtherLinks)
	}
}

func TestPATNeverInErrors(t *testing.T) {
	f := &mcptest.Fake{H: func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`echo ` + r.Header.Get("Authorization") + ` ` + pat))
	}}
	s, _ := newAzServer(t, f, nil)
	tr := mcptest.CallTool(t, s, "azdo_whoami", map[string]any{})
	b64 := base64.StdEncoding.EncodeToString([]byte(":" + pat))
	if !tr.IsError || strings.Contains(tr.Text(), pat) || strings.Contains(tr.Text(), b64) {
		t.Fatalf("PAT vazou: %s", tr.Text())
	}
}
