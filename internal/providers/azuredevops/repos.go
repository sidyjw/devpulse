package azuredevops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/sidyjw/devpulse/internal/httpx"
)

// Azure Repos: repositories, branches, branch policies and pull requests.
// Uses the same organization and PAT as Boards; the PAT needs the "Code"
// scope. Pushing is left to git in the harness: these calls cover what
// happens on the server. Deleting branches and completing or abandoning pull
// requests are intentionally not implemented.

const (
	identityPickerAPIVer = "7.1-preview.1"
	policyEvalAPIVer     = "7.1-preview.1"
	zeroObjectID         = "0000000000000000000000000000000000000000"
	maxPRDescription     = 4000
)

var shaRe = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// gitDo is c.Do with Azure DevOps error bodies ({"message": …}) reduced to
// their message, which is what explains a rejection (e.g. a branch policy).
func (a *AzDO) gitDo(ctx context.Context, method, pathAndQuery string, body, out any) error {
	err := a.c.Do(ctx, method, pathAndQuery, body, out)
	var he *httpx.Error
	if errors.As(err, &he) {
		var b struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(he.Body), &b) == nil && b.Message != "" {
			he.Body = b.Message
		}
	}
	return err
}

// ---------- repositories ----------

type Repository struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Project       string `json:"project"`
	ProjectID     string `json:"projectId"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
	RemoteURL     string `json:"remoteUrl,omitempty"`
	SSHURL        string `json:"sshUrl,omitempty"`
	WebURL        string `json:"webUrl,omitempty"`
	IsDisabled    bool   `json:"isDisabled,omitempty"`
}

type rawRepo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DefaultBranch string `json:"defaultBranch"`
	RemoteURL     string `json:"remoteUrl"`
	SSHURL        string `json:"sshUrl"`
	WebURL        string `json:"webUrl"`
	IsDisabled    bool   `json:"isDisabled"`
	Project       struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
}

func (r rawRepo) repo() Repository {
	return Repository{
		ID: r.ID, Name: r.Name, Project: r.Project.Name, ProjectID: r.Project.ID,
		DefaultBranch: shortBranch(r.DefaultBranch), RemoteURL: r.RemoteURL, SSHURL: r.SSHURL,
		WebURL: r.WebURL, IsDisabled: r.IsDisabled,
	}
}

func (a *AzDO) Repositories(ctx context.Context, project string) ([]Repository, string, error) {
	project, err := a.project(project)
	if err != nil {
		return nil, "", err
	}
	var res struct {
		Value []rawRepo `json:"value"`
	}
	if err := a.gitDo(ctx, http.MethodGet, "/"+pe(project)+"/_apis/git/repositories?api-version="+azdoAPIVersion, nil, &res); err != nil {
		return nil, "", err
	}
	out := make([]Repository, len(res.Value))
	for i, r := range res.Value {
		out[i] = r.repo()
	}
	return out, project, nil
}

// Repository resolves a repository given by name, ID or git remote URL.
func (a *AzDO) Repository(ctx context.Context, project, ref string) (*Repository, error) {
	project, name, err := a.repoRef(project, ref)
	if err != nil {
		return nil, err
	}
	var r rawRepo
	if err := a.gitDo(ctx, http.MethodGet, "/"+pe(project)+"/_apis/git/repositories/"+pe(name)+"?api-version="+azdoAPIVersion, nil, &r); err != nil {
		return nil, err
	}
	repo := r.repo()
	return &repo, nil
}

// repoRef splits a repository reference into project and repository. A git
// remote URL carries its own project and must belong to the configured
// organization, so a token is never used against a repository elsewhere.
func (a *AzDO) repoRef(project, ref string) (string, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", errors.New("informe repository (nome, ID ou URL do remoto, ex.: a saída de `git remote get-url origin`)")
	}
	if strings.Contains(ref, "://") || strings.HasPrefix(ref, "git@") || strings.Contains(ref, "@vs-ssh.") {
		return a.parseRemote(ref)
	}
	if strings.ContainsAny(ref, "/\\") {
		return "", "", fmt.Errorf("repository inválido: %q", ref)
	}
	project, err := a.project(project)
	return project, ref, err
}

// parseRemote reads project and repository from an Azure Repos remote:
//
//	https://dev.azure.com/{org}/{project}/_git/{repo}
//	https://{org}.visualstudio.com[/DefaultCollection]/{project}/_git/{repo}
//	https://{server}/{collection}/{project}/_git/{repo}   (Azure DevOps Server)
//	git@ssh.dev.azure.com:v3/{org}/{project}/{repo}
//	{org}@vs-ssh.visualstudio.com:v3/{org}/{project}/{repo}
func (a *AzDO) parseRemote(remote string) (string, string, error) {
	org := normalizeOrgURL(a.orgURL)
	if i := strings.Index(remote, ":v3/"); i >= 0 && !strings.Contains(remote, "://") {
		parts := strings.Split(strings.TrimSuffix(remote[i+4:], "/"), "/")
		if len(parts) != 3 {
			return "", "", fmt.Errorf("URL do remoto não reconhecida: %s", remote)
		}
		if !strings.EqualFold(org, "https://dev.azure.com/"+parts[0]) {
			return "", "", fmt.Errorf("o remoto %s é de outra organização (configurada: %s)", remote, a.orgURL)
		}
		return unescapeSegment(parts[1]), unescapeSegment(parts[2]), nil
	}
	u, err := url.Parse(remote)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("URL do remoto não reconhecida: %s", remote)
	}
	full := normalizeOrgURL("https://" + u.Host + u.EscapedPath())
	if !strings.HasPrefix(strings.ToLower(full), strings.ToLower(org)+"/") {
		return "", "", fmt.Errorf("o remoto %s é de outra organização (configurada: %s)", remote, a.orgURL)
	}
	parts := strings.Split(strings.Trim(full[len(org):], "/"), "/")
	switch {
	case len(parts) == 3 && parts[1] == "_git":
		return unescapeSegment(parts[0]), unescapeSegment(parts[2]), nil
	case len(parts) == 2 && parts[0] == "_git": // repository named after its project
		n := unescapeSegment(parts[1])
		return n, n, nil
	}
	return "", "", fmt.Errorf("URL do remoto não reconhecida: %s (esperado …/{projeto}/_git/{repositório})", remote)
}

// normalizeOrgURL rewrites https://{org}.visualstudio.com[/DefaultCollection]
// as https://dev.azure.com/{org}, so both spellings of an organization match.
func normalizeOrgURL(s string) string {
	s = strings.TrimRight(s, "/")
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	host := strings.ToLower(u.Host)
	if org, ok := strings.CutSuffix(host, ".visualstudio.com"); ok && org != "" && !strings.Contains(org, ".") {
		p := u.EscapedPath()
		if strings.HasPrefix(strings.ToLower(p), "/defaultcollection") {
			p = p[len("/defaultcollection"):]
		}
		return "https://dev.azure.com/" + org + strings.TrimRight(p, "/")
	}
	return "https://" + host + strings.TrimRight(u.EscapedPath(), "/")
}

func unescapeSegment(s string) string {
	if v, err := url.PathUnescape(s); err == nil {
		return v
	}
	return s
}

// ---------- branches ----------

// branchRef turns "feature/x" or "refs/heads/feature/x" into
// "refs/heads/feature/x", rejecting names git itself would refuse.
func branchRef(name string) (string, error) {
	n := strings.TrimPrefix(strings.TrimSpace(name), "refs/heads/")
	if err := checkBranchName(n); err != nil {
		return "", err
	}
	return "refs/heads/" + n, nil
}

func shortBranch(ref string) string { return strings.TrimPrefix(ref, "refs/heads/") }

func checkBranchName(n string) error {
	bad := func(why string) error { return fmt.Errorf("nome de branch inválido %q: %s", n, why) }
	switch {
	case n == "":
		return errors.New("informe o nome da branch")
	case len(n) > 250:
		return bad("longo demais")
	case n == "@":
		return bad("\"@\" não é permitido")
	case strings.HasPrefix(n, "/") || strings.HasSuffix(n, "/") || strings.Contains(n, "//"):
		return bad("barras no início, no fim ou repetidas")
	case strings.HasSuffix(n, ".") || strings.HasSuffix(n, ".lock"):
		return bad("não pode terminar com \".\" nem \".lock\"")
	case strings.Contains(n, "..") || strings.Contains(n, "@{"):
		return bad("não pode conter \"..\" nem \"@{\"")
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return bad(fmt.Sprintf("caractere %q não é permitido", r))
		}
	}
	for _, part := range strings.Split(n, "/") {
		if strings.HasPrefix(part, ".") {
			return bad("nenhuma parte pode começar com \".\"")
		}
	}
	return nil
}

type Branch struct {
	Name      string `json:"name"`
	Commit    string `json:"commit"`
	Creator   string `json:"creator,omitempty"`
	IsDefault bool   `json:"isDefault,omitempty"`
	IsLocked  bool   `json:"isLocked,omitempty"`
}

type rawRef struct {
	Name     string `json:"name"`
	ObjectID string `json:"objectId"`
	IsLocked bool   `json:"isLocked"`
	Creator  struct {
		DisplayName string `json:"displayName"`
	} `json:"creator"`
}

func (a *AzDO) refs(ctx context.Context, r *Repository, filter, contains string, top int) ([]rawRef, error) {
	q := url.Values{}
	q.Set("filter", filter)
	if contains != "" {
		q.Set("filterContains", contains)
	}
	q.Set("$top", strconv.Itoa(top))
	q.Set("api-version", azdoAPIVersion)
	var res struct {
		Value []rawRef `json:"value"`
	}
	p := "/" + pe(r.Project) + "/_apis/git/repositories/" + pe(r.ID) + "/refs?" + q.Encode()
	if err := a.gitDo(ctx, http.MethodGet, p, nil, &res); err != nil {
		return nil, err
	}
	return res.Value, nil
}

func (a *AzDO) Branches(ctx context.Context, project, repo, contains string, top int) (*Repository, []Branch, error) {
	if top <= 0 {
		top = 200
	}
	if top > 1000 {
		top = 1000
	}
	r, err := a.Repository(ctx, project, repo)
	if err != nil {
		return nil, nil, err
	}
	refs, err := a.refs(ctx, r, "heads/", strings.TrimSpace(contains), top)
	if err != nil {
		return nil, nil, err
	}
	out := make([]Branch, len(refs))
	for i, ref := range refs {
		n := shortBranch(ref.Name)
		out[i] = Branch{Name: n, Commit: ref.ObjectID, Creator: ref.Creator.DisplayName, IsDefault: n == r.DefaultBranch, IsLocked: ref.IsLocked}
	}
	return r, out, nil
}

// branchCommit returns the commit a branch points to (the filter is a
// prefix match, so the name is compared exactly).
func (a *AzDO) branchCommit(ctx context.Context, r *Repository, name string) (string, error) {
	ref, err := branchRef(name)
	if err != nil {
		return "", err
	}
	refs, err := a.refs(ctx, r, strings.TrimPrefix(ref, "refs/"), "", 1000)
	if err != nil {
		return "", err
	}
	for _, x := range refs {
		if x.Name == ref {
			return x.ObjectID, nil
		}
	}
	return "", fmt.Errorf("a branch %q não existe em %s", shortBranch(ref), r.Name)
}

type BranchResult struct {
	Message        string `json:"message"`
	Repository     string `json:"repository"`
	Branch         string `json:"branch"`
	From           string `json:"from"`
	Commit         string `json:"commit"`
	WebURL         string `json:"webUrl,omitempty"`
	WorkItemLinked int    `json:"workItemLinked,omitempty"`
	Warning        string `json:"warning,omitempty"`
}

// CreateBranch creates a branch on the server from another branch (default:
// the repository's default branch) or from a commit SHA, optionally linked
// to a work item.
func (a *AzDO) CreateBranch(ctx context.Context, project, repo, name, from string, workItemID int) (*BranchResult, error) {
	ref, err := branchRef(name)
	if err != nil {
		return nil, err
	}
	r, err := a.Repository(ctx, project, repo)
	if err != nil {
		return nil, err
	}
	from = strings.TrimSpace(from)
	if from == "" {
		if r.DefaultBranch == "" {
			return nil, fmt.Errorf("o repositório %s está vazio; informe from", r.Name)
		}
		from = r.DefaultBranch
	}
	commit := from
	if !shaRe.MatchString(from) {
		if commit, err = a.branchCommit(ctx, r, from); err != nil {
			return nil, err
		}
		from = shortBranch(from)
	}
	var res struct {
		Value []struct {
			Name          string `json:"name"`
			Success       bool   `json:"success"`
			UpdateStatus  string `json:"updateStatus"`
			CustomMessage string `json:"customMessage"`
		} `json:"value"`
	}
	body := []map[string]string{{"name": ref, "oldObjectId": zeroObjectID, "newObjectId": commit}}
	p := "/" + pe(r.Project) + "/_apis/git/repositories/" + pe(r.ID) + "/refs?api-version=" + azdoAPIVersion
	if err := a.gitDo(ctx, http.MethodPost, p, body, &res); err != nil {
		return nil, err
	}
	if len(res.Value) != 1 || !res.Value[0].Success {
		why := "resposta vazia"
		if len(res.Value) == 1 {
			why = strings.TrimSpace(res.Value[0].UpdateStatus + " " + res.Value[0].CustomMessage)
		}
		return nil, fmt.Errorf("o Azure DevOps não criou a branch %s: %s", shortBranch(ref), why)
	}
	out := &BranchResult{
		Message:    fmt.Sprintf("branch %s criada em %s a partir de %s", shortBranch(ref), r.Name, from),
		Repository: r.Name, Branch: shortBranch(ref), From: from, Commit: commit,
	}
	if r.WebURL != "" {
		out.WebURL = r.WebURL + "?version=GB" + url.QueryEscape(shortBranch(ref))
	}
	if workItemID > 0 {
		link := "vstfs:///Git/Ref/" + r.ProjectID + "%2F" + r.ID + "%2FGB" + escapeAll(shortBranch(ref))
		if err := a.AddArtifactLink(ctx, workItemID, link, "Branch"); err != nil {
			out.Warning = fmt.Sprintf("a branch foi criada, mas não foi vinculada ao #%d: %v", workItemID, err)
		} else {
			out.WorkItemLinked = workItemID
		}
	}
	return out, nil
}

// escapeAll percent-encodes s including "/", as artifact links expect.
func escapeAll(s string) string { return strings.ReplaceAll(url.PathEscape(s), "/", "%2F") }

// AddArtifactLink links a work item to a branch, commit or pull request.
func (a *AzDO) AddArtifactLink(ctx context.Context, id int, artifact, name string) error {
	ops := []patchOp{{Op: "add", Path: "/relations/-", Value: map[string]any{
		"rel": "ArtifactLink", "url": artifact, "attributes": map[string]string{"name": name},
	}}}
	p := "/_apis/wit/workitems/" + strconv.Itoa(id) + "?api-version=" + azdoAPIVersion
	return a.c.DoCT(ctx, http.MethodPatch, p, "application/json-patch+json", ops, nil)
}

// ---------- branch policies ----------

type BranchPolicy struct {
	Type     string         `json:"type"`
	Enabled  bool           `json:"enabled"`
	Blocking bool           `json:"blocking"`
	Settings map[string]any `json:"settings,omitempty"`
}

type BranchPolicies struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	// PullRequestRequired is true when an enabled, blocking policy applies:
	// Azure DevOps then only accepts changes to the branch through a PR.
	PullRequestRequired bool           `json:"pullRequestRequired"`
	Policies            []BranchPolicy `json:"policies"`
}

func (a *AzDO) BranchPolicies(ctx context.Context, project, repo, branch string) (*BranchPolicies, error) {
	r, err := a.Repository(ctx, project, repo)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(branch) == "" {
		branch = r.DefaultBranch
	}
	ref, err := branchRef(branch)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("repositoryId", r.ID)
	q.Set("refName", ref)
	q.Set("api-version", azdoAPIVersion)
	var res struct {
		Value []struct {
			IsEnabled  bool           `json:"isEnabled"`
			IsBlocking bool           `json:"isBlocking"`
			IsDeleted  bool           `json:"isDeleted"`
			Settings   map[string]any `json:"settings"`
			Type       struct {
				DisplayName string `json:"displayName"`
			} `json:"type"`
		} `json:"value"`
	}
	if err := a.gitDo(ctx, http.MethodGet, "/"+pe(r.Project)+"/_apis/git/policy/configurations?"+q.Encode(), nil, &res); err != nil {
		return nil, err
	}
	out := &BranchPolicies{Repository: r.Name, Branch: shortBranch(ref), Policies: []BranchPolicy{}}
	for _, p := range res.Value {
		if p.IsDeleted {
			continue
		}
		delete(p.Settings, "scope")
		out.Policies = append(out.Policies, BranchPolicy{Type: p.Type.DisplayName, Enabled: p.IsEnabled, Blocking: p.IsBlocking, Settings: p.Settings})
		if p.IsEnabled && p.IsBlocking {
			out.PullRequestRequired = true
		}
	}
	return out, nil
}

// ---------- pull requests ----------

type PRReviewer struct {
	Name     string `json:"name"`
	Vote     string `json:"vote"`
	Required bool   `json:"required,omitempty"`
}

type PullRequest struct {
	ID           int          `json:"id"`
	Title        string       `json:"title"`
	Status       string       `json:"status"`
	IsDraft      bool         `json:"isDraft"`
	Project      string       `json:"project"`
	Repository   string       `json:"repository"`
	SourceBranch string       `json:"sourceBranch"`
	TargetBranch string       `json:"targetBranch"`
	CreatedBy    string       `json:"createdBy"`
	CreationDate string       `json:"creationDate"`
	MergeStatus  string       `json:"mergeStatus,omitempty"`
	Reviewers    []PRReviewer `json:"reviewers,omitempty"`
	WebURL       string       `json:"webUrl"`
}

type rawPR struct {
	PullRequestID int    `json:"pullRequestId"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Status        string `json:"status"`
	IsDraft       bool   `json:"isDraft"`
	SourceRefName string `json:"sourceRefName"`
	TargetRefName string `json:"targetRefName"`
	CreationDate  string `json:"creationDate"`
	ClosedDate    string `json:"closedDate"`
	MergeStatus   string `json:"mergeStatus"`
	CreatedBy     struct {
		DisplayName string `json:"displayName"`
	} `json:"createdBy"`
	AutoCompleteSetBy *struct {
		DisplayName string `json:"displayName"`
	} `json:"autoCompleteSetBy"`
	LastMergeSourceCommit *struct {
		CommitID string `json:"commitId"`
	} `json:"lastMergeSourceCommit"`
	Reviewers []struct {
		DisplayName string `json:"displayName"`
		Vote        int    `json:"vote"`
		IsRequired  bool   `json:"isRequired"`
	} `json:"reviewers"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Repository struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Project struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"project"`
	} `json:"repository"`
}

