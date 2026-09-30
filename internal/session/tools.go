package session

import (
	"context"
	"encoding/json"

	"github.com/sidyjw/devpulse/internal/mcp"
)

// Register adds the session_time tool.
func (t *Tracker) Register(s *mcp.Server) {
	s.AddTool(&mcp.Tool{
		Name:  "session_time",
		Title: "Tempo da sessão",
		Description: "Quanto tempo passou desde o início desta sessão (ou desde since) e quais outras sessões rodaram no mesmo período, " +
			"para sugerir quanto lançar. Não lança nada: apresente como sugestão e deixe o usuário decidir, principalmente " +
			"se houver sobreposição com outras sessões. O início é o do servidor MCP: em clientes que mantêm o servidor " +
			"aberto entre conversas (ex.: Claude Desktop), informe since.",
		InputSchema: mcp.Obj(map[string]any{
			"since": mcp.StrP("Opcional: início do período, HH:MM (hoje), AAAA-MM-DDTHH:MM ou RFC 3339 (padrão: início da sessão)"),
		}),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, // local only: no API is called
	}, func(_ context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Since string `json:"since"`
		}
		if err := mcp.DecodeArgs(raw, &a); err != nil {
			return nil, err
		}
		since, err := ParseSince(a.Since, t.now())
		if err != nil {
			return nil, err
		}
		return t.Report(since)
	})
}
