package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// What a person did on the server in a period, as evidence for the time to
// log: pushes (with their branches and commits), pull request events
// (created, votes, comments, new commits) and work item revisions. All
// read-only. Dates come in and go out as the user's local time: Azure
// DevOps answers in UTC, which turns an evening into the next day.

const (
	maxActivityDays  = 31
	maxPushCommits   = 20
	parallelRequests = 6
)

// period turns startDate/endDate (calendar dates) into the local instants
// [from, to). Without dates it is today; with only startDate, startDate
// until today; with only endDate, that single day.
func period(start, end string) (time.Time, time.Time, error) {
	start, end = strings.TrimSpace(start), strings.TrimSpace(end)
	if start == "" && end != "" {
		start = end
	}
	f, t, err := dateRange(start, end, today(), today(), maxActivityDays-1)
	if err != nil {
		return f, t, err
	}
	from := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, time.Local)
	to := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 1)
	return from, to, nil
}

// azTime parses an Azure DevOps timestamp (RFC 3339, UTC).
func azTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// localTime rewrites an Azure DevOps timestamp in local time.
func localTime(s string) string {
	if t, ok := azTime(s); ok {
		return t.In(time.Local).Format("2006-01-02T15:04:05-07:00")
	}
	return s
}

func within(s string, from, to time.Time) bool {
	t, ok := azTime(s)
	return ok && !t.Before(from) && t.Before(to)
}