func voteName(v int) string {
	switch {
	case v >= 10:
		return "approved"
	case v >= 5:
		return "approved with suggestions"
	case v <= -10:
		return "rejected"
	case v < 0:
		return "waiting for author"
	}
	return "no vote"
}

func (a *AzDO) pr(r rawPR) PullRequest {
	p := PullRequest{
		ID: r.PullRequestID, Title: r.Title, Status: r.Status, IsDraft: r.IsDraft,
		Project: r.Repository.Project.Name, Repository: r.Repository.Name,
		SourceBranch: shortBranch(r.SourceRefName), TargetBranch: shortBranch(r.TargetRefName),
		CreatedBy: r.CreatedBy.DisplayName, CreationDate: r.CreationDate, MergeStatus: r.MergeStatus,
		WebURL: a.orgURL + "/" + pe(r.Repository.Project.Name) + "/_git/" + pe(r.Repository.Name) + "/pullrequest/" + strconv.Itoa(r.PullRequestID),
	}
	for _, rv := range r.Reviewers {
		p.Reviewers = append(p.Reviewers, PRReviewer{Name: rv.DisplayName, Vote: voteName(rv.Vote), Required: rv.IsRequired})
	}
	return p
}

type PRFilter struct {
	Project, Repository  string
	Status               string // active (default), completed, abandoned, all
	CreatedBy, Reviewer  string // "me" or an identity ID
	SourceBranch, Target string
	Top                  int
}

