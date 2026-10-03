package ponto

import (
	"context"
	"encoding/json"

	"github.com/sidyjw/devpulse/internal/mcp"
)

// Register adds the get_punches tool.
func (r *Reader) Register(s *mcp.Server) {
	s.AddTool(&mcp.Tool{
		Name:  "get_punches",
		Title: "Ponto: marcações",
		Description: "Marcações de ponto (entradas e saídas) por dia, com os intervalos trabalhados e o total. " +
			"Vêm de arquivos locais gravados pela integração de ponto (ex.: a extensão do Senior X); nada é consultado na rede. " +
			"Use como o tempo trabalhado do dia ao propor lançamentos de horas. status: complete (todos os intervalos fechados; " +
			"com 2 batidas pode ser só a saída para o almoço), open (hoje, último intervalo em aberto e contado até agora), " +
			"missing_punch (dia passado com batida faltando), no_punches (sincronizado, sem batidas: folga, feriado, férias), " +
			"not_synced (sem arquivo) e invalid.",
		InputSchema: mcp.Obj(map[string]any{
			"startDate": mcp.StrP("AAAA-MM-DD (padrão: hoje)"),
			"endDate":   mcp.StrP("AAAA-MM-DD (padrão: startDate; máx. 31 dias, não pode ser futuro)"),
		}),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, // local files only
	}, func(_ context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			StartDate string `json:"startDate"`
			EndDate   string `json:"endDate"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		from, to, err := r.ParseRange(a.StartDate, a.EndDate)
		if err != nil {
			return nil, err
		}
		return r.Report(from, to)
	})
}