// clip returns the first line of s, at most n runes.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " …"
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// parallel runs fn(0..n-1) with at most parallelRequests at a time.
func parallel(n int, fn func(i int)) {
	sem := make(chan struct{}, parallelRequests)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

func periodLabel(from, to time.Time) (string, string) {
	return from.Format(dateLayout), to.AddDate(0, 0, -1).Format(dateLayout)
}

// ---------- pushes ----------

type PushCommit struct {
	CommitID string `json:"commitId"`
	Date     string `json:"date"` // author date
	Message  string `json:"message"`
}

type Push struct {
	ID          int          `json:"id"`
	Date        string       `json:"date"`
	Project     string       `json:"project"`
	Repository  string       `json:"repository"`
	PushedBy    string       `json:"pushedBy"`
	Branches    []string     `json:"branches"`
	Commits     []PushCommit `json:"commits,omitempty"`
	MoreCommits bool         `json:"moreCommits,omitempty"`

	repoID string
}

type PushFilter struct {
	Project, Repository string
	PushedBy            string // "me" (default), e-mail, name or identity ID
	StartDate, EndDate  string
	Top                 int
	Commits             bool
}

type PushReport struct {
	StartDate string   `json:"startDate"`
	EndDate   string   `json:"endDate"`
	Count     int      `json:"count"`
	Truncated bool     `json:"truncated,omitempty"`
	Pushes    []Push   `json:"pushes"`
	Skipped   []string `json:"skipped,omitempty"`
}

// Pushes lists the pushes of one person in a period, in one repository or
// in every enabled repository of the project. Pushes (not the commit
// history) are what show work on branches that were not merged yet.
func (a *AzDO) Pushes(ctx context.Context, f PushFilter) (*PushReport, error) {
	from, to, err := period(f.StartDate, f.EndDate)
	if err != nil {
		return nil, err
	}
	who := f.PushedBy
	if strings.TrimSpace(who) == "" {
		who = "me"
	}
	pusher, err := a.identityID(ctx, who)
	if err != nil {
		return nil, err
	}
	top := f.Top
	if top <= 0 {
		top = 50
	}
	if top > 200 {
		top = 200
	}

	var repos []Repository
	if strings.TrimSpace(f.Repository) != "" {
		r, err := a.Repository(ctx, f.Project, f.Repository)
		if err != nil {
			return nil, err
		}
		repos = []Repository{*r}
	} else {
		all, _, err := a.Repositories(ctx, f.Project)
		if err != nil {
			return nil, err
		}
		for _, r := range all {
			if !r.IsDisabled {
				repos = append(repos, r)
			}
		}
	}

	q := url.Values{}
	q.Set("searchCriteria.pusherId", pusher)
	q.Set("searchCriteria.fromDate", from.UTC().Format(time.RFC3339))
	q.Set("searchCriteria.toDate", to.UTC().Format(time.RFC3339))
	q.Set("searchCriteria.includeRefUpdates", "true")
	q.Set("$top", strconv.Itoa(top))
	q.Set("api-version", azdoAPIVersion)

	var mu sync.Mutex
	var pushes []Push
	var skipped []string
	parallel(len(repos), func(i int) {
		r := repos[i]
		var res struct {
			Value []struct {
				PushID   int    `json:"pushId"`
				Date     string `json:"date"`
				PushedBy struct {
					DisplayName string `json:"displayName"`
				} `json:"pushedBy"`
				RefUpdates []struct {
					Name        string `json:"name"`
					NewObjectID string `json:"newObjectId"`
				} `json:"refUpdates"`
			} `json:"value"`
		}
		err := a.gitDo(ctx, http.MethodGet, "/"+pe(r.Project)+"/_apis/git/repositories/"+pe(r.ID)+"/pushes?"+q.Encode(), nil, &res)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			skipped = append(skipped, r.Name+": "+err.Error())
			return
		}
		for _, p := range res.Value {
			if !within(p.Date, from, to) {
				continue
			}
			x := Push{ID: p.PushID, Date: p.Date, Project: r.Project, Repository: r.Name, PushedBy: p.PushedBy.DisplayName, Branches: []string{}, repoID: r.ID}
			for _, u := range p.RefUpdates {
				if strings.HasPrefix(u.Name, "refs/pull/") {
					continue // merge refs Azure DevOps keeps for each pull request
				}
				b := shortBranch(u.Name)
				if u.NewObjectID == zeroObjectID {
					b += " (removida)"
				}
				x.Branches = append(x.Branches, b)
			}
			if len(x.Branches) > 0 || len(p.RefUpdates) == 0 {
				pushes = append(pushes, x)
			}
		}
	})

	sort.Slice(pushes, func(i, j int) bool {
		ti, _ := azTime(pushes[i].Date)
		tj, _ := azTime(pushes[j].Date)
		return ti.After(tj)
	})
	rep := &PushReport{Skipped: skipped}
	rep.StartDate, rep.EndDate = periodLabel(from, to)
	if len(pushes) > top {
		pushes, rep.Truncated = pushes[:top], true
	}

	if f.Commits {
		parallel(len(pushes), func(i int) {
			p := &pushes[i]
			var res struct {
				Value []struct {
					CommitID string `json:"commitId"`
					Comment  string `json:"comment"`
					Author   struct {
						Date string `json:"date"`
					} `json:"author"`
				} `json:"value"`
			}
			path := "/" + pe(p.Project) + "/_apis/git/repositories/" + pe(p.repoID) + "/commits?pushId=" + strconv.Itoa(p.ID) +
				"&top=" + strconv.Itoa(maxPushCommits+1) + "&api-version=" + azdoAPIVersion
			if a.gitDo(ctx, http.MethodGet, path, nil, &res) != nil {
				return // best effort: the push itself is the evidence
			}
			for j, c := range res.Value {
				if j == maxPushCommits {
					p.MoreCommits = true
					break
				}
				id := c.CommitID
				if len(id) > 10 {
					id = id[:10]
				}
				p.Commits = append(p.Commits, PushCommit{CommitID: id, Date: localTime(c.Author.Date), Message: clip(c.Comment, 200)})
			}
		})
	}
	for i := range pushes {
		pushes[i].Date = localTime(pushes[i].Date)
	}
	rep.Pushes, rep.Count = pushes, len(pushes)
	if rep.Pushes == nil {
		rep.Pushes = []Push{}
	}
	return rep, nil
}

// ---------- pull request activity ----------

type PREvent struct {
	Date string `json:"date"`
	Type string `json:"type"` // created, vote, comment, push, status
	Vote string `json:"vote,omitempty"`
	Text string `json:"text,omitempty"`
}