func (a *AzDO) PullRequests(ctx context.Context, f PRFilter) ([]PullRequest, error) {
	q := url.Values{}
	status := strings.ToLower(strings.TrimSpace(f.Status))
	switch status {
	case "":
		status = "active"
	case "active", "completed", "abandoned", "all":
	default:
		return nil, fmt.Errorf("status inválido: %q (use active, completed, abandoned ou all)", f.Status)
	}
	q.Set("searchCriteria.status", status)
	for _, c := range []struct{ param, v string }{{"searchCriteria.creatorId", f.CreatedBy}, {"searchCriteria.reviewerId", f.Reviewer}} {
		if strings.TrimSpace(c.v) == "" {
			continue
		}
		id, err := a.identityID(ctx, c.v)
		if err != nil {
			return nil, err
		}
		q.Set(c.param, id)
	}
	for _, c := range []struct{ param, v string }{{"searchCriteria.sourceRefName", f.SourceBranch}, {"searchCriteria.targetRefName", f.Target}} {
		if strings.TrimSpace(c.v) == "" {
			continue
		}
		ref, err := branchRef(c.v)
		if err != nil {
			return nil, err
		}
		q.Set(c.param, ref)
	}
	top := f.Top
	if top <= 0 {
		top = 50
	}
	if top > 200 {
		top = 200
	}
	q.Set("$top", strconv.Itoa(top))
	q.Set("api-version", azdoAPIVersion)

	var p string
	if strings.TrimSpace(f.Repository) != "" {
		project, repo, err := a.repoRef(f.Project, f.Repository)
		if err != nil {
			return nil, err
		}
		p = "/" + pe(project) + "/_apis/git/repositories/" + pe(repo) + "/pullrequests?"
	} else {
		project, err := a.project(f.Project)
		if err != nil {
			return nil, err
		}
		p = "/" + pe(project) + "/_apis/git/pullrequests?"
	}
	var res struct {
		Value []rawPR `json:"value"`
	}
	if err := a.gitDo(ctx, http.MethodGet, p+q.Encode(), nil, &res); err != nil {
		return nil, err
	}
	out := make([]PullRequest, len(res.Value))
	for i, r := range res.Value {
		out[i] = a.pr(r)
	}
	return out, nil
}

