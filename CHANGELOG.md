# Changelog

Todas as mudanças relevantes do DevPulse ficam registradas aqui.

O formato segue o [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/) e o projeto usa [Versionamento Semântico](https://semver.org/lang/pt-BR/). Enquanto a versão for `0.x`, qualquer versão MINOR pode trazer mudanças incompatíveis (em tools, variáveis de ambiente ou no instalador), e elas são sempre listadas em **Alterado** ou **Removido**.

## [Não lançado]

## [0.1.0] - 2026-09-29

Primeira release pública.

### Adicionado

- Servidor MCP local (stdio), só com a biblioteca padrão do Go.
- Provider Azure DevOps com dois componentes independentes:
  - **Boards**: sprints, quadro da sprint, busca, leitura, criação, edição e comentários em work items.
  - **7pace Timetracker**: lançar, corrigir e resumir horas, com proteção contra lançamento duplicado e lote validado antes de enviar.
- Modos somente leitura (`SEVENPACE_READ_ONLY`, `AZURE_DEVOPS_READ_ONLY`) e exclusão de lançamentos desligada por padrão.
- `devpulse install`, `uninstall` e `detect`: instalador guiado para Claude Code, Claude Desktop, Claude Cowork e clientes MCP genéricos, com menus por setas, backup dos arquivos alterados e tokens gravados em arquivos protegidos.
- Migração automática das instalações antigas `pm-mcp` e `7pace`.
- `devpulse -check` para validar a configuração e testar as conexões.
- Binários para Windows, macOS e Linux (amd64 e arm64), com `SHA256SUMS.txt` e atestado de proveniência.

[Não lançado]: https://github.com/sidyjw/devpulse/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/sidyjw/devpulse/releases/tag/v0.1.0
