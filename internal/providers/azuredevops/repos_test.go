package azuredevops

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sidyjw/devpulse/internal/httpx"
	"github.com/sidyjw/devpulse/internal/mcp"
	"github.com/sidyjw/devpulse/internal/mcp/mcptest"
)

const (
	repoID    = "11111111-1111-1111-1111-111111111111"
	projID    = "22222222-2222-2222-2222-222222222222"
	meID      = "33333333-3333-3333-3333-333333333333"
	anaID     = "44444444-4444-4444-4444-444444444444"
	mainSHA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	decoySHA  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	repoPath  = "/Meu%20Projeto/_apis/git/repositories/" + repoID
	prPathFor = "/Meu%20Projeto/_apis/git/repositories/" + repoID + "/pullRequests/7"
)

func reposFake() *mcptest.Fake {
	pr := map[string]any{
		"pullRequestId": 7, "title": "Exportar CSV", "status": "active", "isDraft": true,
		"sourceRefName": "refs/heads/feature/csv", "targetRefName": "refs/heads/main",
		"createdBy":  map[string]any{"displayName": "Sidiney"},
		"reviewers":  []map[string]any{{"displayName": "Ana", "vote": 10, "isRequired": true}},
		"repository": map[string]any{"id": repoID, "name": "api", "project": map[string]any{"id": projID, "name": "Meu Projeto"}},
	}
	return &mcptest.Fake{H: func(w http.ResponseWriter, r *http.Request, body []byte) {
		p := r.URL.EscapedPath()
		switch {
		case p == "/_apis/connectionData":
			mcptest.WriteJSON(w, map[string]any{"authenticatedUser": map[string]any{"id": meID, "providerDisplayName": "Sidiney"}})
		case p == "/Meu%20Projeto/_apis/git/repositories" && r.Method == http.MethodGet:
			mcptest.WriteJSON(w, map[string]any{"value": []any{}})
		case p == "/Meu%20Projeto/_apis/git/repositories/api" || p == repoPath:
			mcptest.WriteJSON(w, map[string]any{"id": repoID, "name": "api", "defaultBranch": "refs/heads/main",
				"webUrl": "https://dev.azure.com/org/Meu%20Projeto/_git/api", "project": map[string]any{"id": projID, "name": "Meu Projeto"}})
		case p == repoPath+"/refs" && r.Method == http.MethodGet:
			// the filter is a prefix match: "heads/main" also returns main-old
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{
				{"name": "refs/heads/main-old", "objectId": decoySHA},
				{"name": "refs/heads/main", "objectId": mainSHA},
			}})
		case p == repoPath+"/refs" && r.Method == http.MethodPost:
			var upd []map[string]string
			_ = json.Unmarshal(body, &upd)
			if len(upd) == 1 && upd[0]["name"] == "refs/heads/existe" {
				mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{{"name": upd[0]["name"], "success": false, "updateStatus": "failedToCreate"}}})
				return
			}
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{{"name": upd[0]["name"], "success": true, "updateStatus": "succeeded"}}})
		case p == "/Meu%20Projeto/_apis/git/policy/configurations":
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{
				{"isEnabled": true, "isBlocking": true, "type": map[string]any{"displayName": "Minimum number of reviewers"},
					"settings": map[string]any{"minimumApproverCount": 2, "scope": []any{map[string]any{"refName": "refs/heads/main"}}}},
				{"isEnabled": true, "isBlocking": true, "isDeleted": true, "type": map[string]any{"displayName": "Build"}},
			}})
		case p == "/_apis/IdentityPicker/Identities":
			mcptest.WriteJSON(w, map[string]any{"results": []map[string]any{{"identities": []map[string]any{
				{"localId": anaID, "displayName": "Ana", "mail": "ana@corp.com"},
				{"localId": "55555555-5555-5555-5555-555555555555", "displayName": "Ana Paula", "mail": "ana.paula@corp.com"},
			}}}})
		case p == repoPath+"/pullrequests" && r.Method == http.MethodPost:
			if strings.Contains(string(body), "refs/heads/bloqueada") {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"$id":"1","message":"TF401179: An active pull request for the source and target branch already exists.","typeKey":"GitPullRequestExistsException"}`))
				return
			}
			var in map[string]any
			_ = json.Unmarshal(body, &in)
			out := map[string]any{}
			for k, v := range pr {
				out[k] = v
			}
			out["isDraft"], out["sourceRefName"], out["targetRefName"] = in["isDraft"], in["sourceRefName"], in["targetRefName"]
			mcptest.WriteJSON(w, out)
		case p == "/Meu%20Projeto/_apis/git/pullrequests" || p == repoPath+"/pullrequests" || p == "/Meu%20Projeto/_apis/git/repositories/api/pullrequests":
			mcptest.WriteJSON(w, map[string]any{"value": []any{pr}})
		case p == "/Meu%20Projeto/_apis/git/pullrequests/7":
			mcptest.WriteJSON(w, pr)
		case p == prPathFor && r.Method == http.MethodPatch:
			mcptest.WriteJSON(w, pr)
		case strings.HasPrefix(p, prPathFor+"/reviewers/"):
			mcptest.WriteJSON(w, map[string]any{})
		case p == prPathFor+"/workitems":
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{{"id": "4312"}}})
		case p == prPathFor+"/threads" && r.Method == http.MethodGet:
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{
				{"id": 1, "status": "active", "threadContext": map[string]any{"filePath": "/app.go", "rightFileStart": map[string]any{"line": 12}},
					"comments": []map[string]any{{"content": "Renomeia isso", "commentType": "text", "author": map[string]any{"displayName": "Ana"}, "publishedDate": "2026-09-30T10:00:00Z"}}},
				{"id": 2, "comments": []map[string]any{{"content": "Ana voted 10", "commentType": "system"}}},
			}})
		case p == prPathFor+"/threads" && r.Method == http.MethodPost:
			mcptest.WriteJSON(w, map[string]any{"id": 9})
		case strings.HasPrefix(p, prPathFor+"/threads/"):
			mcptest.WriteJSON(w, map[string]any{})
		case p == "/Meu%20Projeto/_apis/policy/evaluations":
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{
				{"status": "approved", "configuration": map[string]any{"isBlocking": true, "type": map[string]any{"displayName": "Minimum number of reviewers"}}}}})
		case strings.HasPrefix(p, "/_apis/wit/workitems/") && r.Method == http.MethodPatch:
			if strings.HasSuffix(p, "/404") {
				http.NotFound(w, r)
				return
			}
			mcptest.WriteJSON(w, map[string]any{"id": 1, "rev": 2})
		default:
			http.NotFound(w, r)
		}
	}}
}

// find is Fake.Find with an escaped path (the fake records it decoded).
func find(f *mcptest.Fake, method, escaped string) []mcptest.Recorded {
	p, _ := url.PathUnescape(escaped)
	return f.Find(method, p)
}

func newReposServer(t *testing.T, f *mcptest.Fake, cfg *reposConfig) (*mcp.Server, *AzDO) {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	if cfg == nil {
		cfg = &reposConfig{}
	}
	az := NewAzDO(ts.URL, pat, "Meu Projeto", "", httpx.NewHTTPClient(5*time.Second))
	s := mcp.NewServer("t", "0", "")
	registerReposTools(s, az, cfg)
	return s, az
}

func call(t *testing.T, s *mcp.Server, name string, args map[string]any, out any) {
	t.Helper()
	tr := mcptest.CallTool(t, s, name, args)
	if tr.IsError {
		t.Fatalf("%s: %s", name, tr.Text())
	}
	if out != nil {
		if err := json.Unmarshal(tr.StructuredContent, out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParseRemote(t *testing.T) {
	az := NewAzDO("https://dev.azure.com/org", pat, "Padrão", "", nil)
	for remote, want := range map[string][2]string{
		"https://dev.azure.com/org/Meu%20Projeto/_git/api":                 {"Meu Projeto", "api"},
		"https://org@dev.azure.com/org/Meu%20Projeto/_git/api":             {"Meu Projeto", "api"},
		"https://ORG.visualstudio.com/DefaultCollection/Proj/_git/web":     {"Proj", "web"},
		"https://org.visualstudio.com/Proj/_git/web/":                      {"Proj", "web"},
		"https://dev.azure.com/org/_git/Solo":                              {"Solo", "Solo"},
		"git@ssh.dev.azure.com:v3/org/Meu%20Projeto/api":                   {"Meu Projeto", "api"},
		"org@vs-ssh.visualstudio.com:v3/org/Proj/web":                      {"Proj", "web"},
		"https://dev.azure.com/org/Proj/_git/api.with.dots":                {"Proj", "api.with.dots"},
		"https://dev.azure.com/org/Proj/_git/r%C3%A9po":                    {"Proj", "répo"},
		"https://user:secret@dev.azure.com/org/Proj/_git/api?x=1#fragment": {"Proj", "api"},
	} {
		p, r, err := az.repoRef("", remote)
		if err != nil || p != want[0] || r != want[1] {
			t.Errorf("%s → %q %q %v", remote, p, r, err)
		}
	}
	for _, bad := range []string{
		"https://dev.azure.com/outra/Proj/_git/api",
		"https://dev.azure.com/organization/Proj/_git/api", // same prefix, different org
		"https://github.com/org/Proj/_git/api",
		"git@ssh.dev.azure.com:v3/outra/Proj/api",
		"git@github.com:org/api.git",
		"https://dev.azure.com/org/Proj/api",
		"a/b",
		"",
	} {
		if _, _, err := az.repoRef("", bad); err == nil {
			t.Errorf("%q deveria ser recusado", bad)
		}
	}
	if p, r, err := az.repoRef("", "api"); err != nil || p != "Padrão" || r != "api" {
		t.Errorf("nome simples: %q %q %v", p, r, err)
	}
	server := NewAzDO("https://tfs.corp.local/tfs/Colecao", pat, "", "", nil)
	if p, r, err := server.repoRef("", "https://tfs.corp.local/tfs/Colecao/Proj/_git/api"); err != nil || p != "Proj" || r != "api" {
		t.Errorf("Azure DevOps Server: %q %q %v", p, r, err)
	}
}

func TestBranchNames(t *testing.T) {
	for _, ok := range []string{"feature/4312-csv", "refs/heads/main", "fix_1.2", "release/2026.09"} {
		if _, err := branchRef(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a b", "a..b", "a~1", "a^", "a:b", "x?", "x*", "[x", `a\b`, "/a", "a/", "a//b", "a.lock", "a.", ".a", "x/.y", "@", "a@{1}", "a\x01"} {
		if _, err := branchRef(bad); err == nil {
			t.Errorf("%q deveria ser recusado", bad)
		}
	}
}

func TestCreateBranchFromDefaultAndLinks(t *testing.T) {
	f := reposFake()
	s, az := newReposServer(t, f, nil)
	var res BranchResult
	call(t, s, "create_branch", map[string]any{
		"repository": az.orgURL + "/Meu%20Projeto/_git/api", "name": "feature/4312-csv", "workItemId": 4312,
	}, &res)
	post := find(f, http.MethodPost, repoPath+"/refs")
	if len(post) != 1 {
		t.Fatalf("POST refs: %d", len(post))
	}
	var upd []map[string]string
	_ = json.Unmarshal(post[0].Body, &upd)
	if upd[0]["name"] != "refs/heads/feature/4312-csv" || upd[0]["oldObjectId"] != zeroObjectID || upd[0]["newObjectId"] != mainSHA {
		t.Errorf("atualização da ref = %v", upd)
	}
	if res.From != "main" || res.Commit != mainSHA || res.WorkItemLinked != 4312 || res.Warning != "" {
		t.Errorf("resultado = %+v", res)
	}
	link := find(f, http.MethodPatch, "/_apis/wit/workitems/4312")
	if len(link) != 1 {
		t.Fatal("vínculo com o work item não enviado")
	}
	var ops []patchOp
	_ = json.Unmarshal(link[0].Body, &ops)
	rel, _ := ops[0].Value.(map[string]any)
	if rel["rel"] != "ArtifactLink" || rel["url"] != "vstfs:///Git/Ref/"+projID+"%2F"+repoID+"%2FGBfeature%2F4312-csv" {
		t.Errorf("vínculo = %v", rel)
	}

	// from a commit, the SHA is used as is; a failed link is a warning
	f = reposFake()
	s, _ = newReposServer(t, f, nil)
	res = BranchResult{}
	call(t, s, "create_branch", map[string]any{"repository": "api", "name": "hotfix", "from": decoySHA, "workItemId": 404}, &res)
	if res.Commit != decoySHA || res.WorkItemLinked != 0 || !strings.Contains(res.Warning, "#404") {
		t.Errorf("resultado = %+v", res)
	}
	if len(find(f, http.MethodGet, repoPath+"/refs")) != 0 {
		t.Error("com SHA não deveria consultar refs")
	}

	// Azure DevOps reports failures per ref, with HTTP 200
	if tr := mcptest.CallTool(t, s, "create_branch", map[string]any{"repository": "api", "name": "existe"}); !tr.IsError || !strings.Contains(tr.Text(), "failedToCreate") {
		t.Errorf("deveria falhar: %s", tr.Text())
	}
	if tr := mcptest.CallTool(t, s, "create_branch", map[string]any{"repository": "api", "name": "x", "from": "nao-existe"}); !tr.IsError {
		t.Error("origem inexistente deveria falhar")
	}
}

func TestRemoteFromOtherOrgSendsNothing(t *testing.T) {
	f := reposFake()
	s, _ := newReposServer(t, f, nil)
	tr := mcptest.CallTool(t, s, "create_branch", map[string]any{"repository": "https://dev.azure.com/outra/P/_git/r", "name": "x"})
	if !tr.IsError || !strings.Contains(tr.Text(), "outra organização") {
		t.Fatalf("deveria recusar: %s", tr.Text())
	}
	if n := len(f.All()); n != 0 {
		t.Errorf("%d requisições enviadas", n)
	}
}

func TestBranchPolicies(t *testing.T) {
	f := reposFake()
	s, _ := newReposServer(t, f, nil)
	var bp BranchPolicies
	call(t, s, "get_branch_policies", map[string]any{"repository": "api"}, &bp)
	if bp.Branch != "main" || !bp.PullRequestRequired || len(bp.Policies) != 1 {
		t.Fatalf("políticas = %+v", bp)
	}
	if _, ok := bp.Policies[0].Settings["scope"]; ok || bp.Policies[0].Settings["minimumApproverCount"] != float64(2) {
		t.Errorf("settings = %v", bp.Policies[0].Settings)
	}
	q := find(f, http.MethodGet, "/_apis/git/policy/configurations")[0].RawQuery
	if !strings.Contains(q, "repositoryId="+repoID) || !strings.Contains(q, "refName=refs%2Fheads%2Fmain") {
		t.Errorf("query = %s", q)
	}
}

func TestCreatePullRequest(t *testing.T) {
	f := reposFake()
	s, _ := newReposServer(t, f, nil)
	var res PRWriteResult
	call(t, s, "create_pull_request", map[string]any{
		"repository": "api", "sourceBranch": "feature/csv", "title": "  Exportar CSV ", "description": "## O quê\n- csv",
		"requiredReviewers": []string{"ana@corp.com"}, "reviewers": []string{"me", anaID},
		"workItemIds": []int{4312}, "labels": []string{"api"},
	}, &res)
	post := find(f, http.MethodPost, repoPath+"/pullrequests")
	if len(post) != 1 {
		t.Fatal("POST da PR não enviado")
	}
	var body struct {
		SourceRefName, TargetRefName, Title, Description string
		IsDraft                                          bool
		Reviewers                                        []struct {
			ID         string
			IsRequired bool
		}
		WorkItemRefs []struct{ ID string }
		Labels       []struct{ Name string }
	}
	_ = json.Unmarshal(post[0].Body, &body)
	if body.SourceRefName != "refs/heads/feature/csv" || body.TargetRefName != "refs/heads/main" || body.Title != "Exportar CSV" || body.IsDraft {
		t.Errorf("corpo = %+v", body)
	}
	// ana (required, by e-mail) and her GUID are the same person: listed once, required
	if len(body.Reviewers) != 2 || body.Reviewers[0].ID != anaID || !body.Reviewers[0].IsRequired || body.Reviewers[1].ID != meID || body.Reviewers[1].IsRequired {
		t.Errorf("revisores = %+v", body.Reviewers)
	}
	if len(body.WorkItemRefs) != 1 || body.WorkItemRefs[0].ID != "4312" || len(body.Labels) != 1 {
		t.Errorf("work items/labels = %+v %+v", body.WorkItemRefs, body.Labels)
	}
	if res.PullRequest == nil || res.PullRequest.ID != 7 || !strings.HasSuffix(res.WebURL, "/Meu%20Projeto/_git/api/pullrequest/7") {
		t.Errorf("resultado = %+v", res)
	}

	for _, args := range []map[string]any{
		{"repository": "api", "sourceBranch": "main", "title": "x"},                              // same as the default target
		{"repository": "api", "sourceBranch": "a", "title": " "},                                 // empty title
		{"repository": "api", "sourceBranch": "a b", "title": "x"},                               // invalid branch
		{"repository": "api", "sourceBranch": "a", "title": "x", "reviewers": []string{"Ana P"}}, // ambiguous
		{"repository": "api", "sourceBranch": "a", "title": "x", "description": strings.Repeat("x", 4001)},
	} {
		if tr := mcptest.CallTool(t, s, "create_pull_request", args); !tr.IsError {
			t.Errorf("deveria falhar: %v", args)
		}
	}
	// the Azure DevOps message is shown instead of the raw JSON
	tr := mcptest.CallTool(t, s, "create_pull_request", map[string]any{"repository": "api", "sourceBranch": "bloqueada", "title": "x"})
	if !tr.IsError || !strings.Contains(tr.Text(), ": TF401179: An active pull request") || strings.Contains(tr.Text(), "typeKey") {
		t.Errorf("erro = %s", tr.Text())
	}
}

func TestUpdatePullRequestAndComments(t *testing.T) {
	f := reposFake()
	s, _ := newReposServer(t, f, nil)
	var res PRWriteResult
	call(t, s, "update_pull_request", map[string]any{"id": 7, "draft": false, "addReviewers": []string{"ana@corp.com"}, "workItemIds": []int{4312, 404}}, &res)
	patch := find(f, http.MethodPatch, prPathFor)
	if len(patch) != 1 || string(patch[0].Body) != `{"isDraft":false}` {
		t.Fatalf("PATCH = %+v", patch)
	}
	put := find(f, http.MethodPut, prPathFor+"/reviewers/"+anaID)
	if len(put) != 1 || !strings.Contains(string(put[0].Body), `"isRequired":false`) {
		t.Errorf("PUT revisor = %+v", put)
	}
	link := find(f, http.MethodPatch, "/_apis/wit/workitems/4312")
	if len(link) != 1 || !strings.Contains(string(link[0].Body), "vstfs:///Git/PullRequestId/"+projID+"%2F"+repoID+"%2F7") {
		t.Errorf("vínculo = %+v", link)
	}
	if !strings.Contains(res.Message, "vinculada a #4312") || !strings.Contains(res.Warning, "#404") {
		t.Errorf("resultado = %+v", res)
	}
	if tr := mcptest.CallTool(t, s, "update_pull_request", map[string]any{"id": 7}); !tr.IsError {
		t.Error("sem alterações deveria falhar")
	}

	call(t, s, "add_pull_request_comment", map[string]any{"id": 7, "text": "Feito", "threadId": 1}, nil)
	reply := find(f, http.MethodPost, prPathFor+"/threads/1/comments")
	if len(reply) != 1 || !strings.Contains(string(reply[0].Body), `"parentCommentId":1`) {
		t.Errorf("resposta = %+v", reply)
	}
	call(t, s, "add_pull_request_comment", map[string]any{"id": 7, "text": "Aqui", "filePath": "src/app.go", "line": 3}, &res)
	thread := find(f, http.MethodPost, prPathFor+"/threads")
	if len(thread) != 1 || !strings.Contains(string(thread[0].Body), `"filePath":"/src/app.go"`) || !strings.Contains(res.Message, "thread 9") {
		t.Errorf("thread = %+v / %+v", thread, res)
	}
	for _, args := range []map[string]any{
		{"id": 7, "text": " "},
		{"id": 7, "text": "x", "line": 3},
		{"id": 7, "text": "x", "threadId": 1, "filePath": "/a"},
	} {
		if tr := mcptest.CallTool(t, s, "add_pull_request_comment", args); !tr.IsError {
			t.Errorf("deveria falhar: %v", args)
		}
	}
}

func TestReadPullRequests(t *testing.T) {
	f := reposFake()
	s, _ := newReposServer(t, f, nil)
	var list struct {
		Count        int
		PullRequests []PullRequest
	}
	call(t, s, "list_pull_requests", map[string]any{"createdBy": "me", "targetBranch": "main"}, &list)
	q := find(f, http.MethodGet, "/Meu%20Projeto/_apis/git/pullrequests")[0].RawQuery
	for _, want := range []string{"searchCriteria.creatorId=" + meID, "searchCriteria.status=active", "searchCriteria.targetRefName=refs%2Fheads%2Fmain"} {
		if !strings.Contains(q, want) {
			t.Errorf("query sem %s: %s", want, q)
		}
	}
	if list.Count != 1 || list.PullRequests[0].Reviewers[0].Vote != "approved" || list.PullRequests[0].SourceBranch != "feature/csv" {
		t.Errorf("lista = %+v", list)
	}
	if tr := mcptest.CallTool(t, s, "list_pull_requests", map[string]any{"status": "merged"}); !tr.IsError {
		t.Error("status inválido deveria falhar")
	}

	var d PullRequestDetail
	call(t, s, "get_pull_request", map[string]any{"id": 7}, &d)
	if len(d.WorkItems) != 1 || d.WorkItems[0] != 4312 || len(d.Checks) != 1 || d.Checks[0].Status != "approved" {
		t.Errorf("detalhe = %+v", d)
	}
	if len(d.Threads) != 1 || d.Threads[0].FilePath != "/app.go" || d.Threads[0].Line != 12 {
		t.Errorf("threads (sem as de sistema) = %+v", d.Threads)
	}

	var br struct {
		DefaultBranch string
		Branches      []Branch
	}
	call(t, s, "list_branches", map[string]any{"repository": "api"}, &br)
	if br.DefaultBranch != "main" || len(br.Branches) != 2 || !br.Branches[1].IsDefault || br.Branches[0].IsDefault {
		t.Errorf("branches = %+v", br)
	}
}

func TestReposReadOnly(t *testing.T) {
	s, _ := newReposServer(t, reposFake(), &reposConfig{ReadOnly: true})
	l := mcptest.ListTools(t, s)
	for _, n := range []string{"create_branch", "create_pull_request", "update_pull_request", "add_pull_request_comment"} {
		if strings.Contains(l, `"`+n+`"`) {
			t.Errorf("somente leitura expôs %s", n)
		}
	}
	if !strings.Contains(l, `"get_pull_request"`) {
		t.Error("tools de leitura deveriam continuar")
	}
}