// identityID accepts "me" or an identity ID (GUID); searches by name need a
// different API and are done by resolveIdentity.
func (a *AzDO) identityID(ctx context.Context, v string) (string, error) {
	v = strings.TrimSpace(v)
	if strings.EqualFold(v, "me") || strings.EqualFold(v, "@me") {
		me, err := a.Me(ctx)
		if err != nil {
			return "", err
		}
		return me.ID, nil
	}
	if guidRe.MatchString(v) {
		return v, nil
	}
	return a.resolveIdentity(ctx, v)
}

// resolveIdentity finds a user or group by e-mail or display name with the
// Identity Picker, which lives on the organization host.
func (a *AzDO) resolveIdentity(ctx context.Context, q string) (string, error) {
	body := map[string]any{
		"query":           q,
		"identityTypes":   []string{"user", "group"},
		"operationScopes": []string{"ims", "source"},
		"options":         map[string]int{"MinResults": 1, "MaxResults": 10},
		"properties":      []string{"DisplayName", "Mail", "SignInAddress"},
	}
	var res struct {
		Results []struct {
			Identities []struct {
				LocalID       string `json:"localId"`
				DisplayName   string `json:"displayName"`
				Mail          string `json:"mail"`
				SignInAddress string `json:"signInAddress"`
			} `json:"identities"`
		} `json:"results"`
	}
	if err := a.gitDo(ctx, http.MethodPost, "/_apis/IdentityPicker/Identities?api-version="+identityPickerAPIVer, body, &res); err != nil {
		return "", fmt.Errorf("buscar a identidade %q: %w", q, err)
	}
	type cand struct{ id, label string }
	var all, exact []cand
	for _, r := range res.Results {
		for _, i := range r.Identities {
			c := cand{i.LocalID, i.DisplayName}
			if i.Mail != "" {
				c.label += " <" + i.Mail + ">"
			}
			all = append(all, c)
			if strings.EqualFold(q, i.Mail) || strings.EqualFold(q, i.SignInAddress) || strings.EqualFold(q, i.DisplayName) {
				exact = append(exact, c)
			}
		}
	}
	pick := exact
	if len(pick) == 0 {
		pick = all
	}
	switch {
	case len(pick) == 0:
		return "", fmt.Errorf("nenhuma pessoa ou grupo encontrado para %q", q)
	case len(pick) > 1:
		var labels []string
		for _, c := range pick {
			labels = append(labels, c.label)
		}
		return "", fmt.Errorf("%q é ambíguo: %s; use o e-mail", q, strings.Join(labels, "; "))
	case pick[0].id == "":
		return "", fmt.Errorf("%s ainda não tem ID nesta organização; informe o ID da identidade", pick[0].label)
	}
	return pick[0].id, nil
}

