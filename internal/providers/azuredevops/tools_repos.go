package azuredevops

import (
	"context"
	"encoding/json"

	"github.com/sidyjw/devpulse/internal/mcp"
)

// registerReposTools adds the Azure Repos tools.
func registerReposTools(s *mcp.Server, az *AzDO, cfg *reposConfig) {
	projectP := mcp.StrP("Projeto (padrão: AZURE_DEVOPS_PROJECT; ignorado quando repository é uma URL)")
	repoP := mcp.StrP("Repositório: nome, ID ou URL do remoto (ex.: a saída de `git remote get-url origin`)")

	s.AddTool(&mcp.Tool{
		Name:        "list_repositories",
		Title:       "Azure Repos: repositórios",
		Description: "Lista os repositórios Git de um projeto, com a branch padrão e as URLs de clone.",
		InputSchema: mcp.Obj(map[string]any{"project": projectP}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		repos, project, err := az.Repositories(ctx, a.Project)
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": project, "repositories": repos}, nil
	})

	s.AddTool(&mcp.Tool{
		Name:  "get_repository",
		Title: "Azure Repos: repositório",
		Description: "Identifica um repositório (por nome, ID ou URL do remoto) e mostra projeto, ID e branch padrão. " +
			"Recusa remotos de outra organização.",
		InputSchema: mcp.Obj(map[string]any{"repository": repoP, "project": projectP}, "repository"),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Repository string `json:"repository"`
			Project    string `json:"project"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return az.Repository(ctx, a.Project, a.Repository)
	})

	s.AddTool(&mcp.Tool{
		Name:        "list_branches",
		Title:       "Azure Repos: branches",
		Description: "Lista as branches remotas de um repositório, com o commit de cada uma e qual é a padrão.",
		InputSchema: mcp.Obj(map[string]any{
			"repository": repoP,
			"project":    projectP,
			"contains":   mcp.StrP("Opcional: só branches cujo nome contém este texto"),
			"top":        mcp.IntP("Máximo de branches (padrão 200, máx. 1000)"),
		}, "repository"),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Repository string `json:"repository"`
			Project    string `json:"project"`
			Contains   string `json:"contains"`
			Top        int    `json:"top"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		r, bs, err := az.Branches(ctx, a.Project, a.Repository, a.Contains, a.Top)
		if err != nil {
			return nil, err
		}
		return map[string]any{"repository": r.Name, "defaultBranch": r.DefaultBranch, "branches": bs}, nil
	})

	s.AddTool(&mcp.Tool{
		Name:  "get_branch_policies",
		Title: "Azure Repos: políticas da branch",
		Description: "Mostra as políticas de uma branch (revisores mínimos, build, work item vinculado…). " +
			"pullRequestRequired=true indica que o Azure DevOps só aceita mudanças nessa branch por pull request.",
		InputSchema: mcp.Obj(map[string]any{
			"repository": repoP,
			"project":    projectP,
			"branch":     mcp.StrP("Branch (padrão: a branch padrão do repositório)"),
		}, "repository"),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Repository string `json:"repository"`
			Project    string `json:"project"`
			Branch     string `json:"branch"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return az.BranchPolicies(ctx, a.Project, a.Repository, a.Branch)
	})

	s.AddTool(&mcp.Tool{
		Name:        "list_pull_requests",
		Title:       "Azure Repos: pull requests",
		Description: "Lista pull requests de um repositório ou do projeto inteiro, filtrando por status, autor, revisor e branches.",
		InputSchema: mcp.Obj(map[string]any{
			"project":      projectP,
			"repository":   mcp.StrP("Opcional: nome, ID ou URL do remoto; sem ele, busca no projeto todo"),
			"status":       mcp.EnumP("Status (padrão active)", "active", "completed", "abandoned", "all"),
			"createdBy":    mcp.StrP("Opcional: \"me\", e-mail, nome ou ID do autor"),
			"reviewer":     mcp.StrP("Opcional: \"me\", e-mail, nome ou ID do revisor"),
			"sourceBranch": mcp.StrP("Opcional: branch de origem"),
			"targetBranch": mcp.StrP("Opcional: branch de destino"),
			"top":          mcp.IntP("Máximo de PRs (padrão 50, máx. 200)"),
		}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Project      string `json:"project"`
			Repository   string `json:"repository"`
			Status       string `json:"status"`
			CreatedBy    string `json:"createdBy"`
			Reviewer     string `json:"reviewer"`
			SourceBranch string `json:"sourceBranch"`
			TargetBranch string `json:"targetBranch"`
			Top          int    `json:"top"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		prs, err := az.PullRequests(ctx, PRFilter{
			Project: a.Project, Repository: a.Repository, Status: a.Status, CreatedBy: a.CreatedBy,
			Reviewer: a.Reviewer, SourceBranch: a.SourceBranch, Target: a.TargetBranch, Top: a.Top,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"count": len(prs), "pullRequests": prs}, nil
	})

	s.AddTool(&mcp.Tool{
		Name:  "get_pull_request",
		Title: "Azure Repos: detalhes da PR",
		Description: "Detalhes de uma pull request: descrição, revisores e votos, work items vinculados, " +
			"políticas (checks) e threads de comentários com arquivo e linha.",
		InputSchema: mcp.Obj(map[string]any{
			"id":              mcp.IntP("ID da pull request"),
			"project":         projectP,
			"includeComments": mcp.BoolP("Incluir as threads de comentários (padrão true)"),
		}, "id"),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			ID              int    `json:"id"`
			Project         string `json:"project"`
			IncludeComments *bool  `json:"includeComments"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return az.PullRequestDetail(ctx, a.Project, a.ID, a.IncludeComments == nil || *a.IncludeComments)
	})

	if cfg.ReadOnly {
		return
	}

	s.AddTool(&mcp.Tool{
		Name:  "create_branch",
		Title: "Azure Repos: criar branch",
		Description: "Cria uma branch no repositório remoto a partir de outra branch (padrão: a branch padrão) ou de um commit, " +
			"opcionalmente vinculada a um work item. Não mexe no clone local: para trabalhar nela, use git fetch e git checkout.",
		InputSchema: mcp.Obj(map[string]any{
			"repository": repoP,
			"project":    projectP,
			"name":       mcp.StrP("Nome da nova branch, ex.: feature/4312-exportar-csv"),
			"from":       mcp.StrP("Opcional: branch ou SHA de commit de origem (padrão: a branch padrão)"),
			"workItemId": mcp.IntP("Opcional: work item a vincular à branch"),
		}, "repository", "name"),
		Annotations: mcp.WriteTool,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Repository string `json:"repository"`
			Project    string `json:"project"`
			Name       string `json:"name"`
			From       string `json:"from"`
			WorkItemID int    `json:"workItemId"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return az.CreateBranch(ctx, a.Project, a.Repository, a.Name, a.From, a.WorkItemID)
	})

	s.AddTool(&mcp.Tool{
		Name:  "create_pull_request",
		Title: "Azure Repos: criar PR",
		Description: "Abre uma pull request. A branch de origem precisa existir no remoto (git push antes). " +
			"targetBranch padrão: a branch padrão do repositório. draft=true abre como rascunho. " +
			"Revisores e work items são opcionais.",
		InputSchema: mcp.Obj(map[string]any{
			"repository":        repoP,
			"project":           projectP,
			"sourceBranch":      mcp.StrP("Branch de origem"),
			"targetBranch":      mcp.StrP("Branch de destino (padrão: a branch padrão)"),
			"title":             mcp.StrP("Título"),
			"description":       mcp.StrP("Descrição em Markdown (até 4000 caracteres)"),
			"draft":             mcp.BoolP("true = rascunho (padrão false)"),
			"reviewers":         mcp.ArrP("string", "Revisores opcionais: e-mail, nome, grupo ou ID"),
			"requiredReviewers": mcp.ArrP("string", "Revisores obrigatórios: e-mail, nome, grupo ou ID"),
			"workItemIds":       mcp.ArrP("integer", "Work items a vincular"),
			"labels":            mcp.ArrP("string", "Tags da PR"),
		}, "repository", "sourceBranch", "title"),
		Annotations: mcp.WriteTool,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Repository        string   `json:"repository"`
			Project           string   `json:"project"`
			SourceBranch      string   `json:"sourceBranch"`
			TargetBranch      string   `json:"targetBranch"`
			Title             string   `json:"title"`
			Description       string   `json:"description"`
			Draft             bool     `json:"draft"`
			Reviewers         []string `json:"reviewers"`
			RequiredReviewers []string `json:"requiredReviewers"`
			WorkItemIDs       []int    `json:"workItemIds"`
			Labels            []string `json:"labels"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return az.CreatePullRequest(ctx, NewPullRequest{
			Project: a.Project, Repository: a.Repository, SourceBranch: a.SourceBranch, TargetBranch: a.TargetBranch,
			Title: a.Title, Description: a.Description, Draft: a.Draft, Reviewers: a.Reviewers,
			Required: a.RequiredReviewers, WorkItemIDs: a.WorkItemIDs, Labels: a.Labels,
		})
	})

	s.AddTool(&mcp.Tool{
		Name:  "update_pull_request",
		Title: "Azure Repos: alterar PR",
		Description: "Altera uma pull request: título, descrição, rascunho (draft=false publica), branch de destino, " +
			"acrescenta revisores e vincula work items. Só envie o que muda.",
		InputSchema: mcp.Obj(map[string]any{
			"id":                   mcp.IntP("ID da pull request"),
			"project":              projectP,
			"title":                mcp.StrP("Novo título"),
			"description":          mcp.StrP("Nova descrição em Markdown (até 4000 caracteres)"),
			"draft":                mcp.BoolP("true = volta para rascunho; false = publica"),
			"targetBranch":         mcp.StrP("Nova branch de destino"),
			"addReviewers":         mcp.ArrP("string", "Revisores opcionais a acrescentar"),
			"addRequiredReviewers": mcp.ArrP("string", "Revisores obrigatórios a acrescentar"),
			"workItemIds":          mcp.ArrP("integer", "Work items a vincular"),
		}, "id"),
		Annotations: mcp.WriteTool,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			ID                   int      `json:"id"`
			Project              string   `json:"project"`
			Title                *string  `json:"title"`
			Description          *string  `json:"description"`
			Draft                *bool    `json:"draft"`
			TargetBranch         *string  `json:"targetBranch"`
			AddReviewers         []string `json:"addReviewers"`
			AddRequiredReviewers []string `json:"addRequiredReviewers"`
			WorkItemIDs          []int    `json:"workItemIds"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return az.UpdatePullRequest(ctx, a.Project, a.ID, PullRequestChanges{
			Title: a.Title, Description: a.Description, Draft: a.Draft, TargetBranch: a.TargetBranch,
			AddReviewers: a.AddReviewers, AddRequired: a.AddRequiredReviewers, WorkItemIDs: a.WorkItemIDs,
		})
	})

	s.AddTool(&mcp.Tool{
		Name:  "add_pull_request_comment",
		Title: "Azure Repos: comentar na PR",
		Description: "Comenta numa pull request: nova thread geral, nova thread numa linha de arquivo (filePath e line) " +
			"ou resposta a uma thread existente (threadId, veja get_pull_request).",
		InputSchema: mcp.Obj(map[string]any{
			"id":       mcp.IntP("ID da pull request"),
			"project":  projectP,
			"text":     mcp.StrP("Texto em Markdown"),
			"threadId": mcp.IntP("Opcional: responde a esta thread"),
			"filePath": mcp.StrP("Opcional: arquivo comentado, ex.: /src/app.go"),
			"line":     mcp.IntP("Opcional: linha do arquivo (versão nova)"),
		}, "id", "text"),
		Annotations: mcp.WriteTool,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			ID       int    `json:"id"`
			Project  string `json:"project"`
			Text     string `json:"text"`
			ThreadID int    `json:"threadId"`
			FilePath string `json:"filePath"`
			Line     int    `json:"line"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return az.AddPullRequestComment(ctx, a.Project, a.ID, a.Text, a.ThreadID, a.FilePath, a.Line)
	})
}
