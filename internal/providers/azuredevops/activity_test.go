package azuredevops

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sidyjw/devpulse/internal/mcp/mcptest"
)

const (
	webID = "66666666-6666-6666-6666-666666666666"
	oldID = "77777777-7777-7777-7777-777777777777"
)

func activityFake() *mcptest.Fake {
	repo := func(id, name string, disabled bool) map[string]any {
		return map[string]any{"id": id, "name": name, "isDisabled": disabled, "project": map[string]any{"id": projID, "name": "Meu Projeto"}}
	}
	pr := func(id int, creator, creatorName, created string) map[string]any {
		return map[string]any{
			"pullRequestId": id, "title": fmt.Sprintf("PR %d", id), "status": "active", "creationDate": created,
			"sourceRefName": "refs/heads/users/sidiney/4312-exportar-csv", "targetRefName": "refs/heads/main",
			"createdBy":  map[string]any{"id": creator, "displayName": creatorName},
			"repository": map[string]any{"id": repoID, "name": "api", "project": map[string]any{"id": projID, "name": "Meu Projeto"}},
		}
	}
	var commits []map[string]any
	for i := 0; i < 25; i++ {
		commits = append(commits, map[string]any{"commitId": fmt.Sprintf("%040d", i), "comment": "feat: exportar CSV\n\ncorpo longo", "author": map[string]any{"date": "2026-10-02T14:00:00Z"}})
	}
	vote := func(id string, v int, date string) map[string]any {
		return map[string]any{
			"properties": map[string]any{"CodeReviewThreadType": map[string]any{"$value": "VoteUpdate"}, "CodeReviewVoteResult": map[string]any{"$value": fmt.Sprint(v)}},
			"comments":   []map[string]any{{"content": "voted", "commentType": "system", "publishedDate": date, "author": map[string]any{"id": id}}},
		}
	}
	return &mcptest.Fake{H: func(w http.ResponseWriter, r *http.Request, body []byte) {
		p := r.URL.EscapedPath()
		q := r.URL.Query()
		switch {
		case p == "/_apis/connectionData":
			mcptest.WriteJSON(w, map[string]any{"authenticatedUser": map[string]any{"id": meID, "providerDisplayName": "Sidiney"}})
		case p == "/Meu%20Projeto/_apis/git/repositories":
			mcptest.WriteJSON(w, map[string]any{"value": []any{repo(repoID, "api", false), repo(webID, "web", false), repo(oldID, "legado", true)}})
		case p == "/Meu%20Projeto/_apis/git/repositories/api":
			mcptest.WriteJSON(w, repo(repoID, "api", false))
		case p == repoPath+"/pushes":
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{
				{"pushId": 41, "date": "2026-10-02T21:11:34.5741964Z", "pushedBy": map[string]any{"displayName": "Sidiney"},
					"refUpdates": []map[string]any{{"name": "refs/heads/users/sidiney/4312-exportar-csv", "newObjectId": mainSHA}}},
				{"pushId": 40, "date": "2026-10-02T12:00:00Z", "pushedBy": map[string]any{"displayName": "Sidiney"},
					"refUpdates": []map[string]any{{"name": "refs/heads/users/sidiney/velha", "newObjectId": zeroObjectID}}},
				// outside the period: the server filter is not trusted blindly
				{"pushId": 39, "date": "2026-09-20T12:00:00Z", "pushedBy": map[string]any{"displayName": "Sidiney"}},
				// only the merge ref Azure DevOps keeps for a pull request: noise
				{"pushId": 42, "date": "2026-10-02T21:12:00Z", "pushedBy": map[string]any{"displayName": "Sidiney"},
					"refUpdates": []map[string]any{{"name": "refs/pull/7/merge", "newObjectId": mainSHA}}},
			}})
		case p == "/Meu%20Projeto/_apis/git/repositories/"+webID+"/pushes":
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"TF401027: You need the Git 'GenericRead' permission."}`))
		case p == repoPath+"/commits" && q.Get("pushId") == "41":
			mcptest.WriteJSON(w, map[string]any{"value": commits})
		case p == repoPath+"/commits":
			mcptest.WriteJSON(w, map[string]any{"value": []any{}})
		case p == "/Meu%20Projeto/_apis/git/pullrequests" && q.Get("searchCriteria.creatorId") == meID:
			mcptest.WriteJSON(w, map[string]any{"value": []any{pr(7, meID, "Sidiney", "2026-10-02T13:00:00Z")}})
		case p == "/Meu%20Projeto/_apis/git/pullrequests" && q.Get("searchCriteria.reviewerId") == meID:
			mcptest.WriteJSON(w, map[string]any{"value": []any{
				pr(7, meID, "Sidiney", "2026-10-02T13:00:00Z"), // also in the first list
				pr(8, anaID, "Ana", "2026-09-28T13:00:00Z"),
				pr(9, anaID, "Ana", "2026-09-29T13:00:00Z"), // nothing of mine in the period
				pr(5, anaID, "Ana", "2026-07-01T13:00:00Z"), // before lookback: never checked
			}})
		case p == prPathFor+"/threads":
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{
				{"comments": []map[string]any{
					{"content": "Faltou o teste\ndo CSV", "commentType": "text", "publishedDate": "2026-10-02T18:00:00Z", "author": map[string]any{"id": meID}},
					{"content": "Ok", "commentType": "text", "publishedDate": "2026-10-02T18:05:00Z", "author": map[string]any{"id": anaID}},
					{"content": "antigo", "commentType": "text", "publishedDate": "2026-09-30T18:00:00Z", "author": map[string]any{"id": meID}},
				}},
				{"properties": map[string]any{"CodeReviewThreadType": map[string]any{"$value": "RefUpdate"}},
					"comments": []map[string]any{{"content": "Sidiney updated", "commentType": "system", "publishedDate": "2026-10-02T17:00:00Z", "author": map[string]any{"id": meID}}}},
				{"properties": map[string]any{"CodeReviewThreadType": map[string]any{"$value": "ReviewersUpdate"}},
					"comments": []map[string]any{{"content": "added Ana", "commentType": "system", "publishedDate": "2026-10-02T13:01:00Z", "author": map[string]any{"id": meID}}}},
				{"isDeleted": true, "comments": []map[string]any{{"content": "apagado", "commentType": "text", "publishedDate": "2026-10-02T18:00:00Z", "author": map[string]any{"id": meID}}}},
			}})
		case p == "/Meu%20Projeto/_apis/git/repositories/"+repoID+"/pullRequests/8/threads":
			mcptest.WriteJSON(w, map[string]any{"value": []any{vote(meID, 10, "2026-10-02T15:30:00Z"), vote(anaID, -10, "2026-10-02T15:40:00Z")}})
		case p == "/Meu%20Projeto/_apis/git/repositories/"+repoID+"/pullRequests/9/threads":
			mcptest.WriteJSON(w, map[string]any{"value": []any{vote(meID, 5, "2026-09-29T15:30:00Z")}})
		case strings.HasSuffix(p, "/pullRequests/7/workitems"):
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{{"id": "4312"}}})
		case strings.HasSuffix(p, "/pullRequests/8/workitems"):
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{{"id": "4313"}}})
		case p == "/_apis/wit/workItems/4312/updates":
			mcptest.WriteJSON(w, map[string]any{"value": []map[string]any{
				{"rev": 1, "revisedBy": map[string]any{"id": anaID, "displayName": "Ana"},
					"fields": map[string]any{"System.ChangedDate": map[string]any{"newValue": "2026-10-01T12:00:00Z"}, "System.Title": map[string]any{"newValue": "Exportar CSV"}}},
				{"rev": 2, "revisedBy": map[string]any{"id": meID, "displayName": "Sidiney"},
					"fields": map[string]any{
						"System.ChangedDate":                    map[string]any{"oldValue": "2026-10-01T12:00:00Z", "newValue": "2026-10-02T14:00:00Z"},
						"System.Rev":                            map[string]any{"oldValue": 1, "newValue": 2},
						"System.State":                          map[string]any{"oldValue": "New", "newValue": "Active"},
						"System.AssignedTo":                     map[string]any{"newValue": map[string]any{"displayName": "Sidiney", "uniqueName": "sidiney@corp.com"}},
						"System.Description":                    map[string]any{"newValue": "<p>Um texto bem longo</p>"},
						"System.History":                        map[string]any{"newValue": "<div>Começando pelo <b>worker</b></div>"},
						"System.CommentCount":                   map[string]any{"oldValue": 0, "newValue": 1},
						"System.AuthorizedAs":                   map[string]any{"newValue": map[string]any{"displayName": "Sidiney"}},
						"Microsoft.VSTS.Foo.X":                  map[string]any{"oldValue": 1.5, "newValue": 2.0},
						"System.IterationLevel3":                map[string]any{"newValue": "Sprint 42"},
						"Microsoft.VSTS.Common.StateChangeDate": map[string]any{"newValue": "2026-10-02T14:00:00Z"},
						"System.Tags":                           map[string]any{},
					},
					"relations": map[string]any{"added": []map[string]any{{"rel": "ArtifactLink", "url": "vstfs:///Git/PullRequestId/x", "attributes": map[string]any{"name": "Pull Request"}}}}},
				{"rev": 3, "revisedBy": map[string]any{"id": meID, "displayName": "Sidiney"},
					"fields": map[string]any{
						"System.ChangedDate":                      map[string]any{"newValue": "2026-10-02T20:00:00Z"},
						"Microsoft.VSTS.Scheduling.CompletedWork": map[string]any{"oldValue": 0.0, "newValue": 1.5},
						"Microsoft.VSTS.Scheduling.RemainingWork": map[string]any{"oldValue": 2.0, "newValue": 0.5},
					}},
				{"rev": 4, "revisedBy": map[string]any{"id": meID, "displayName": "Sidiney"}, "revisedDate": "9999-01-01T00:00:00Z",
					"fields": map[string]any{"System.ChangedDate": map[string]any{"newValue": "2026-10-03T12:00:00Z"}, "System.State": map[string]any{"oldValue": "Active", "newValue": "PR"}}},
			}})
		default:
			http.NotFound(w, r)
		}
	}}
}

func TestListPushes(t *testing.T) {
	f := activityFake()
	s, _ := newReposServer(t, f, nil)
	var rep PushReport
	call(t, s, "list_pushes", map[string]any{"startDate": "2026-10-01", "endDate": "2026-10-02"}, &rep)

	if rep.Count != 2 || rep.StartDate != "2026-10-01" || rep.EndDate != "2026-10-02" {
		t.Fatalf("relatório = %+v", rep)
	}
	newest, older := rep.Pushes[0], rep.Pushes[1]
	if newest.ID != 41 || newest.Repository != "api" || newest.Branches[0] != "users/sidiney/4312-exportar-csv" || newest.Date != localTime("2026-10-02T21:11:34.5741964Z") {
		t.Errorf("push 41 = %+v", newest)
	}
	if len(newest.Commits) != maxPushCommits || !newest.MoreCommits || newest.Commits[0].CommitID != "0000000000" ||
		newest.Commits[0].Message != "feat: exportar CSV …" {
		t.Errorf("commits = %d %v %+v", len(newest.Commits), newest.MoreCommits, newest.Commits[0])
	}
	if older.ID != 40 || older.Branches[0] != "users/sidiney/velha (removida)" || len(older.Commits) != 0 {
		t.Errorf("push 40 = %+v", older)
	}
	// the disabled repository is not queried; the one without permission is reported
	if len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0], "web: ") || !strings.Contains(rep.Skipped[0], "GenericRead") {
		t.Errorf("skipped = %v", rep.Skipped)
	}
	if n := len(find(f, http.MethodGet, "/Meu%20Projeto/_apis/git/repositories/"+oldID+"/pushes")); n != 0 {
		t.Errorf("repositório desativado consultado %d vez(es)", n)
	}
	reqs := find(f, http.MethodGet, repoPath+"/pushes")
	if len(reqs) != 1 {
		t.Fatalf("pushes: %d requisições", len(reqs))
	}
	q, _ := url.ParseQuery(reqs[0].RawQuery)
	if q.Get("searchCriteria.pusherId") != meID || q.Get("searchCriteria.includeRefUpdates") != "true" ||
		!strings.HasSuffix(q.Get("searchCriteria.fromDate"), "Z") || q.Get("$top") != "50" {
		t.Errorf("query = %v", q)
	}

	// commits=false skips the commit requests
	before := len(find(f, http.MethodGet, repoPath+"/commits"))
	var one PushReport
	call(t, s, "list_pushes", map[string]any{"repository": "api", "startDate": "2026-10-02", "commits": false}, &one)
	if len(find(f, http.MethodGet, repoPath+"/commits")) != before || one.Count != 2 || one.Pushes[0].Commits != nil || one.Skipped != nil {
		t.Errorf("commits=false = %+v", one)
	}
}

func TestPullRequestActivity(t *testing.T) {
	f := activityFake()
	s, _ := newReposServer(t, f, nil)
	var rep PRActivityReport
	call(t, s, "list_pull_request_activity", map[string]any{"startDate": "2026-10-02", "endDate": "2026-10-02"}, &rep)

	if rep.Checked != 3 {
		t.Errorf("PRs verificadas = %d (a 7 aparece nas duas listas; a 5 é antiga demais)", rep.Checked)
	}
	if len(rep.PullRequests) != 2 {
		t.Fatalf("PRs com atividade = %+v", rep.PullRequests)
	}
	mine, reviewed := rep.PullRequests[0], rep.PullRequests[1]
	if mine.PullRequestID != 7 || len(mine.WorkItems) != 1 || mine.WorkItems[0] != 4312 {
		t.Errorf("PR 7 = %+v", mine)
	}
	var types []string
	for _, e := range mine.Events {
		types = append(types, e.Type)
	}
	if strings.Join(types, ",") != "created,push,comment" {
		t.Errorf("eventos da PR 7 = %v", types)
	}
	if c := mine.Events[2]; c.Text != "Faltou o teste …" || c.Date != localTime("2026-10-02T18:00:00Z") {
		t.Errorf("comentário = %+v", c)
	}
	if reviewed.PullRequestID != 8 || len(reviewed.Events) != 1 || reviewed.Events[0].Type != "vote" || reviewed.Events[0].Vote != "approved" ||
		reviewed.CreatedBy != "Ana" || reviewed.WorkItems[0] != 4313 {
		t.Errorf("PR 8 = %+v", reviewed)
	}
	if n := len(find(f, http.MethodGet, "/Meu%20Projeto/_apis/git/repositories/"+repoID+"/pullRequests/5/threads")); n != 0 {
		t.Errorf("PR antiga consultada %d vez(es)", n)
	}
	if n := len(find(f, http.MethodGet, "/Meu%20Projeto/_apis/git/repositories/"+repoID+"/pullRequests/9/workitems")); n != 0 {
		t.Error("work items buscados para PR sem atividade")
	}
	if tr := mcptest.CallTool(t, s, "list_pull_request_activity", map[string]any{"lookbackDays": 91}); !tr.IsError {
		t.Error("lookbackDays acima do máximo deveria falhar")
	}
}

func TestWorkItemUpdates(t *testing.T) {
	f := activityFake()
	s, _ := newAzServer(t, f, nil)
	var all WorkItemUpdates
	call(t, s, "get_work_item_updates", map[string]any{"id": 4312}, &all)
	if all.Total != 4 || all.Count != 4 || !all.Updates[0].Created || all.Updates[1].Created {
		t.Errorf("sem filtro = %d/%d %+v", all.Count, all.Total, all.Updates)
	}

	var mine WorkItemUpdates
	call(t, s, "get_work_item_updates", map[string]any{"id": 4312, "changedBy": "me", "startDate": "2026-10-02", "endDate": "2026-10-02"}, &mine)
	if mine.Count != 2 {
		t.Fatalf("minhas em 02/10 = %+v", mine.Updates)
	}
	u := mine.Updates[0]
	if u.Rev != 2 || u.By != "Sidiney" || u.Date != localTime("2026-10-02T14:00:00Z") || u.TimeTrackingOnly {
		t.Errorf("rev 2 = %+v", u)
	}
	if c := u.Changes["System.State"]; c.Old != "New" || c.New != "Active" {
		t.Errorf("estado = %+v", c)
	}
	if c := u.Changes["System.AssignedTo"]; c.New != "Sidiney" || c.Old != nil {
		t.Errorf("responsável = %+v", c)
	}
	if c := u.Changes["System.Description"]; c.New != "(texto, 18 caracteres)" {
		t.Errorf("descrição = %+v", c)
	}
	for _, meta := range []string{"System.ChangedDate", "System.Rev", "System.History", "System.CommentCount", "System.AuthorizedAs", "System.IterationLevel3", "Microsoft.VSTS.Common.StateChangeDate", "System.Tags"} {
		if _, ok := u.Changes[meta]; ok {
			t.Errorf("%s não deveria aparecer", meta)
		}
	}
	if u.Comment != "Começando pelo worker" || len(u.Linked) != 1 || u.Linked[0] != "Pull Request" {
		t.Errorf("comentário/links = %q %v", u.Comment, u.Linked)
	}
	if u := mine.Updates[1]; u.Rev != 3 || !u.TimeTrackingOnly {
		t.Errorf("rev 3 = %+v", u)
	}

	var ana WorkItemUpdates
	call(t, s, "get_work_item_updates", map[string]any{"id": 4312, "changedBy": "ana"}, &ana)
	if ana.Count != 1 || ana.Updates[0].Rev != 1 {
		t.Errorf("por nome = %+v", ana.Updates)
	}
}