type PRActivity struct {
	PullRequestID int       `json:"pullRequestId"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	Project       string    `json:"project"`
	Repository    string    `json:"repository"`
	SourceBranch  string    `json:"sourceBranch"`
	CreatedBy     string    `json:"createdBy"`
	WorkItems     []int     `json:"workItems,omitempty"`
	WebURL        string    `json:"webUrl"`
	Events        []PREvent `json:"events"`

	raw rawPR
}

type PRActivityFilter struct {
	Project            string
	User               string // "me" (default), e-mail, name or identity ID
	StartDate, EndDate string
	LookbackDays       int // pull requests created up to this many days before startDate
	Top                int // pull requests per list (created by / reviewed by)
}

type PRActivityReport struct {
	StartDate    string       `json:"startDate"`
	EndDate      string       `json:"endDate"`
	Checked      int          `json:"pullRequestsChecked"`
	PullRequests []PRActivity `json:"pullRequests"`
	Skipped      []string     `json:"skipped,omitempty"`
}

type rawThread struct {
	IsDeleted  bool `json:"isDeleted"`
	Properties map[string]struct {
		Value any `json:"$value"`
	} `json:"properties"`
	Comments []struct {
		Content       string `json:"content"`
		CommentType   string `json:"commentType"`
		IsDeleted     bool   `json:"isDeleted"`
		PublishedDate string `json:"publishedDate"`
		Author        struct {
			ID string `json:"id"`
		} `json:"author"`
	} `json:"comments"`
}

func (t rawThread) prop(name string) string {
	if p, ok := t.Properties[name]; ok {
		return fmt.Sprint(p.Value)
	}
	return ""
}

// PullRequestActivity finds what one person did in pull requests during a
// period: the ones they created, their votes, comments and new commits.
// It checks the pull requests they created or review that were opened up to
// LookbackDays before the period.
func (a *AzDO) PullRequestActivity(ctx context.Context, f PRActivityFilter) (*PRActivityReport, error) {
	from, to, err := period(f.StartDate, f.EndDate)
	if err != nil {
		return nil, err
	}
	who := f.User
	if strings.TrimSpace(who) == "" {
		who = "me"
	}
	user, err := a.identityID(ctx, who)
	if err != nil {
		return nil, err
	}
	lookback := f.LookbackDays
	if lookback <= 0 {
		lookback = 30
	}
	if lookback > 90 {
		return nil, errors.New("lookbackDays máximo é 90")
	}
	top := f.Top
	if top <= 0 {
		top = 100
	}
	cutoff := from.AddDate(0, 0, -lookback)

	seen := map[int]bool{}
	var candidates []PRActivity
	for _, pf := range []PRFilter{
		{Project: f.Project, Status: "all", CreatedBy: user, Top: top},
		{Project: f.Project, Status: "all", Reviewer: user, Top: top},
	} {
		prs, err := a.rawPullRequests(ctx, pf)
		if err != nil {
			return nil, err
		}
		for _, r := range prs {
			created, ok := azTime(r.CreationDate)
			if seen[r.PullRequestID] || !ok || created.Before(cutoff) || !created.Before(to) {
				continue
			}
			seen[r.PullRequestID] = true
			p := a.pr(r)
			candidates = append(candidates, PRActivity{
				PullRequestID: p.ID, Title: p.Title, Status: p.Status, Project: p.Project, Repository: p.Repository,
				SourceBranch: p.SourceBranch, CreatedBy: p.CreatedBy, WebURL: p.WebURL, raw: r,
			})
		}
	}

	var mu sync.Mutex
	var skipped []string
	parallel(len(candidates), func(i int) {
		c := &candidates[i]
		if strings.EqualFold(c.raw.CreatedBy.ID, user) && within(c.raw.CreationDate, from, to) {
			c.Events = append(c.Events, PREvent{Date: c.raw.CreationDate, Type: "created"})
		}
		var th struct {
			Value []rawThread `json:"value"`
		}
		if err := a.gitDo(ctx, http.MethodGet, a.prPath(&c.raw)+"/threads?api-version="+azdoAPIVersion, nil, &th); err != nil {
			mu.Lock()
			skipped = append(skipped, fmt.Sprintf("PR %d: %v", c.PullRequestID, err))
			mu.Unlock()
			return
		}
		for _, t := range th.Value {
			if t.IsDeleted {
				continue
			}
			kind := t.prop("CodeReviewThreadType")
			for _, cm := range t.Comments {
				if cm.IsDeleted || !strings.EqualFold(cm.Author.ID, user) || !within(cm.PublishedDate, from, to) {
					continue
				}
				e := PREvent{Date: cm.PublishedDate}
				switch {
				case kind == "VoteUpdate":
					v, _ := strconv.Atoi(t.prop("CodeReviewVoteResult"))
					e.Type, e.Vote = "vote", voteName(v)
				case kind == "RefUpdate":
					e.Type = "push"
				case kind == "StatusUpdate":
					e.Type, e.Text = "status", clip(cm.Content, 200)
				case cm.CommentType == "system":
					continue // reviewers added, policy updates…
				default:
					e.Type, e.Text = "comment", clip(cm.Content, 300)
				}
				c.Events = append(c.Events, e)
			}
		}
		if len(c.Events) == 0 {
			return
		}
		var wis struct {
			Value []struct {
				ID string `json:"id"`
			} `json:"value"`
		}
		if a.gitDo(ctx, http.MethodGet, a.prPath(&c.raw)+"/workitems?api-version="+azdoAPIVersion, nil, &wis) == nil {
			for _, w := range wis.Value {
				if n, err := strconv.Atoi(w.ID); err == nil {
					c.WorkItems = append(c.WorkItems, n)
				}
			}
		}
	})

	rep := &PRActivityReport{Checked: len(candidates), PullRequests: []PRActivity{}, Skipped: skipped}
	rep.StartDate, rep.EndDate = periodLabel(from, to)
	for _, c := range candidates {
		if len(c.Events) == 0 {
			continue
		}
		sort.Slice(c.Events, func(i, j int) bool {
			ti, _ := azTime(c.Events[i].Date)
			tj, _ := azTime(c.Events[j].Date)
			return ti.Before(tj)
		})
		for i := range c.Events {
			c.Events[i].Date = localTime(c.Events[i].Date)
		}
		rep.PullRequests = append(rep.PullRequests, c)
	}
	sort.Slice(rep.PullRequests, func(i, j int) bool {
		return rep.PullRequests[i].Events[0].Date < rep.PullRequests[j].Events[0].Date
	})
	return rep, nil
}

// ---------- work item revisions ----------

type FieldChange struct {
	Old any `json:"old,omitempty"`
	New any `json:"new,omitempty"`
}

type WorkItemUpdate struct {
	Rev     int                    `json:"rev"`
	Date    string                 `json:"date"`
	By      string                 `json:"by"`
	Created bool                   `json:"created,omitempty"` // the revision that created the item
	Changes map[string]FieldChange `json:"changes,omitempty"`
	Comment string                 `json:"comment,omitempty"`
	Linked  []string               `json:"linksAdded,omitempty"`
	Removed []string               `json:"linksRemoved,omitempty"`
	// TimeTrackingOnly marks revisions that only moved Completed/Remaining
	// Work, usually written by a time tracker rather than by the person.
	TimeTrackingOnly bool `json:"timeTrackingOnly,omitempty"`
}

type WorkItemUpdates struct {
	ID      int              `json:"id"`
	Total   int              `json:"totalRevisions"`
	Count   int              `json:"count"`
	Updates []WorkItemUpdate `json:"updates"`
}

type WorkItemUpdatesFilter struct {
	ID                 int
	ChangedBy          string // "me", e-mail, name or identity ID ("" = anyone)
	StartDate, EndDate string // optional
}

// metaFields change on every revision, or follow another field, and say
// nothing about the work.
var metaFields = map[string]bool{
	"System.Rev": true, "System.ChangedDate": true, "System.ChangedBy": true, "System.AuthorizedDate": true,
	"System.RevisedDate": true, "System.AuthorizedAs": true, "System.Watermark": true, "System.PersonId": true,
	"System.CommentCount": true, "System.History": true, "System.Id": true, "System.IsDeleted": true,
	"System.CreatedDate": true, "System.CreatedBy": true, "System.TeamProject": true, "System.NodeName": true,
	"System.AreaId": true, "System.IterationId": true,
	"Microsoft.VSTS.Common.StateChangeDate": true, "Microsoft.VSTS.Common.ActivatedDate": true,
	"Microsoft.VSTS.Common.ActivatedBy": true, "Microsoft.VSTS.Common.ResolvedDate": true,
	"Microsoft.VSTS.Common.ResolvedBy": true, "Microsoft.VSTS.Common.ClosedDate": true, "Microsoft.VSTS.Common.ClosedBy": true,
}

func isMetaField(name string) bool {
	return metaFields[name] || strings.HasPrefix(name, "System.AreaLevel") || strings.HasPrefix(name, "System.IterationLevel")
}

var timeTrackingFields = map[string]bool{
	"Microsoft.VSTS.Scheduling.CompletedWork": true, "Microsoft.VSTS.Scheduling.RemainingWork": true,
}

var longTextFields = map[string]bool{
	"System.Description": true, "Microsoft.VSTS.Common.AcceptanceCriteria": true, "Microsoft.VSTS.TCM.ReproSteps": true,
}

func changeValue(field string, v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return identityName(t)
	case string:
		if longTextFields[field] {
			return "(texto, " + strconv.Itoa(utf8.RuneCountInString(htmlToText(t))) + " caracteres)"
		}
		return clip(t, 200)
	}
	return v
}

// WorkItemRevisions lists the revisions of a work item (who changed what
// and when), optionally only one person's and only in a period.
func (a *AzDO) WorkItemRevisions(ctx context.Context, f WorkItemUpdatesFilter) (*WorkItemUpdates, error) {
	if f.ID <= 0 {
		return nil, errors.New("id inválido")
	}
	var from, to time.Time
	if f.StartDate != "" || f.EndDate != "" {
		var err error
		if from, to, err = period(f.StartDate, f.EndDate); err != nil {
			return nil, err
		}
	}
	match := func(id, name, unique string) bool { return true }
	if who := strings.TrimSpace(f.ChangedBy); who != "" {
		if strings.EqualFold(who, "me") || strings.EqualFold(who, "@me") || guidRe.MatchString(who) {
			id, err := a.identityID(ctx, who)
			if err != nil {
				return nil, err
			}
			match = func(rid, _, _ string) bool { return strings.EqualFold(rid, id) }
		} else {
			w := strings.ToLower(who)
			match = func(_, name, unique string) bool {
				return strings.Contains(strings.ToLower(name), w) || strings.Contains(strings.ToLower(unique), w)
			}
		}
	}

	type rawLink struct {
		Rel        string         `json:"rel"`
		URL        string         `json:"url"`
		Attributes map[string]any `json:"attributes"`
	}
	type rawUpdate struct {
		Rev       int `json:"rev"`
		RevisedBy struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			UniqueName  string `json:"uniqueName"`
		} `json:"revisedBy"`
		RevisedDate string `json:"revisedDate"`
		Fields      map[string]struct {
			OldValue any `json:"oldValue"`
			NewValue any `json:"newValue"`
		} `json:"fields"`
		Relations struct {
			Added   []rawLink `json:"added"`
			Removed []rawLink `json:"removed"`
		} `json:"relations"`
	}
	const page = 200
	var all []rawUpdate
	for skip := 0; skip < 5*page; skip += page {
		var res struct {
			Value []rawUpdate `json:"value"`
		}
		p := "/_apis/wit/workItems/" + strconv.Itoa(f.ID) + "/updates?$top=" + strconv.Itoa(page) + "&$skip=" + strconv.Itoa(skip) + "&api-version=" + azdoAPIVersion
		if err := a.c.Do(ctx, http.MethodGet, p, nil, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Value...)
		if len(res.Value) < page {
			break
		}
	}

	link := func(l rawLink) string {
		name := l.Rel
		if n, ok := l.Attributes["name"].(string); ok && n != "" {
			name = n
		}
		if id := idFromURL(l.URL); id != 0 {
			return name + " " + strconv.Itoa(id)
		}
		return name
	}
	out := &WorkItemUpdates{ID: f.ID, Total: len(all), Updates: []WorkItemUpdate{}}
	for _, u := range all {
		date := u.RevisedDate
		if c, ok := u.Fields["System.ChangedDate"]; ok {
			if s, ok := c.NewValue.(string); ok {
				date = s
			}
		}
		if !from.IsZero() && !within(date, from, to) {
			continue
		}
		if !match(u.RevisedBy.ID, u.RevisedBy.DisplayName, u.RevisedBy.UniqueName) {
			continue
		}
		x := WorkItemUpdate{Rev: u.Rev, Date: localTime(date), By: u.RevisedBy.DisplayName, Created: u.Rev == 1, Changes: map[string]FieldChange{}}
		onlyTime := true
		for name, c := range u.Fields {
			if name == "System.History" {
				if s, ok := c.NewValue.(string); ok {
					x.Comment = clip(htmlToText(s), 500)
				}
				continue
			}
			if isMetaField(name) {
				continue
			}
			ch := FieldChange{Old: changeValue(name, c.OldValue), New: changeValue(name, c.NewValue)}
			if ch.Old == nil && ch.New == nil {
				continue
			}
			if !timeTrackingFields[name] {
				onlyTime = false
			}
			x.Changes[name] = ch
		}
		for _, l := range u.Relations.Added {
			x.Linked = append(x.Linked, link(l))
		}
		for _, l := range u.Relations.Removed {
			x.Removed = append(x.Removed, link(l))
		}
		x.TimeTrackingOnly = onlyTime && len(x.Changes) > 0 && x.Comment == "" && len(x.Linked)+len(x.Removed) == 0
		if len(x.Changes) == 0 {
			x.Changes = nil
		}
		out.Updates = append(out.Updates, x)
	}
	out.Count = len(out.Updates)
	return out, nil
}