type PRComment struct {
	Author string `json:"author"`
	Date   string `json:"date"`
	Text   string `json:"text"`
}

type PRThread struct {
	ID       int         `json:"id"`
	Status   string      `json:"status,omitempty"`
	FilePath string      `json:"filePath,omitempty"`
	Line     int         `json:"line,omitempty"`
	Comments []PRComment `json:"comments"`
}

type PRCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Blocking bool   `json:"blocking"`
}

type PullRequestDetail struct {
	PullRequest
	Description  string     `json:"description,omitempty"`
	ClosedDate   string     `json:"closedDate,omitempty"`
	AutoComplete string     `json:"autoCompleteSetBy,omitempty"`
	LastCommit   string     `json:"lastCommit,omitempty"`
	Labels       []string   `json:"labels,omitempty"`
	WorkItems    []int      `json:"workItems,omitempty"`
	Checks       []PRCheck  `json:"checks,omitempty"`
	Threads      []PRThread `json:"threads,omitempty"`
}

func (a *AzDO) rawPullRequest(ctx context.Context, project string, id int) (*rawPR, error) {
	if id <= 0 {
		return nil, errors.New("id inválido")
	}
	project, err := a.project(project)
	if err != nil {
		return nil, err
	}
	var r rawPR
	if err := a.gitDo(ctx, http.MethodGet, "/"+pe(project)+"/_apis/git/pullrequests/"+strconv.Itoa(id)+"?api-version="+azdoAPIVersion, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (a *AzDO) prPath(r *rawPR) string {
	return "/" + pe(r.Repository.Project.Name) + "/_apis/git/repositories/" + pe(r.Repository.ID) + "/pullRequests/" + strconv.Itoa(r.PullRequestID)
}

// PullRequestDetail returns a pull request with its linked work items,
// policy checks and (optionally) comment threads. The extras are best
// effort: a failure there does not hide the pull request itself.
func (a *AzDO) PullRequestDetail(ctx context.Context, project string, id int, withThreads bool) (*PullRequestDetail, error) {
	r, err := a.rawPullRequest(ctx, project, id)
	if err != nil {
		return nil, err
	}
	d := &PullRequestDetail{PullRequest: a.pr(*r), Description: r.Description, ClosedDate: r.ClosedDate}
	if r.AutoCompleteSetBy != nil {
		d.AutoComplete = r.AutoCompleteSetBy.DisplayName
	}
	if r.LastMergeSourceCommit != nil {
		d.LastCommit = r.LastMergeSourceCommit.CommitID
	}
	for _, l := range r.Labels {
		d.Labels = append(d.Labels, l.Name)
	}

	var wis struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
	}
	if a.gitDo(ctx, http.MethodGet, a.prPath(r)+"/workitems?api-version="+azdoAPIVersion, nil, &wis) == nil {
		for _, w := range wis.Value {
			if n, err := strconv.Atoi(w.ID); err == nil {
				d.WorkItems = append(d.WorkItems, n)
			}
		}
	}

	var ev struct {
		Value []struct {
			Status        string `json:"status"`
			Configuration struct {
				IsBlocking bool `json:"isBlocking"`
				Type       struct {
					DisplayName string `json:"displayName"`
				} `json:"type"`
			} `json:"configuration"`
		} `json:"value"`
	}
	artifact := "vstfs:///CodeReview/CodeReviewId/" + r.Repository.Project.ID + "/" + strconv.Itoa(r.PullRequestID)
	evPath := "/" + pe(r.Repository.Project.Name) + "/_apis/policy/evaluations?artifactId=" + url.QueryEscape(artifact) + "&api-version=" + policyEvalAPIVer
	if a.gitDo(ctx, http.MethodGet, evPath, nil, &ev) == nil {
		for _, e := range ev.Value {
			d.Checks = append(d.Checks, PRCheck{Name: e.Configuration.Type.DisplayName, Status: e.Status, Blocking: e.Configuration.IsBlocking})
		}
	}

	if withThreads {
		var th struct {
			Value []struct {
				ID            int    `json:"id"`
				Status        string `json:"status"`
				IsDeleted     bool   `json:"isDeleted"`
				ThreadContext *struct {
					FilePath       string `json:"filePath"`
					RightFileStart *struct {
						Line int `json:"line"`
					} `json:"rightFileStart"`
				} `json:"threadContext"`
				Comments []struct {
					Content       string `json:"content"`
					CommentType   string `json:"commentType"`
					IsDeleted     bool   `json:"isDeleted"`
					PublishedDate string `json:"publishedDate"`
					Author        struct {
						DisplayName string `json:"displayName"`
					} `json:"author"`
				} `json:"comments"`
			} `json:"value"`
		}
		if a.gitDo(ctx, http.MethodGet, a.prPath(r)+"/threads?api-version="+azdoAPIVersion, nil, &th) == nil {
			for _, t := range th.Value {
				if t.IsDeleted {
					continue
				}
				pt := PRThread{ID: t.ID, Status: t.Status}
				if t.ThreadContext != nil {
					pt.FilePath = t.ThreadContext.FilePath
					if t.ThreadContext.RightFileStart != nil {
						pt.Line = t.ThreadContext.RightFileStart.Line
					}
				}
				for _, c := range t.Comments {
					if c.IsDeleted || c.CommentType == "system" {
						continue
					}
					date := c.PublishedDate
					if len(date) > 16 {
						date = strings.Replace(date[:16], "T", " ", 1)
					}
					pt.Comments = append(pt.Comments, PRComment{Author: c.Author.DisplayName, Date: date, Text: httpx.Truncate(c.Content, 4000)})
				}
				if len(pt.Comments) > 0 {
					d.Threads = append(d.Threads, pt)
				}
			}
		}
	}
	return d, nil
}

type NewPullRequest struct {
	Project, Repository        string
	SourceBranch, TargetBranch string
	Title, Description         string
	Draft                      bool
	Reviewers, Required        []string
	WorkItemIDs                []int
	Labels                     []string
}

type PRWriteResult struct {
	Message     string       `json:"message"`
	PullRequest *PullRequest `json:"pullRequest,omitempty"`
	WebURL      string       `json:"webUrl,omitempty"`
	Warning     string       `json:"warning,omitempty"`
}

func checkPRText(title, description *string) error {
	if title != nil {
		t := strings.TrimSpace(*title)
		if t == "" || len(t) > 400 {
			return errors.New("title deve ter entre 1 e 400 caracteres")
		}
		*title = t
	}
	if description != nil && len(*description) > maxPRDescription {
		return fmt.Errorf("description tem %d caracteres; o Azure DevOps aceita até %d", len(*description), maxPRDescription)
	}
	return nil
}

func (a *AzDO) reviewerList(ctx context.Context, optional, required []string) ([]map[string]any, error) {
	var out []map[string]any
	seen := map[string]bool{}
	for _, g := range []struct {
		names    []string
		required bool
	}{{required, true}, {optional, false}} {
		for _, n := range g.names {
			if strings.TrimSpace(n) == "" {
				continue
			}
			id, err := a.identityID(ctx, n)
			if err != nil {
				return nil, err
			}
			if seen[strings.ToLower(id)] {
				continue
			}
			seen[strings.ToLower(id)] = true
			out = append(out, map[string]any{"id": id, "isRequired": g.required})
		}
	}
	return out, nil
}

// CreatePullRequest opens a pull request. Nothing is assumed beyond what
// Azure DevOps itself does: the target defaults to the repository's default
// branch and the PR is published unless Draft is set.
func (a *AzDO) CreatePullRequest(ctx context.Context, n NewPullRequest) (*PRWriteResult, error) {
	if err := checkPRText(&n.Title, &n.Description); err != nil {
		return nil, err
	}
	src, err := branchRef(n.SourceBranch)
	if err != nil {
		return nil, fmt.Errorf("sourceBranch: %w", err)
	}
	r, err := a.Repository(ctx, n.Project, n.Repository)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(n.TargetBranch)
	if target == "" {
		if r.DefaultBranch == "" {
			return nil, fmt.Errorf("o repositório %s não tem branch padrão; informe targetBranch", r.Name)
		}
		target = r.DefaultBranch
	}
	dst, err := branchRef(target)
	if err != nil {
		return nil, fmt.Errorf("targetBranch: %w", err)
	}
	if src == dst {
		return nil, errors.New("sourceBranch e targetBranch são a mesma branch")
	}
	reviewers, err := a.reviewerList(ctx, n.Reviewers, n.Required)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"sourceRefName": src,
		"targetRefName": dst,
		"title":         n.Title,
		"description":   n.Description,
		"isDraft":       n.Draft,
	}
	if len(reviewers) > 0 {
		body["reviewers"] = reviewers
	}
	if len(n.WorkItemIDs) > 0 {
		refs := make([]map[string]string, 0, len(n.WorkItemIDs))
		for _, id := range n.WorkItemIDs {
			if id <= 0 {
				return nil, fmt.Errorf("workItemIds: ID inválido %d", id)
			}
			refs = append(refs, map[string]string{"id": strconv.Itoa(id)})
		}
		body["workItemRefs"] = refs
	}
	var labels []map[string]string
	for _, l := range n.Labels {
		if l = strings.TrimSpace(l); l != "" {
			labels = append(labels, map[string]string{"name": l})
		}
	}
	if len(labels) > 0 {
		body["labels"] = labels
	}
	var created rawPR
	p := "/" + pe(r.Project) + "/_apis/git/repositories/" + pe(r.ID) + "/pullrequests?supportsIterations=true&api-version=" + azdoAPIVersion
	if err := a.gitDo(ctx, http.MethodPost, p, body, &created); err != nil {
		return nil, err
	}
	pr := a.pr(created)
	kind := "PR"
	if pr.IsDraft {
		kind = "PR (rascunho)"
	}
	return &PRWriteResult{
		Message:     fmt.Sprintf("%s !%d criada: %s → %s", kind, pr.ID, pr.SourceBranch, pr.TargetBranch),
		PullRequest: &pr, WebURL: pr.WebURL,
	}, nil
}

