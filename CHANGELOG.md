# Changelog

Todas as mudanças relevantes do DevPulse ficam registradas aqui.

O formato segue o [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/) e o projeto usa [Versionamento Semântico](https://semver.org/lang/pt-BR/). Enquanto a versão for `0.x`, qualquer versão MINOR pode trazer mudanças incompatíveis (em tools, variáveis de ambiente ou no instalador), e elas são sempre listadas em **Alterado** ou **Removido**.

## [Não lançado]

## [0.2.0] - 2026-09-30

Quem está na 0.1.0 atualiza rodando de novo o script de instalação (ou baixando a release). A partir da 0.2.0, basta `devpulse update`.

### Adicionado

- Instalação em uma linha: `install.sh` (macOS/Linux) e `install.ps1` (Windows). Eles baixam a release do sistema, conferem o SHA256 e rodam o `devpulse install`. Também são publicados em cada release.
- `devpulse update`:
  - baixa a versão mais recente, ou a pedida com `--version`, e confere o SHA256;
  - testa o novo executável antes de trocar o de cada app e deixa o anterior como `.old`;
  - `--check` só informa se há versão nova.
- O instalador oferece adicionar a pasta do executável ao `PATH`: no Windows, no PATH do usuário; nos shells zsh, bash e fish, no arquivo de perfil. `--no-path` pula esse passo.
- `Guide.Show` nos providers: o passo a passo de um campo pode aparecer sempre antes da pergunta (`GuideAlways`).

### Alterado

- O passo a passo para gerar o PAT do Azure DevOps e o token do 7pace agora aparece antes da pergunta do arquivo, mesmo quando o arquivo já existe. Antes, só aparecia depois do Enter e se o arquivo não existisse.

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

[Não lançado]: https://github.com/sidyjw/devpulse/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/sidyjw/devpulse/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/sidyjw/devpulse/releases/tag/v0.1.0
