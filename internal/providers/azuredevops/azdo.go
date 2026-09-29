package azuredevops

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/sidiney/devpulse/internal/httpx"
)

// Azure DevOps (Boards) client: projects, teams, sprints, work items.
// PAT scopes needed: "Work Items (Read & write)" and "Project and Team (Read)".
// Deleting work items is intentionally not implemented.
type AzDO struct {
	c          *httpx.Client
	orgURL     string
	defProject string
	defTeam    string

	mu       sync.Mutex
	me       *AzIdentity
	defTeams map[string]string // project -> default team
}

const (
	azdoAPIVersion     = "7.1"
	azdoCommentsAPIVer = "7.1-preview.4"
	maxWorkItems       = 200
)

func NewAzDO(orgURL, pat, defProject, defTeam string, hc *http.Client) *AzDO {
	basic := base64.StdEncoding.EncodeToString([]byte(":" + pat))
	return &AzDO{
		c: &httpx.Client{
			HTTP:    hc,
			Root:    strings.TrimRight(orgURL, "/"),
			Auth:    "Basic " + basic,
			Secrets: []string{pat, basic},
			Name:    "Azure DevOps",
		},
		orgURL:     strings.TrimRight(orgURL, "/"),
		defProject: defProject,
		defTeam:    defTeam,
		defTeams:   map[string]string{},
	}
}

var pe = url.PathEscape

// ---------- identity / projects / teams ----------

type AzIdentity struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Account     string `json:"account"`
}

func (a *AzDO) Me(ctx context.Context) (*AzIdentity, error) {
	a.mu.Lock()
	if a.me != nil {
		m := a.me
		a.mu.Unlock()
		return m, nil
	}
	a.mu.Unlock()
	var cd struct {
		AuthenticatedUser struct {
			ID                  string `json:"id"`
			ProviderDisplayName string `json:"providerDisplayName"`
			Properties          struct {
				Account struct {
					Value string `json:"$value"`
				} `json:"Account"`
			} `json:"properties"`
		} `json:"authenticatedUser"`
	}
	if err := a.c.Do(ctx, http.MethodGet, "/_apis/connectionData?api-version=7.1-preview", nil, &cd); err != nil {
		return nil, err
	}
	m := &AzIdentity{
		ID:          cd.AuthenticatedUser.ID,
		DisplayName: cd.AuthenticatedUser.ProviderDisplayName,
		Account:     cd.AuthenticatedUser.Properties.Account.Value,
	}
	a.mu.Lock()
	a.me = m
	a.mu.Unlock()
	return m, nil
}

type AzProject struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func (a *AzDO) Projects(ctx context.Context) ([]AzProject, error) {
	var res struct {
		Value []AzProject `json:"value"`
	}
	if err := a.c.Do(ctx, http.MethodGet, "/_apis/projects?api-version="+azdoAPIVersion+"&$top=500", nil, &res); err != nil {
		return nil, err
	}
	sort.Slice(res.Value, func(i, j int) bool { return strings.ToLower(res.Value[i].Name) < strings.ToLower(res.Value[j].Name) })
	return res.Value, nil
}

type AzTeam struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IsDefault   bool   `json:"isDefault,omitempty"`
}

func (a *AzDO) Teams(ctx context.Context, project string) ([]AzTeam, error) {
	project, err := a.project(project)
	if err != nil {
		return nil, err
	}
	var res struct {
		Value []AzTeam `json:"value"`
	}
	if err := a.c.Do(ctx, http.MethodGet, "/_apis/projects/"+pe(project)+"/teams?api-version="+azdoAPIVersion+"&$top=500", nil, &res); err != nil {
		return nil, err
	}
	def, _ := a.defaultTeam(ctx, project)
	for i := range res.Value {
		res.Value[i].IsDefault = strings.EqualFold(res.Value[i].Name, def)
	}
	sort.Slice(res.Value, func(i, j int) bool { return strings.ToLower(res.Value[i].Name) < strings.ToLower(res.Value[j].Name) })
	return res.Value, nil
}

func (a *AzDO) project(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = a.defProject
	}
	if p == "" {
		return "", errors.New("informe o projeto (parâmetro project) ou defina AZURE_DEVOPS_PROJECT")
	}
	return p, nil
}

func (a *AzDO) defaultTeam(ctx context.Context, project string) (string, error) {
	a.mu.Lock()
	if t, ok := a.defTeams[project]; ok {
		a.mu.Unlock()
		return t, nil
	}
	a.mu.Unlock()
	var res struct {
		DefaultTeam struct {
			Name string `json:"name"`
		} `json:"defaultTeam"`
	}
	if err := a.c.Do(ctx, http.MethodGet, "/_apis/projects/"+pe(project)+"?api-version="+azdoAPIVersion, nil, &res); err != nil {
		return "", err
	}
	if res.DefaultTeam.Name == "" {
		return "", fmt.Errorf("projeto %q não tem time padrão; informe o parâmetro team", project)
	}
	a.mu.Lock()
	a.defTeams[project] = res.DefaultTeam.Name
	a.mu.Unlock()
	return res.DefaultTeam.Name, nil
}