type PullRequestChanges struct {
	Title, Description, TargetBranch *string
	Draft                            *bool
	AddReviewers, AddRequired        []string
	WorkItemIDs                      []int
}

// UpdatePullRequest changes title, description, draft state or target
// branch, adds reviewers and links work items (through the work item: the
// PR API has no call to add them after creation).
func (a *AzDO) UpdatePullRequest(ctx context.Context, project string, id int, ch PullRequestChanges) (*PRWriteResult, error) {
	if err := checkPRText(ch.Title, ch.Description); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if ch.Title != nil {
		body["title"] = *ch.Title
	}
	if ch.Description != nil {
		body["description"] = *ch.Description
	}
	if ch.Draft != nil {
		body["isDraft"] = *ch.Draft
	}
	if ch.TargetBranch != nil {
		dst, err := branchRef(*ch.TargetBranch)
		if err != nil {
			return nil, fmt.Errorf("targetBranch: %w", err)
		}
		body["targetRefName"] = dst
	}
	if len(body) == 0 && len(ch.AddReviewers) == 0 && len(ch.AddRequired) == 0 && len(ch.WorkItemIDs) == 0 {
		return nil, errors.New("nenhuma alteração informada")
	}
	reviewers, err := a.reviewerList(ctx, ch.AddReviewers, ch.AddRequired)
	if err != nil {
		return nil, err
	}
	r, err := a.rawPullRequest(ctx, project, id)
	if err != nil {
		return nil, err
	}
	var done []string
	if len(body) > 0 {
		var updated rawPR
		if err := a.gitDo(ctx, http.MethodPatch, a.prPath(r)+"?api-version="+azdoAPIVersion, body, &updated); err != nil {
			return nil, err
		}
		r = &updated
		done = append(done, "dados atualizados")
	}
	for _, rv := range reviewers {
		p := a.prPath(r) + "/reviewers/" + pe(rv["id"].(string)) + "?api-version=" + azdoAPIVersion
		if err := a.gitDo(ctx, http.MethodPut, p, map[string]any{"vote": 0, "isRequired": rv["isRequired"]}, nil); err != nil {
			return nil, fmt.Errorf("PR !%d: %s; falha ao adicionar revisor: %w", id, strings.Join(append(done, "nada mais foi alterado"), ", "), err)
		}
	}
	if len(reviewers) > 0 {
		done = append(done, fmt.Sprintf("%d revisor(es) adicionado(s)", len(reviewers)))
	}
	res := &PRWriteResult{}
	if len(ch.WorkItemIDs) > 0 {
		link := "vstfs:///Git/PullRequestId/" + r.Repository.Project.ID + "%2F" + r.Repository.ID + "%2F" + strconv.Itoa(r.PullRequestID)
		var linked []string
		var failed []string
		for _, wi := range ch.WorkItemIDs {
			if err := a.AddArtifactLink(ctx, wi, link, "Pull Request"); err != nil {
				failed = append(failed, fmt.Sprintf("#%d: %v", wi, err))
			} else {
				linked = append(linked, "#"+strconv.Itoa(wi))
			}
		}
		if len(linked) > 0 {
			done = append(done, "vinculada a "+strings.Join(linked, ", "))
		}
		if len(failed) > 0 {
			res.Warning = "não vinculada a " + strings.Join(failed, "; ")
		}
	}
	pr := a.pr(*r)
	res.PullRequest, res.WebURL = &pr, pr.WebURL
	res.Message = fmt.Sprintf("PR !%d: %s", id, strings.Join(done, ", "))
	if len(done) == 0 {
		res.Message = fmt.Sprintf("PR !%d: nada foi alterado", id)
	}
	return res, nil
}

