package azuredevops

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/sidiney/pm-mcp/internal/mcp"
)

var fieldsMapP = map[string]any{
	"type":                 "object",
	"description":          "Opcional: outros campos por reference name, ex.: {\"Custom.Cliente\": \"ACME\"}",
	"additionalProperties": true,
}

// Fields shared by create_work_item and update_work_item.
func workItemEditProps() map[string]any {
	return map[string]any{
		"title":              mcp.StrP("Título"),
		"description":        mcp.StrP("Descrição em texto simples (quebras de linha e listas com '- ' são preservadas)"),
		"acceptanceCriteria": mcp.StrP("Critérios de aceite em texto simples"),
		"assignedTo":         mcp.StrP("\"me\", e-mail ou nome de exibição; \"\" remove o responsável"),
		"sprint":             mcp.StrP("\"current\", nome da sprint (ex.: \"Sprint 42\") ou caminho completo; \"\" volta para o backlog"),
		"team":               mcp.StrP("Time usado para resolver a sprint (padrão: time padrão do projeto)"),
		"areaPath":           mcp.StrP("Area path completo"),
		"priority":           mcp.IntP("Prioridade 1–4"),
		"storyPoints":        mcp.NumP("Story points (processo Agile)"),
		"effort":             mcp.NumP("Effort (processo Scrum)"),
		"originalEstimate":   mcp.NumP("Estimativa original em horas (Task)"),
		"remainingWork":      mcp.NumP("Trabalho restante em horas (Task)"),
		"tags":               mcp.ArrP("string", "Substitui TODAS as tags"),
		"fields":             fieldsMapP,
		"dryRun":             mcp.BoolP("true = só valida no Azure DevOps, sem gravar"),
	}
}

type editArgs struct {
	Title              *string        `json:"title"`
	Description        *string        `json:"description"`
	AcceptanceCriteria *string        `json:"acceptanceCriteria"`
	AssignedTo         *string        `json:"assignedTo"`
	Sprint             *string        `json:"sprint"`
	Team               string         `json:"team"`
	AreaPath           *string        `json:"areaPath"`
	Priority           *int           `json:"priority"`
	StoryPoints        *float64       `json:"storyPoints"`
	Effort             *float64       `json:"effort"`
	OriginalEstimate   *float64       `json:"originalEstimate"`
	RemainingWork      *float64       `json:"remainingWork"`
	Tags               *[]string      `json:"tags"`
	Fields             map[string]any `json:"fields"`
	DryRun             bool           `json:"dryRun"`
}

func (e editArgs) changes() WorkItemChanges {
	return WorkItemChanges{
		Title: e.Title, Description: e.Description, AcceptanceCriteria: e.AcceptanceCriteria,
		AssignedTo: e.AssignedTo, Sprint: e.Sprint, Team: e.Team, AreaPath: e.AreaPath,
		Priority: e.Priority, StoryPoints: e.StoryPoints, Effort: e.Effort,
		OriginalEstimate: e.OriginalEstimate, RemainingWork: e.RemainingWork,
		Tags: e.Tags, Fields: e.Fields,
	}
}