func (a *AzDO) team(ctx context.Context, project, t string) (string, error) {
	t = strings.TrimSpace(t)
	if t != "" {
		return t, nil
	}
	if a.defTeam != "" && strings.EqualFold(project, a.defProject) {
		return a.defTeam, nil
	}
	return a.defaultTeam(ctx, project)
}

// ---------- sprints ----------

type Sprint struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	StartDate  string `json:"startDate,omitempty"`
	FinishDate string `json:"finishDate,omitempty"`
	TimeFrame  string `json:"timeFrame,omitempty"` // past | current | future
}

func (a *AzDO) Sprints(ctx context.Context, project, team, timeframe string) ([]Sprint, string, string, error) {
	project, err := a.project(project)
	if err != nil {
		return nil, "", "", err
	}
	team, err = a.team(ctx, project, team)
	if err != nil {
		return nil, "", "", err
	}
	var res struct {
		Value []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Path       string `json:"path"`
			Attributes struct {
				StartDate  *string `json:"startDate"`
				FinishDate *string `json:"finishDate"`
				TimeFrame  string  `json:"timeFrame"`
			} `json:"attributes"`
		} `json:"value"`
	}
	p := "/" + pe(project) + "/" + pe(team) + "/_apis/work/teamsettings/iterations?api-version=" + azdoAPIVersion
	if err := a.c.Do(ctx, http.MethodGet, p, nil, &res); err != nil {
		return nil, "", "", err
	}
	tf := strings.ToLower(strings.TrimSpace(timeframe))
	out := []Sprint{}
	for _, v := range res.Value {
		s := Sprint{ID: v.ID, Name: v.Name, Path: v.Path, TimeFrame: strings.ToLower(v.Attributes.TimeFrame)}
		if v.Attributes.StartDate != nil && len(*v.Attributes.StartDate) >= 10 {
			s.StartDate = (*v.Attributes.StartDate)[:10]
		}
		if v.Attributes.FinishDate != nil && len(*v.Attributes.FinishDate) >= 10 {
			s.FinishDate = (*v.Attributes.FinishDate)[:10]
		}
		if tf != "" && tf != "all" && s.TimeFrame != tf {
			continue
		}
		out = append(out, s)
	}
	return out, project, team, nil
}

// resolveSprint accepts "", "current", a sprint name or a full iteration path.
func (a *AzDO) resolveSprint(ctx context.Context, project, team, sel string) (*Sprint, string, string, error) {
	sprints, project, team, err := a.Sprints(ctx, project, team, "all")
	if err != nil {
		return nil, "", "", err
	}
	sel = strings.TrimSpace(sel)
	for i := range sprints {
		s := &sprints[i]
		if (sel == "" || strings.EqualFold(sel, "current")) && s.TimeFrame == "current" {
			return s, project, team, nil
		}
		if sel != "" && (strings.EqualFold(s.Name, sel) || strings.EqualFold(s.Path, sel)) {
			return s, project, team, nil
		}
	}
	if sel == "" || strings.EqualFold(sel, "current") {
		return nil, "", "", fmt.Errorf("o time %q não tem sprint atual configurada", team)
	}
	names := []string{}
	for _, s := range sprints {
		names = append(names, s.Name)
	}
	if len(names) > 15 {
		names = names[len(names)-15:]
	}
	return nil, "", "", fmt.Errorf("sprint %q não encontrada no time %q; opções: %s", sel, team, strings.Join(names, ", "))
}

// ---------- work items ----------