// AddPullRequestComment starts a thread (optionally on a file line) or
// replies to an existing one.
func (a *AzDO) AddPullRequestComment(ctx context.Context, project string, id int, text string, threadID int, filePath string, line int) (*PRWriteResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("text é obrigatório")
	}
	if len(text) > 150000 {
		return nil, errors.New("comentário longo demais")
	}
	filePath = strings.TrimSpace(filePath)
	if threadID > 0 && filePath != "" {
		return nil, errors.New("use threadId (responder) ou filePath (novo comentário no arquivo), não os dois")
	}
	if line < 0 || (line > 0 && filePath == "") {
		return nil, errors.New("line exige filePath e deve ser positiva")
	}
	r, err := a.rawPullRequest(ctx, project, id)
	if err != nil {
		return nil, err
	}
	comment := map[string]any{"content": text, "commentType": 1}
	if threadID > 0 {
		comment["parentCommentId"] = 1
		p := a.prPath(r) + "/threads/" + strconv.Itoa(threadID) + "/comments?api-version=" + azdoAPIVersion
		if err := a.gitDo(ctx, http.MethodPost, p, comment, nil); err != nil {
			return nil, err
		}
		return &PRWriteResult{Message: fmt.Sprintf("resposta adicionada à thread %d da PR !%d", threadID, id), WebURL: a.pr(*r).WebURL}, nil
	}
	comment["parentCommentId"] = 0
	thread := map[string]any{"comments": []any{comment}, "status": 1}
	if filePath != "" {
		if !strings.HasPrefix(filePath, "/") {
			filePath = "/" + filePath
		}
		ctxt := map[string]any{"filePath": filePath}
		if line > 0 {
			ctxt["rightFileStart"] = map[string]int{"line": line, "offset": 1}
			ctxt["rightFileEnd"] = map[string]int{"line": line, "offset": 1}
		}
		thread["threadContext"] = ctxt
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := a.gitDo(ctx, http.MethodPost, a.prPath(r)+"/threads?api-version="+azdoAPIVersion, thread, &created); err != nil {
		return nil, err
	}
	return &PRWriteResult{Message: fmt.Sprintf("comentário adicionado à PR !%d (thread %d)", id, created.ID), WebURL: a.pr(*r).WebURL}, nil
}