func registerAzDOTools(s *mcp.Server, az *AzDO, cfg *boardsConfig) {
	projectP := mcp.StrP("Projeto (padrão: AZURE_DEVOPS_PROJECT)")
	teamP := mcp.StrP("Time (padrão: AZURE_DEVOPS_TEAM ou o time padrão do projeto)")

	s.AddTool(&mcp.Tool{
		Name:        "azdo_whoami",
		Title:       "Azure DevOps: quem sou eu",
		Description: "Mostra o usuário do Azure DevOps associado ao PAT e os padrões configurados. Use para testar a conexão.",
		InputSchema: mcp.Obj(map[string]any{}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		me, err := az.Me(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"user": me, "organization": az.orgURL, "defaultProject": az.defProject, "defaultTeam": az.defTeam, "readOnly": cfg.ReadOnly}, nil
	})

	s.AddTool(&mcp.Tool{
		Name:        "list_projects",
		Title:       "Azure DevOps: projetos",
		Description: "Lista os projetos da organização.",
		InputSchema: mcp.Obj(map[string]any{}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		p, err := az.Projects(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"projects": p}, nil
	})

	s.AddTool(&mcp.Tool{
		Name:        "list_teams",
		Title:       "Azure DevOps: times",
		Description: "Lista os times de um projeto (indica o time padrão).",
		InputSchema: mcp.Obj(map[string]any{"project": projectP}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		t, err := az.Teams(ctx, a.Project)
		if err != nil {
			return nil, err
		}
		return map[string]any{"teams": t}, nil
	})

	s.AddTool(&mcp.Tool{
		Name:        "list_sprints",
		Title:       "Azure DevOps: sprints",
		Description: "Lista as sprints (iterações) de um time, com datas e se são passadas, atual ou futuras.",
		InputSchema: mcp.Obj(map[string]any{
			"project":   projectP,
			"team":      teamP,
			"timeframe": mcp.EnumP("Filtro (padrão all)", "all", "past", "current", "future"),
		}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Project, Team, Timeframe string
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		sp, project, team, err := az.Sprints(ctx, a.Project, a.Team, a.Timeframe)
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": project, "team": team, "sprints": sp}, nil
	})

	s.AddTool(&mcp.Tool{
		Name:  "get_sprint_board",
		Title: "Azure DevOps: quadro da sprint",
		Description: "Mostra o quadro de uma sprint (padrão: a atual) agrupado por coluna/estado, com responsáveis, " +
			"pai de cada item, story points e trabalho restante. Bom para daily e para ver o andamento.",
		InputSchema: mcp.Obj(map[string]any{
			"project":    projectP,
			"team":       teamP,
			"sprint":     mcp.StrP("\"current\" (padrão), nome ou caminho da sprint"),
			"types":      mcp.ArrP("string", "Opcional: tipos a incluir, ex.: [\"User Story\",\"Bug\"] ou [\"Task\"]"),
			"assignedTo": mcp.StrP("Opcional: \"me\", e-mail/nome ou \"unassigned\""),
		}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Project    string   `json:"project"`
			Team       string   `json:"team"`
			Sprint     string   `json:"sprint"`
			Types      []string `json:"types"`
			AssignedTo string   `json:"assignedTo"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return az.SprintBoard(ctx, a.Project, a.Team, a.Sprint, a.Types, a.AssignedTo)
	})

	s.AddTool(&mcp.Tool{
		Name:  "query_work_items",
		Title: "Azure DevOps: buscar work items",
		Description: "Busca épicos, features, user stories, tasks, bugs etc. por filtros combináveis. " +
			"Sem states, itens fechados/removidos ficam de fora (use includeClosed). " +
			"Para consultas avançadas passe wiql (somente leitura, deve começar com SELECT). Máx. 200 itens.",
		InputSchema: mcp.Obj(map[string]any{
			"project":       projectP,
			"team":          teamP,
			"types":         mcp.ArrP("string", "Ex.: [\"Epic\"], [\"Feature\"], [\"User Story\",\"Bug\"], [\"Task\"]"),
			"states":        mcp.ArrP("string", "Ex.: [\"New\",\"Active\"]"),
			"assignedTo":    mcp.StrP("\"me\", e-mail/nome ou \"unassigned\""),
			"sprint":        mcp.StrP("\"current\", nome ou caminho da sprint"),
			"areaPath":      mcp.StrP("Area path (inclui sub-áreas)"),
			"parentId":      mcp.IntP("Só filhos diretos deste item (ex.: features de um épico, tasks de uma US)"),
			"titleContains": mcp.StrP("Texto contido no título"),
			"tag":           mcp.StrP("Tag"),
			"includeClosed": mcp.BoolP("Incluir Closed/Done/Removed"),
			"top":           mcp.IntP("Máximo de itens (padrão 100, máx. 200)"),
			"wiql":          mcp.StrP("Consulta WIQL completa; ignora os demais filtros"),
		}),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Project       string   `json:"project"`
			Team          string   `json:"team"`
			Types         []string `json:"types"`
			States        []string `json:"states"`
			AssignedTo    string   `json:"assignedTo"`
			Sprint        string   `json:"sprint"`
			AreaPath      string   `json:"areaPath"`
			ParentID      int      `json:"parentId"`
			TitleContains string   `json:"titleContains"`
			Tag           string   `json:"tag"`
			IncludeClosed bool     `json:"includeClosed"`
			Top           int      `json:"top"`
			WIQL          string   `json:"wiql"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return az.Query(ctx, WorkItemFilter{
			Project: a.Project, Team: a.Team, Types: a.Types, States: a.States, AssignedTo: a.AssignedTo,
			Sprint: a.Sprint, AreaPath: a.AreaPath, ParentID: a.ParentID, TitleContains: a.TitleContains,
			Tag: a.Tag, IncludeClosed: a.IncludeClosed, Top: a.Top, WIQL: a.WIQL,
		})
	})

	s.AddTool(&mcp.Tool{
		Name:  "get_work_item",
		Title: "Azure DevOps: detalhes do item",
		Description: "Detalhes de um work item: descrição, critérios de aceite, pai, filhos, outros links e comentários recentes. " +
			"allFields=true traz todos os campos (útil para descobrir reference names de campos customizados).",
		InputSchema: mcp.Obj(map[string]any{
			"id":              mcp.IntP("ID do work item"),
			"includeComments": mcp.BoolP("Incluir comentários (padrão true)"),
			"allFields":       mcp.BoolP("Incluir todos os campos brutos"),
		}, "id"),
		Annotations: mcp.ReadOnly,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			ID              int   `json:"id"`
			IncludeComments *bool `json:"includeComments"`
			AllFields       bool  `json:"allFields"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		withComments := a.IncludeComments == nil || *a.IncludeComments
		return az.WorkItemDetail(ctx, a.ID, withComments, a.AllFields)
	})

	if cfg.ReadOnly {
		return
	}

	createProps := workItemEditProps()
	createProps["project"] = projectP
	createProps["type"] = mcp.StrP("Tipo: Epic, Feature, User Story, Product Backlog Item, Task, Bug…")
	createProps["parentId"] = mcp.IntP("Opcional: ID do item pai (ex.: a US de uma Task, o épico de uma Feature)")
	s.AddTool(&mcp.Tool{
		Name:  "create_work_item",
		Title: "Azure DevOps: criar work item",
		Description: "Cria um work item (épico, feature, US, task, bug…), opcionalmente já vinculado ao pai e numa sprint. " +
			"Confirme título, tipo e pai com o usuário antes. Use dryRun=true para validar sem criar.",
		InputSchema: mcp.Obj(createProps, "type", "title"),
		Annotations: mcp.WriteTool,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			editArgs
			Project  string `json:"project"`
			Type     string `json:"type"`
			ParentID int    `json:"parentId"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		ch := a.changes()
		if a.ParentID > 0 {
			ch.ParentID = &a.ParentID
		}
		return az.CreateWorkItem(ctx, a.Project, a.Type, ch, a.DryRun)
	})

	updateProps := workItemEditProps()
	updateProps["id"] = mcp.IntP("ID do work item")
	updateProps["state"] = mcp.StrP("Novo estado (ex.: New, Active, Resolved, Closed, Removed / To Do, In Progress, Done)")
	updateProps["reason"] = mcp.StrP("Opcional: motivo da mudança de estado")
	updateProps["completedWork"] = mcp.NumP("Trabalho concluído em horas (Task)")
	updateProps["addTags"] = mcp.ArrP("string", "Tags a acrescentar")
	updateProps["removeTags"] = mcp.ArrP("string", "Tags a remover")
	updateProps["parentId"] = mcp.IntP("Novo pai; 0 remove o vínculo com o pai")
	s.AddTool(&mcp.Tool{
		Name:  "update_work_item",
		Title: "Azure DevOps: alterar work item",
		Description: "Altera um work item: estado (mover no quadro), responsável, sprint, título, descrição, estimativas, tags, pai e campos customizados. " +
			"Só envie o que muda. Falha com segurança se o item foi alterado por outra pessoa no meio tempo. dryRun=true só valida.",
		InputSchema: mcp.Obj(updateProps, "id"),
		Annotations: mcp.WriteTool,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			editArgs
			ID            int      `json:"id"`
			State         *string  `json:"state"`
			Reason        *string  `json:"reason"`
			CompletedWork *float64 `json:"completedWork"`
			AddTags       []string `json:"addTags"`
			RemoveTags    []string `json:"removeTags"`
			ParentID      *int     `json:"parentId"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		if a.ID <= 0 {
			return nil, errors.New("id é obrigatório")
		}
		ch := a.changes()
		ch.State, ch.Reason, ch.CompletedWork = a.State, a.Reason, a.CompletedWork
		ch.AddTags, ch.RemoveTags, ch.ParentID = a.AddTags, a.RemoveTags, a.ParentID
		return az.UpdateWorkItem(ctx, a.ID, ch, a.DryRun)
	})

	s.AddTool(&mcp.Tool{
		Name:        "add_work_item_comment",
		Title:       "Azure DevOps: comentar",
		Description: "Adiciona um comentário (discussão) a um work item.",
		InputSchema: mcp.Obj(map[string]any{
			"id":   mcp.IntP("ID do work item"),
			"text": mcp.StrP("Texto do comentário"),
		}, "id", "text"),
		Annotations: mcp.WriteTool,
	}, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			ID   int    `json:"id"`
			Text string `json:"text"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return az.AddComment(ctx, a.ID, a.Text)
	})
}