type WorkItem struct {
	ID               int      `json:"id"`
	Type             string   `json:"type"`
	Title            string   `json:"title"`
	State            string   `json:"state"`
	BoardColumn      string   `json:"boardColumn,omitempty"`
	AssignedTo       string   `json:"assignedTo,omitempty"`
	Project          string   `json:"project,omitempty"`
	IterationPath    string   `json:"iterationPath,omitempty"`
	AreaPath         string   `json:"areaPath,omitempty"`
	ParentID         *int     `json:"parentId,omitempty"`
	Priority         *float64 `json:"priority,omitempty"`
	StoryPoints      *float64 `json:"storyPoints,omitempty"`
	Effort           *float64 `json:"effort,omitempty"`
	OriginalEstimate *float64 `json:"originalEstimate,omitempty"`
	RemainingWork    *float64 `json:"remainingWork,omitempty"`
	CompletedWork    *float64 `json:"completedWork,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	ChangedDate      string   `json:"changedDate,omitempty"`
}

var summaryFields = []string{
	"System.Id", "System.WorkItemType", "System.Title", "System.State", "System.BoardColumn",
	"System.AssignedTo", "System.TeamProject", "System.IterationPath", "System.AreaPath",
	"System.Parent", "System.Tags", "System.ChangedDate", "Microsoft.VSTS.Common.Priority",
	"Microsoft.VSTS.Scheduling.StoryPoints", "Microsoft.VSTS.Scheduling.Effort",
	"Microsoft.VSTS.Scheduling.OriginalEstimate", "Microsoft.VSTS.Scheduling.RemainingWork",
	"Microsoft.VSTS.Scheduling.CompletedWork",
}

func workItemFromFields(id int, f map[string]any) WorkItem {
	wi := WorkItem{
		ID:               id,
		Type:             str(f["System.WorkItemType"]),
		Title:            str(f["System.Title"]),
		State:            str(f["System.State"]),
		BoardColumn:      str(f["System.BoardColumn"]),
		AssignedTo:       identityName(f["System.AssignedTo"]),
		Project:          str(f["System.TeamProject"]),
		IterationPath:    str(f["System.IterationPath"]),
		AreaPath:         str(f["System.AreaPath"]),
		Priority:         num(f["Microsoft.VSTS.Common.Priority"]),
		StoryPoints:      num(f["Microsoft.VSTS.Scheduling.StoryPoints"]),
		Effort:           num(f["Microsoft.VSTS.Scheduling.Effort"]),
		OriginalEstimate: num(f["Microsoft.VSTS.Scheduling.OriginalEstimate"]),
		RemainingWork:    num(f["Microsoft.VSTS.Scheduling.RemainingWork"]),
		CompletedWork:    num(f["Microsoft.VSTS.Scheduling.CompletedWork"]),
		ChangedDate:      str(f["System.ChangedDate"]),
		Tags:             splitTags(str(f["System.Tags"])),
	}
	if p := num(f["System.Parent"]); p != nil {
		v := int(*p)
		wi.ParentID = &v
	}
	return wi
}

// WorkItemsByID fetches summaries preserving the given order.
func (a *AzDO) WorkItemsByID(ctx context.Context, ids []int) ([]WorkItem, error) {
	byID := map[int]WorkItem{}
	for start := 0; start < len(ids); start += 200 {
		end := start + 200
		if end > len(ids) {
			end = len(ids)
		}
		body := map[string]any{"ids": ids[start:end], "fields": summaryFields, "errorPolicy": "omit"}
		var batch struct {
			Value []struct {
				ID     int            `json:"id"`
				Fields map[string]any `json:"fields"`
			} `json:"value"`
		}
		if err := a.c.Do(ctx, http.MethodPost, "/_apis/wit/workitemsbatch?api-version="+azdoAPIVersion, body, &batch); err != nil {
			return nil, err
		}
		for _, v := range batch.Value {
			if v.ID != 0 {
				byID[v.ID] = workItemFromFields(v.ID, v.Fields)
			}
		}
	}
	out := make([]WorkItem, 0, len(ids))
	for _, id := range ids {
		if wi, ok := byID[id]; ok {
			out = append(out, wi)
		}
	}
	return out, nil
}

// runWIQL executes a (read-only) WIQL query and returns up to top ids.
func (a *AzDO) runWIQL(ctx context.Context, project, team, query string, top int) ([]int, error) {
	prefix := ""
	if project != "" {
		prefix = "/" + pe(project)
		if team != "" {
			prefix += "/" + pe(team)
		}
	}
	var res struct {
		WorkItems []struct {
			ID int `json:"id"`
		} `json:"workItems"`
		WorkItemRelations []struct {
			Target *struct {
				ID int `json:"id"`
			} `json:"target"`
		} `json:"workItemRelations"`
	}
	p := prefix + "/_apis/wit/wiql?api-version=" + azdoAPIVersion + "&$top=" + strconv.Itoa(top)
	if err := a.c.Do(ctx, http.MethodPost, p, map[string]string{"query": query}, &res); err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	ids := []int{}
	add := func(id int) {
		if id != 0 && !seen[id] && len(ids) < top {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, w := range res.WorkItems {
		add(w.ID)
	}
	for _, r := range res.WorkItemRelations {
		if r.Target != nil {
			add(r.Target.ID)
		}
	}
	return ids, nil
}

type WorkItemFilter struct {
	Project       string
	Team          string
	Types         []string
	States        []string
	AssignedTo    string // "me", an e-mail/name, or "" for anyone
	Sprint        string // "current", sprint name or iteration path
	AreaPath      string
	ParentID      int
	TitleContains string
	Tag           string
	IncludeClosed bool
	Top           int
	WIQL          string // raw query; overrides the other filters
}

type QueryResult struct {
	Project string     `json:"project"`
	Sprint  *Sprint    `json:"sprint,omitempty"`
	WIQL    string     `json:"wiql"`
	Count   int        `json:"count"`
	Items   []WorkItem `json:"items"`
}

var closedStates = "('Closed', 'Removed', 'Done')"

func (a *AzDO) Query(ctx context.Context, f WorkItemFilter) (*QueryResult, error) {
	if f.Top <= 0 || f.Top > maxWorkItems {
		f.Top = 100
	}
	project := strings.TrimSpace(f.Project)
	if project == "" {
		project = a.defProject
	}
	res := &QueryResult{Project: project}

	if q := strings.TrimSpace(f.WIQL); q != "" {
		if !strings.HasPrefix(strings.ToUpper(q), "SELECT") {
			return nil, errors.New("wiql deve começar com SELECT")
		}
		team := ""
		if project != "" && strings.Contains(strings.ToLower(q), "@currentiteration") {
			team, _ = a.team(ctx, project, f.Team)
		}
		ids, err := a.runWIQL(ctx, project, team, q, f.Top)
		if err != nil {
			return nil, err
		}
		res.WIQL = q
		res.Items, err = a.WorkItemsByID(ctx, ids)
		res.Count = len(res.Items)
		return res, err
	}

	where := []string{}
	if project != "" {
		where = append(where, "[System.TeamProject] = "+wiqlQuote(project))
	}
	if len(f.Types) > 0 {
		where = append(where, "[System.WorkItemType] IN ("+quoteList(f.Types)+")")
	}
	if len(f.States) > 0 {
		where = append(where, "[System.State] IN ("+quoteList(f.States)+")")
	} else if !f.IncludeClosed {
		where = append(where, "[System.State] NOT IN "+closedStates)
	}
	switch who := strings.TrimSpace(f.AssignedTo); {
	case who == "":
	case strings.EqualFold(who, "me") || strings.EqualFold(who, "@me"):
		where = append(where, "[System.AssignedTo] = @Me")
	case strings.EqualFold(who, "unassigned") || strings.EqualFold(who, "none"):
		where = append(where, "[System.AssignedTo] = ''")
	default:
		where = append(where, "[System.AssignedTo] = "+wiqlQuote(who))
	}
	if s := strings.TrimSpace(f.Sprint); s != "" {
		path := s
		if !strings.Contains(s, `\`) {
			if project == "" {
				return nil, errors.New("para filtrar por sprint informe o projeto")
			}
			sp, _, _, err := a.resolveSprint(ctx, project, f.Team, s)
			if err != nil {
				return nil, err
			}
			res.Sprint = sp
			path = sp.Path
		}
		where = append(where, "[System.IterationPath] = "+wiqlQuote(path))
	}
	if ap := strings.TrimSpace(f.AreaPath); ap != "" {
		where = append(where, "[System.AreaPath] UNDER "+wiqlQuote(ap))
	}
	if f.ParentID > 0 {
		where = append(where, "[System.Parent] = "+strconv.Itoa(f.ParentID))
	}
	if t := strings.TrimSpace(f.TitleContains); t != "" {
		where = append(where, "[System.Title] CONTAINS "+wiqlQuote(t))
	}
	if t := strings.TrimSpace(f.Tag); t != "" {
		where = append(where, "[System.Tags] CONTAINS "+wiqlQuote(t))
	}
	if len(where) == 0 {
		return nil, errors.New("informe ao menos um filtro (ex.: project, assignedTo)")
	}
	q := "SELECT [System.Id] FROM WorkItems WHERE " + strings.Join(where, " AND ") +
		" ORDER BY [Microsoft.VSTS.Common.BacklogPriority] ASC, [System.ChangedDate] DESC"
	res.WIQL = q
	ids, err := a.runWIQL(ctx, project, "", q, f.Top)
	if err != nil {
		return nil, err
	}
	res.Items, err = a.WorkItemsByID(ctx, ids)
	res.Count = len(res.Items)
	return res, err
}

// ---------- sprint board ----------

type BoardColumn struct {
	Column        string     `json:"column"`
	Count         int        `json:"count"`
	RemainingWork float64    `json:"remainingWork,omitempty"`
	Items         []WorkItem `json:"items"`
}

type SprintBoard struct {
	Project       string         `json:"project"`
	Team          string         `json:"team"`
	Sprint        Sprint         `json:"sprint"`
	TotalItems    int            `json:"totalItems"`
	RemainingWork float64        `json:"remainingWork"`
	ByAssignee    map[string]int `json:"itemsByAssignee"`
	Columns       []BoardColumn  `json:"columns"`
}

var stateOrder = map[string]int{
	"new": 0, "proposed": 0, "to do": 1, "approved": 1, "active": 2, "committed": 2,
	"in progress": 2, "doing": 2, "resolved": 3, "testing": 3, "done": 4, "closed": 4, "removed": 5,
}

func (a *AzDO) SprintBoard(ctx context.Context, project, team, sprint string, types []string, assignedTo string) (*SprintBoard, error) {
	sp, project, team, err := a.resolveSprint(ctx, project, team, sprint)
	if err != nil {
		return nil, err
	}
	qr, err := a.Query(ctx, WorkItemFilter{
		Project: project, Team: team, Sprint: sp.Path, Types: types,
		AssignedTo: assignedTo, IncludeClosed: true, Top: maxWorkItems,
	})
	if err != nil {
		return nil, err
	}
	b := &SprintBoard{Project: project, Team: team, Sprint: *sp, ByAssignee: map[string]int{}}
	cols := map[string]*BoardColumn{}
	order := []string{}
	for _, wi := range qr.Items {
		if strings.EqualFold(wi.State, "Removed") {
			continue
		}
		col := wi.State // the sprint taskboard is state-based
		if wi.Type != "Task" && wi.BoardColumn != "" {
			col = wi.BoardColumn // backlog items: use the Kanban column
		}
		c, ok := cols[col]
		if !ok {
			c = &BoardColumn{Column: col, Items: []WorkItem{}}
			cols[col] = c
			order = append(order, col)
		}
		c.Items = append(c.Items, wi)
		c.Count++
		if wi.RemainingWork != nil {
			c.RemainingWork += *wi.RemainingWork
			b.RemainingWork += *wi.RemainingWork
		}
		who := wi.AssignedTo
		if who == "" {
			who = "(sem responsável)"
		}
		b.ByAssignee[who]++
		b.TotalItems++
	}
	sort.SliceStable(order, func(i, j int) bool {
		oi, iok := stateOrder[strings.ToLower(order[i])]
		oj, jok := stateOrder[strings.ToLower(order[j])]
		if !iok {
			oi = 3
		}
		if !jok {
			oj = 3
		}
		return oi < oj
	})
	for _, k := range order {
		b.Columns = append(b.Columns, *cols[k])
	}
	return b, nil
}

// ---------- single work item ----------

type WorkItemLink struct {
	Relation string `json:"relation"`
	ID       int    `json:"id,omitempty"`
	URL      string `json:"url,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

type WorkItemComment struct {
	Author string `json:"author"`
	Date   string `json:"date"`
	Text   string `json:"text"`
}

type WorkItemDetail struct {
	WorkItem
	Rev                int               `json:"rev"`
	WebURL             string            `json:"webUrl,omitempty"`
	Description        string            `json:"description,omitempty"`
	AcceptanceCriteria string            `json:"acceptanceCriteria,omitempty"`
	ReproSteps         string            `json:"reproSteps,omitempty"`
	Parent             *WorkItem         `json:"parent,omitempty"`
	Children           []WorkItem        `json:"children,omitempty"`
	OtherLinks         []WorkItemLink    `json:"otherLinks,omitempty"`
	Comments           []WorkItemComment `json:"comments,omitempty"`
	AllFields          map[string]any    `json:"allFields,omitempty"`
}

type rawRelation struct {
	Rel        string         `json:"rel"`
	URL        string         `json:"url"`
	Attributes map[string]any `json:"attributes"`
}

type rawWorkItem struct {
	ID        int            `json:"id"`
	Rev       int            `json:"rev"`
	Fields    map[string]any `json:"fields"`
	Relations []rawRelation  `json:"relations"`
	Links     struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"_links"`
}

func (a *AzDO) rawWorkItem(ctx context.Context, id int) (*rawWorkItem, error) {
	if id <= 0 {
		return nil, errors.New("id inválido")
	}
	var w rawWorkItem
	if err := a.c.Do(ctx, http.MethodGet, "/_apis/wit/workitems/"+strconv.Itoa(id)+"?$expand=relations&api-version="+azdoAPIVersion, nil, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

func (a *AzDO) WorkItemDetail(ctx context.Context, id int, withComments, allFields bool) (*WorkItemDetail, error) {
	w, err := a.rawWorkItem(ctx, id)
	if err != nil {
		return nil, err
	}
	d := &WorkItemDetail{
		WorkItem:           workItemFromFields(w.ID, w.Fields),
		Rev:                w.Rev,
		WebURL:             w.Links.HTML.Href,
		Description:        htmlToText(str(w.Fields["System.Description"])),
		AcceptanceCriteria: htmlToText(str(w.Fields["Microsoft.VSTS.Common.AcceptanceCriteria"])),
		ReproSteps:         htmlToText(str(w.Fields["Microsoft.VSTS.TCM.ReproSteps"])),
	}
	if allFields {
		d.AllFields = w.Fields
	}
	var childIDs []int
	var parentID int
	for _, r := range w.Relations {
		rid := idFromURL(r.URL)
		switch r.Rel {
		case "System.LinkTypes.Hierarchy-Reverse":
			parentID = rid
		case "System.LinkTypes.Hierarchy-Forward":
			childIDs = append(childIDs, rid)
		default:
			name := r.Rel
			if n, ok := r.Attributes["name"].(string); ok && n != "" {
				name = n
			}
			l := WorkItemLink{Relation: name, ID: rid, Comment: str(r.Attributes["comment"])}
			if rid == 0 {
				l.URL = r.URL
			}
			d.OtherLinks = append(d.OtherLinks, l)
		}
	}
	ids := append([]int{}, childIDs...)
	if parentID != 0 {
		ids = append(ids, parentID)
	}
	if len(ids) > 0 {
		items, err := a.WorkItemsByID(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			it := it
			if it.ID == parentID {
				d.Parent = &it
			} else {
				d.Children = append(d.Children, it)
			}
		}
	}
	if withComments && d.Project != "" {
		var cr struct {
			Comments []struct {
				Text      string `json:"text"`
				CreatedBy struct {
					DisplayName string `json:"displayName"`
				} `json:"createdBy"`
				CreatedDate string `json:"createdDate"`
			} `json:"comments"`
		}
		p := "/" + pe(d.Project) + "/_apis/wit/workItems/" + strconv.Itoa(id) + "/comments?api-version=" + azdoCommentsAPIVer + "&$top=30&order=desc"
		if err := a.c.Do(ctx, http.MethodGet, p, nil, &cr); err == nil {
			for _, c := range cr.Comments {
				date := c.CreatedDate
				if len(date) > 16 {
					date = strings.Replace(date[:16], "T", " ", 1)
				}
				d.Comments = append(d.Comments, WorkItemComment{Author: c.CreatedBy.DisplayName, Date: date, Text: htmlToText(c.Text)})
			}
		}
	}
	return d, nil
}

// ---------- create / update ----------

type patchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// WorkItemChanges describes fields to set. nil pointer = leave unchanged.
type WorkItemChanges struct {
	Title              *string
	State              *string
	Reason             *string
	AssignedTo         *string // "me", e-mail, display name or "" to unassign
	Sprint             *string // "current", sprint name or iteration path
	Team               string  // used to resolve Sprint
	AreaPath           *string
	Description        *string // plain text (converted to HTML)
	AcceptanceCriteria *string
	Priority           *int
	StoryPoints        *float64
	Effort             *float64
	OriginalEstimate   *float64
	RemainingWork      *float64
	CompletedWork      *float64
	Tags               *[]string // replaces all tags
	AddTags            []string
	RemoveTags         []string
	ParentID           *int // 0 removes the parent
	Fields             map[string]any
}

var fieldRefRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)

var readOnlyFields = map[string]bool{
	"System.Id": true, "System.Rev": true, "System.CreatedDate": true, "System.CreatedBy": true,
	"System.ChangedDate": true, "System.ChangedBy": true, "System.TeamProject": true,
	"System.WorkItemType": true, "System.Parent": true, "System.BoardColumn": true,
}

func (a *AzDO) buildOps(ctx context.Context, project string, ch WorkItemChanges, current *rawWorkItem) ([]patchOp, error) {
	var ops []patchOp
	set := func(field string, v any) {
		ops = append(ops, patchOp{Op: "add", Path: "/fields/" + field, Value: v})
	}
	if current != nil {
		ops = append(ops, patchOp{Op: "test", Path: "/rev", Value: current.Rev})
	}
	if ch.Title != nil {
		t := strings.TrimSpace(*ch.Title)
		if t == "" || len(t) > 255 {
			return nil, errors.New("title deve ter entre 1 e 255 caracteres")
		}
		set("System.Title", t)
	}
	if ch.State != nil {
		set("System.State", strings.TrimSpace(*ch.State))
	}
	if ch.Reason != nil {
		set("System.Reason", strings.TrimSpace(*ch.Reason))
	}
	if ch.AssignedTo != nil {
		who := strings.TrimSpace(*ch.AssignedTo)
		if strings.EqualFold(who, "me") || strings.EqualFold(who, "@me") {
			me, err := a.Me(ctx)
			if err != nil {
				return nil, err
			}
			who = me.Account
			if who == "" {
				who = me.DisplayName
			}
		}
		set("System.AssignedTo", who)
	}
	if ch.Sprint != nil {
		s := strings.TrimSpace(*ch.Sprint)
		path := s
		if s != "" && !strings.Contains(s, `\`) {
			sp, _, _, err := a.resolveSprint(ctx, project, ch.Team, s)
			if err != nil {
				return nil, err
			}
			path = sp.Path
		}
		if path == "" {
			path = project // backlog root
		}
		set("System.IterationPath", path)
	}
	if ch.AreaPath != nil {
		set("System.AreaPath", strings.TrimSpace(*ch.AreaPath))
	}
	if ch.Description != nil {
		set("System.Description", textToHTML(*ch.Description))
	}
	if ch.AcceptanceCriteria != nil {
		set("Microsoft.VSTS.Common.AcceptanceCriteria", textToHTML(*ch.AcceptanceCriteria))
	}
	if ch.Priority != nil {
		if *ch.Priority < 1 || *ch.Priority > 4 {
			return nil, errors.New("priority deve ser de 1 a 4")
		}
		set("Microsoft.VSTS.Common.Priority", *ch.Priority)
	}
	for _, n := range []struct {
		v     *float64
		field string
	}{
		{ch.StoryPoints, "Microsoft.VSTS.Scheduling.StoryPoints"},
		{ch.Effort, "Microsoft.VSTS.Scheduling.Effort"},
		{ch.OriginalEstimate, "Microsoft.VSTS.Scheduling.OriginalEstimate"},
		{ch.RemainingWork, "Microsoft.VSTS.Scheduling.RemainingWork"},
		{ch.CompletedWork, "Microsoft.VSTS.Scheduling.CompletedWork"},
	} {
		if n.v != nil {
			if *n.v < 0 || *n.v > 10000 {
				return nil, fmt.Errorf("valor fora do intervalo para %s", n.field)
			}
			set(n.field, *n.v)
		}
	}
	if ch.Tags != nil || len(ch.AddTags) > 0 || len(ch.RemoveTags) > 0 {
		var tags []string
		if ch.Tags != nil {
			tags = *ch.Tags
		} else if current != nil {
			tags = splitTags(str(current.Fields["System.Tags"]))
		}
		tags = mergeTags(tags, ch.AddTags, ch.RemoveTags)
		set("System.Tags", strings.Join(tags, "; "))
	}
	for k, v := range ch.Fields {
		if !fieldRefRe.MatchString(k) {
			return nil, fmt.Errorf("nome de campo inválido: %q (use o reference name, ex.: Custom.MeuCampo)", k)
		}
		if readOnlyFields[k] {
			return nil, fmt.Errorf("o campo %s não pode ser alterado por aqui", k)
		}
		set(k, v)
	}
	if ch.ParentID != nil {
		if current != nil {
			for i, r := range current.Relations {
				if r.Rel == "System.LinkTypes.Hierarchy-Reverse" {
					if idFromURL(r.URL) == *ch.ParentID {
						return ops, nil // already the parent; nothing to do for relations
					}
					ops = append(ops, patchOp{Op: "remove", Path: "/relations/" + strconv.Itoa(i)})
				}
			}
		}
		if *ch.ParentID > 0 {
			ops = append(ops, patchOp{Op: "add", Path: "/relations/-", Value: map[string]any{
				"rel": "System.LinkTypes.Hierarchy-Reverse",
				"url": a.orgURL + "/_apis/wit/workItems/" + strconv.Itoa(*ch.ParentID),
			}})
		}
	}
	return ops, nil
}

type WriteResult struct {
	DryRun   bool      `json:"dryRun"`
	Message  string    `json:"message"`
	WorkItem *WorkItem `json:"workItem,omitempty"`
	WebURL   string    `json:"webUrl,omitempty"`
}

var workItemTypeRe = regexp.MustCompile(`^[\p{L}\p{N} _\-]{1,64}$`)

func (a *AzDO) CreateWorkItem(ctx context.Context, project, wiType string, ch WorkItemChanges, dryRun bool) (*WriteResult, error) {
	project, err := a.project(project)
	if err != nil {
		return nil, err
	}
	wiType = strings.TrimSpace(wiType)
	if !workItemTypeRe.MatchString(wiType) {
		return nil, fmt.Errorf("tipo de work item inválido: %q", wiType)
	}
	if ch.Title == nil {
		return nil, errors.New("title é obrigatório")
	}
	ops, err := a.buildOps(ctx, project, ch, nil)
	if err != nil {
		return nil, err
	}
	p := "/" + pe(project) + "/_apis/wit/workitems/" + pe("$"+wiType) + "?api-version=" + azdoAPIVersion
	if dryRun {
		p += "&validateOnly=true"
	}
	var w rawWorkItem
	if err := a.c.DoCT(ctx, http.MethodPost, p, "application/json-patch+json", ops, &w); err != nil {
		return nil, err
	}
	res := &WriteResult{DryRun: dryRun}
	if dryRun {
		res.Message = "validação OK — nada foi criado"
		return res, nil
	}
	wi := workItemFromFields(w.ID, w.Fields)
	res.WorkItem, res.WebURL = &wi, w.Links.HTML.Href
	res.Message = fmt.Sprintf("%s #%d criado", wi.Type, wi.ID)
	return res, nil
}

func (a *AzDO) UpdateWorkItem(ctx context.Context, id int, ch WorkItemChanges, dryRun bool) (*WriteResult, error) {
	cur, err := a.rawWorkItem(ctx, id)
	if err != nil {
		return nil, err
	}
	project := str(cur.Fields["System.TeamProject"])
	ops, err := a.buildOps(ctx, project, ch, cur)
	if err != nil {
		return nil, err
	}
	if len(ops) <= 1 { // only the rev test
		return nil, errors.New("nenhuma alteração informada")
	}
	p := "/_apis/wit/workitems/" + strconv.Itoa(id) + "?api-version=" + azdoAPIVersion
	if dryRun {
		p += "&validateOnly=true"
	}
	var w rawWorkItem
	if err := a.c.DoCT(ctx, http.MethodPatch, p, "application/json-patch+json", ops, &w); err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Status == http.StatusPreconditionFailed {
			return nil, fmt.Errorf("o item #%d foi alterado por outra pessoa enquanto isso; consulte de novo e repita", id)
		}
		return nil, err
	}
	res := &WriteResult{DryRun: dryRun}
	if dryRun {
		res.Message = "validação OK — nada foi alterado"
		return res, nil
	}
	wi := workItemFromFields(w.ID, w.Fields)
	res.WorkItem, res.WebURL = &wi, w.Links.HTML.Href
	res.Message = fmt.Sprintf("#%d atualizado (rev %d)", id, w.Rev)
	return res, nil
}

func (a *AzDO) AddComment(ctx context.Context, id int, text string) (*WriteResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("text é obrigatório")
	}
	if len(text) > 20000 {
		return nil, errors.New("comentário longo demais")
	}
	var w rawWorkItem
	if err := a.c.Do(ctx, http.MethodGet, "/_apis/wit/workitems/"+strconv.Itoa(id)+"?fields=System.TeamProject&api-version="+azdoAPIVersion, nil, &w); err != nil {
		return nil, err
	}
	project := str(w.Fields["System.TeamProject"])
	if project == "" {
		return nil, fmt.Errorf("não foi possível descobrir o projeto do item #%d", id)
	}
	p := "/" + pe(project) + "/_apis/wit/workItems/" + strconv.Itoa(id) + "/comments?api-version=" + azdoCommentsAPIVer
	if err := a.c.Do(ctx, http.MethodPost, p, map[string]string{"text": textToHTML(text)}, nil); err != nil {
		return nil, err
	}
	return &WriteResult{Message: fmt.Sprintf("comentário adicionado ao #%d", id)}, nil
}

// ---------- helpers ----------

// wiqlQuote produces a safe WIQL string literal: no control characters,
// single quotes doubled.
func wiqlQuote(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(v))
	if len(v) > 256 {
		v = v[:256]
	}
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

func quoteList(vs []string) string {
	q := make([]string, 0, len(vs))
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			q = append(q, wiqlQuote(v))
		}
	}
	return strings.Join(q, ", ")
}

func identityName(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if s := str(t["displayName"]); s != "" {
			return s
		}
		return str(t["uniqueName"])
	}
	return ""
}

func idFromURL(u string) int {
	i := strings.LastIndexByte(u, '/')
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(u[i+1:])
	if err != nil {
		return 0
	}
	return n
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ";") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func mergeTags(base, add, remove []string) []string {
	out := []string{}
	seen := map[string]bool{}
	rm := map[string]bool{}
	for _, r := range remove {
		rm[strings.ToLower(strings.TrimSpace(r))] = true
	}
	for _, t := range append(append([]string{}, base...), add...) {
		t = strings.TrimSpace(t)
		k := strings.ToLower(t)
		if t == "" || seen[k] || rm[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
	}
	return out
}

var (
	tagBreakRe = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li|/h[1-6]|/tr)\s*/?>`)
	liRe       = regexp.MustCompile(`(?i)<\s*li[^>]*>`)
	tagRe      = regexp.MustCompile(`<[^>]*>`)
	blankRe    = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(s string) string {
	if s == "" {
		return ""
	}
	s = tagBreakRe.ReplaceAllString(s, "\n")
	s = liRe.ReplaceAllString(s, "- ")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	s = blankRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return httpx.Truncate(strings.TrimSpace(s), 6000)
}

func textToHTML(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return ""
	}
	paras := strings.Split(s, "\n\n")
	var b strings.Builder
	for _, p := range paras {
		lines := strings.Split(p, "\n")
		allBullets := true
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if !(strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")) {
				allBullets = false
				break
			}
		}
		if allBullets {
			b.WriteString("<ul>")
			for _, l := range lines {
				b.WriteString("<li>" + html.EscapeString(strings.TrimSpace(strings.TrimSpace(l)[2:])) + "</li>")
			}
			b.WriteString("</ul>")
			continue
		}
		esc := make([]string, len(lines))
		for i, l := range lines {
			esc[i] = html.EscapeString(l)
		}
		b.WriteString("<div>" + strings.Join(esc, "<br>") + "</div>")
	}
	return b.String()
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func num(v any) *float64 {
	if f, ok := v.(float64); ok {
		return &f
	}
	return nil
}
