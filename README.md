# DevPulse

Servidor MCP **local** que conecta clientes de IA (Claude Code, Claude Desktop, Cowork e qualquer cliente MCP) às suas **ferramentas de gestão de projetos**.

O servidor é organizado em **providers** (a ferramenta de gestão) e **componentes** (os módulos dessa ferramenta). Você liga só os componentes que usa:

| Provider | Componente | O que faz |
|---|---|---|
| Azure DevOps | **Boards** | Sprints, quadro, épicos, features, user stories, tasks e bugs |
| Azure DevOps | **7pace Timetracker** | Lançar, corrigir e resumir horas |

Outras ferramentas (Jira, GitLab…) entram como um novo provider. Veja [Adicionando um provider](#adicionando-um-provider).

Nasceu como `7pace-mcp`, uma reescrita em Go do [turnono/7pace-mcp-server](https://github.com/turnono/7pace-mcp-server) com foco em segurança e correção, e depois se chamou `pm-mcp`. Quem vem de uma dessas versões deve ler a [migração](#migrando-do-pm-mcp-ou-do-7pace-mcp).

## Por que esta versão é mais segura

| | Original (Node) | Esta versão (Go) |
|---|---|---|
| Dependências | express, axios, cors, dotenv, SDK MCP e dependências transitivas | **nenhuma**: só a biblioteca padrão do Go (`go version -m` confirma) |
| Token pelo chat | tool `configure_sevenpace` recebe o token como argumento | **não existe**: o token vem só de variável de ambiente ou arquivo |
| Modo HTTP | servidor web com CORS `*` e token na query string | **só stdio**: nenhuma porta aberta |
| Sem credenciais | `log_time` responde "✅" sem lançar nada | falha ao iniciar, com mensagem clara |
| Redirecionamentos | seguidos | **bloqueados**: o token só vai para o host configurado |
| Token em erros | pode aparecer | removido de toda mensagem de erro (`[REDACTED]`) |
| Tipo de atividade inválido | ignorado em silêncio | erro com a lista de opções válidas |
| Lançamento duplicado | lança de novo | **recusado** (mesmo dia, item, duração e comentário) |
| Lote de lançamentos | não tem | valida **tudo** antes de enviar o primeiro |
| Exclusão | sempre disponível | desligada por padrão (`SEVENPACE_ENABLE_DELETE`) |
| Somente leitura | não tem | `SEVENPACE_READ_ONLY` / `AZURE_DEVOPS_READ_ONLY` |
| Edição concorrente no Boards | não tem | usa a revisão do item (`test /rev`): não sobrescreve alteração de outra pessoa |
| Instalação | editar JSON à mão | `devpulse install`: faz backup, preserva o resto do arquivo e o token vai da área de transferência direto para um arquivo protegido |

Bugs de API corrigidos, conforme a [documentação oficial da 7pace](https://github.com/7pace/timetracker-rest-api-samplecode):

- A listagem usava `from`/`to`, mas o correto é `$fromTimestamp`/`$toTimestamp`, com paginação `$count`/`$skip`.
- O update usava `PUT`, mas a API usa `PATCH`.
- A lista de tipos de atividade sempre vinha vazia, porque a resposta é `data.activityTypes[]`.
- O endpoint `/reports/time` não existe. Ele foi substituído por um resumo calculado localmente (`time_summary`).

## Tools

**Azure DevOps / 7pace Timetracker**

| Tool | O que faz |
|---|---|
| `sevenpace_whoami` | Testa a conexão e mostra seu usuário |
| `list_activity_types` | Tipos de atividade (Development, Testing…) |
| `get_worklogs` | Seus lançamentos num período (padrão: últimos 7 dias) |
| `time_summary` | Horas por dia e por item, com os **dias úteis em que faltam horas** (com o Boards ligado, mostra também o título dos itens) |
| `log_time` | Lança um período (com proteção contra duplicidade) |
| `log_time_batch` | Lança vários de uma vez (até 60), por exemplo a semana |
| `update_worklog` | Corrige um lançamento |
| `delete_worklog` | Exclui um lançamento (só com `SEVENPACE_ENABLE_DELETE=true`) |

**Azure DevOps / Boards**

| Tool | O que faz |
|---|---|
| `azdo_whoami` | Testa a conexão |
| `list_projects`, `list_teams` | Projetos e times |
| `list_sprints` | Sprints passadas, atual e futuras, com datas |
| `get_sprint_board` | Quadro da sprint por coluna/estado, com responsáveis e trabalho restante |
| `query_work_items` | Busca por tipo, estado, responsável, sprint, área, pai, título, tag ou WIQL |
| `get_work_item` | Detalhes: descrição, critérios de aceite, pai, filhos, links e comentários |
| `create_work_item` | Cria Epic, Feature, User Story, Task, Bug… já com pai e sprint |
| `update_work_item` | Estado, responsável, sprint, estimativas, tags, pai, campos customizados |
| `add_work_item_comment` | Comenta num item |

Excluir work items não é suportado de propósito. Para isso, use `state: "Removed"`.

---

## Instalação rápida

### 1. Compile (precisa do [Go](https://go.dev/dl/) 1.22 ou superior)

```powershell
# Windows (PowerShell), dentro da pasta do projeto
go build -trimpath -ldflags "-s -w" -o devpulse.exe .
```

```bash
# macOS / Linux
go build -trimpath -ldflags "-s -w" -o devpulse .
```

### 2. Rode o instalador

```bash
./devpulse install
```

O assistente faz isto:

1. **Detecta o sistema** (Windows, macOS, Linux/WSL) e as harnesses instaladas.
2. Pergunta a **harness** e **em quais apps** instalar. No Claude: Claude Code, Claude Desktop e/ou Claude Cowork. No Claude Code, também pergunta o **escopo**: `user`, `local` ou `project`.
3. Mostra o **arquivo de configuração padrão** de cada app. Tecle Enter para aceitar ou digite outro caminho se o seu ambiente é customizado.
4. Pergunta a **ferramenta de gestão** e os **componentes** que você quer integrar.
5. Pergunta só as configurações desses componentes, cada uma com um valor padrão. As opções avançadas (somente leitura, exclusão, time padrão, tempo limite…) ficam atrás de uma pergunta.
6. Para cada token: se o arquivo não existe, mostra o passo a passo para gerá-lo (com o link da sua organização) e oferece criá-lo. **Copie o token (Ctrl+C) e tecle Enter.** O instalador lê a área de transferência, grava um arquivo que só você pode ler (`chmod 600` ou `icacls`) e limpa a área de transferência. O token nunca aparece na tela nem no histórico do terminal.
7. Copia o executável para um lugar fixo: `%LOCALAPPDATA%\Programs\devpulse\` no Windows ou `~/.local/bin/` no macOS/Linux. Esse lugar também pode ser alterado.
8. Mostra um **resumo**, roda o `-check` com a configuração nova e só então grava. Antes de alterar um arquivo existente, faz backup dele (`*.bak-AAAAMMDD-HHMMSS`) e mantém tudo o que não é do servidor.

Nos menus e nas perguntas de sim/não, navegue com as **setas**: ↑/↓ movem, **Espaço** marca nas listas de múltipla escolha, ←/→ alternam entre Sim e Não, **Enter** confirma e **Ctrl+C** cancela sem alterar nada. A linha de ajuda aparece embaixo de cada pergunta. Fora de um terminal (por exemplo, com a entrada vinda de um pipe), o instalador aceita as respostas digitadas por número.

Se já existir uma instalação, os valores dela viram o padrão. Isso vale também para as entradas antigas `pm-mcp` e `7pace`, e o instalador oferece removê-las.

Onde cada app é configurado:

| App | Como |
|---|---|
| Claude Code | `claude mcp add-json` quando o CLI está no PATH; senão edita `~/.claude.json` (escopos user/local) ou `.mcp.json` (escopo project) |
| Claude Desktop | `claude_desktop_config.json`: `%APPDATA%\Claude\` no Windows (na instalação da Microsoft Store, o arquivo virtualizado em `%LOCALAPPDATA%\Packages\Claude_*\LocalCache\Roaming\Claude\`) e `~/Library/Application Support/Claude/` no macOS |
| Claude Cowork | Usa os servidores do Claude Desktop, por meio da ponte do Desktop. O arquivo é o mesmo e é gravado uma vez só. |
| Outra (genérica) | Mostra a configuração pronta em JSON (`mcpServers`, usada pela maioria dos clientes), no formato do VS Code (`servers`) e em TOML (Codex). Com `--config`, grava num arquivo JSON qualquer. |

### Outros comandos

```bash
devpulse detect      # SO, apps detectados, arquivos de configuração e instalações existentes
devpulse uninstall   # remove a entrada (com backup); não apaga o executável nem os tokens
devpulse -check      # testa a configuração do ambiente atual
```

### Modo não interativo

Todas as perguntas têm uma flag equivalente. Com `--yes`, os valores padrão são aceitos sem perguntar. Veja `devpulse install -h`.

```bash
devpulse install --yes --harness claude --app code,desktop --scope user \
  --provider azuredevops --components boards,sevenpace \
  --set SEVENPACE_ORGANIZATION=minhaorg \
  --set SEVENPACE_TOKEN_FILE=~/.devpulse/7pace-token \
  --set AZURE_DEVOPS_ORG_URL=https://dev.azure.com/minhaorg \
  --set AZURE_DEVOPS_PAT_FILE=~/.devpulse/azdo-pat \
  --set AZURE_DEVOPS_PROJECT="Meu Projeto"
```

| Flag | Para quê |
|---|---|
| `--dry-run` | Mostra tudo o que seria feito, sem gravar nada |
| `--config <arquivo>` / `--key <a.b>` | Grava num arquivo e numa chave diferentes do padrão (ex.: `--harness generic --config ~/.cursor/mcp.json`) |
| `--bin-dir <pasta>` / `--no-copy` | Muda o destino do executável, ou usa o executável de onde ele está |
| `--name <nome>` | Nome da entrada (padrão `devpulse`) |
| `--advanced` | Pergunta também as opções avançadas |
| `--force` | Substitui entradas existentes sem perguntar |
| `--skip-check` | Não roda o `-check` antes de gravar |

## Gerando os tokens

**7pace**: no Azure DevOps, abra o **7pace Timetracker** e vá em **Settings → API & Reporting** para criar um token. A organização é o prefixo da URL. Por exemplo, em `https://minhaorg.timehub.7pace.com`, ela é `minhaorg`.

**Azure DevOps (Boards)**: em `https://dev.azure.com/<org>`, abra o ícone de usuário e depois **Personal access tokens → New Token**. Em **Scopes → Custom defined**, marque:
- **Work Items: Read & write**. Se quiser só consultar, basta **Read**.
- **Project and Team: Read**

Escolha uma validade curta, como 90 dias.

Não cole tokens no chat da IA. O instalador cuida de gravá-los.

## Instalação manual

Se preferir não usar o instalador:

1. Salve cada token num arquivo só seu.

   ```powershell
   # Windows: copie o token (Ctrl+C) e rode
   $dir  = Join-Path $env:USERPROFILE ".devpulse"
   $file = Join-Path $dir "7pace-token"   # ou "azdo-pat"
   New-Item -ItemType Directory -Force $dir | Out-Null
   [IO.File]::WriteAllText($file, (Get-Clipboard -Raw).Trim())
   icacls $file /inheritance:r /grant:r "$($env:USERNAME):(F)"
   Set-Clipboard -Value " "
   ```

   ```bash
   # macOS / Linux
   mkdir -p ~/.devpulse && printf '%s' 'COLE_O_TOKEN_AQUI' > ~/.devpulse/7pace-token && chmod 600 ~/.devpulse/7pace-token
   ```

2. Adicione o servidor ao seu cliente. Este é o formato `mcpServers`, usado pelo Claude Desktop, pelo `.mcp.json` e pela maioria dos clientes:

   ```json
   {
     "mcpServers": {
       "devpulse": {
         "command": "C:\\Users\\SEU_USUARIO\\AppData\\Local\\Programs\\devpulse\\devpulse.exe",
         "env": {
           "SEVENPACE_ORGANIZATION": "minhaorg",
           "SEVENPACE_TOKEN_FILE": "C:\\Users\\SEU_USUARIO\\.devpulse\\7pace-token",
           "AZURE_DEVOPS_ORG_URL": "https://dev.azure.com/minhaorg",
           "AZURE_DEVOPS_PAT_FILE": "C:\\Users\\SEU_USUARIO\\.devpulse\\azdo-pat",
           "AZURE_DEVOPS_PROJECT": "Meu Projeto"
         }
       }
     }
   }
   ```

3. Teste com `devpulse -check` usando as mesmas variáveis. A saída esperada é parecida com esta:

   ```
   Azure DevOps: https://dev.azure.com/minhaorg
     ✓ conectado como Sidiney (sidiney@empresa.com)
     ✓ 5 projetos visíveis
     ✓ time "Meu Projeto Team", sprint atual: Sprint 42 (2026-09-21 a 2026-10-02)
     somente leitura: false
   7pace: https://minhaorg.timehub.7pace.com/api
     ✓ conectado como Sidiney (sidiney@empresa.com)
     ✓ 8 tipos de atividade
     somente leitura: false | exclusão habilitada: false
   ```

## Variáveis de ambiente

Um componente é ativado quando suas variáveis obrigatórias estão definidas. É preciso configurar ao menos um componente.

**Azure DevOps / Boards**

| Variável | Obrigatória | Descrição |
|---|---|---|
| `AZURE_DEVOPS_ORG_URL` | sim | `https://dev.azure.com/minhaorg` |
| `AZURE_DEVOPS_PAT_FILE` ou `AZURE_DEVOPS_PAT` | sim | Arquivo com o PAT (recomendado) ou o PAT direto |
| `AZURE_DEVOPS_PROJECT` | não | Projeto padrão (evita repetir em toda pergunta) |
| `AZURE_DEVOPS_TEAM` | não | Time padrão (se não definido, usa o time padrão do projeto) |
| `AZURE_DEVOPS_READ_ONLY` | não | `true` esconde as tools que criam ou alteram work items |

**Azure DevOps / 7pace Timetracker**

| Variável | Obrigatória | Descrição |
|---|---|---|
| `SEVENPACE_ORGANIZATION` | sim | Org do 7pace (`minhaorg` em `minhaorg.timehub.7pace.com`) |
| `SEVENPACE_TOKEN_FILE` ou `SEVENPACE_TOKEN` | sim | Arquivo com o token (recomendado) ou o token direto |
| `SEVENPACE_BASE_URL` | não | Substitui a URL da API (precisa ser https) |
| `SEVENPACE_READ_ONLY` | não | `true` esconde as tools que lançam ou alteram horas |
| `SEVENPACE_ENABLE_DELETE` | não | `true` expõe `delete_worklog` |

**Geral**

| Variável | Obrigatória | Descrição |
|---|---|---|
| `DEVPULSE_HTTP_TIMEOUT` | não | Tempo limite por requisição (padrão `30s`, máx. `5m`). Os nomes antigos `PM_MCP_HTTP_TIMEOUT` e `SEVENPACE_HTTP_TIMEOUT` continuam aceitos. |

## Migrando do pm-mcp ou do 7pace-mcp

- As variáveis dos componentes e os nomes das tools não mudaram. Uma config antiga funciona trocando só o `command` pelo novo executável.
- O jeito mais fácil é rodar `devpulse install`. Ele encontra as entradas antigas (`pm-mcp` e `7pace`), usa os valores delas como padrão, cria a entrada `devpulse` e oferece remover as antigas. Se a config antiga tinha o token direto no JSON, ele oferece mover o token para um arquivo protegido.
- Os arquivos de token existentes continuam sendo usados onde estão (`~/.pm-mcp/`, `~/.7pace/`); não é preciso gerar tokens novos. Instalações novas usam `~/.devpulse/`.
- O executável antigo (ex.: `%LOCALAPPDATA%\Programs\pm-mcp\pm-mcp.exe`) não é apagado. O instalador avisa quando ele deixa de ser usado; apague-o se nenhum outro app o usa.
- `PM_MCP_HTTP_TIMEOUT` e `SEVENPACE_HTTP_TIMEOUT` viram `DEVPULSE_HTTP_TIMEOUT`, mas os nomes antigos continuam funcionando.
- `devpulse uninstall` e `devpulse detect` também encontram as entradas antigas.

## Exemplos de uso (é só conversar)

- "Quanto falta lançar esta semana?"
- "Quais tasks estão comigo na sprint atual?"
- "Lança 2h ontem na 4312 (revisão do PR de autenticação) e 6h na 4290 (implementação da API de pedidos)."
- "Distribui as 8h de sexta entre minhas tasks ativas, proporcional ao que fiz na semana." O assistente monta a proposta e pede sua confirmação antes de lançar.
- "Corrige o lançamento de terça da 4312 para 1h30."
- "Mostra o quadro da sprint."
- "Cria uma US 'Exportar pedidos em CSV' no épico 3900, na sprint atual, com 5 pontos, e quebra em 3 tasks de 4h."
- "Move a 4312 para Done e zera o remaining."
- "Lista os épicos ativos e as features de cada um."

Antes de qualquer escrita, o assistente mostra o que vai fazer. O Claude Desktop também pede sua permissão a cada chamada de tool que grava.

## Estrutura do código

```
main.go                          # serve | -check | -version | install | uninstall | detect
internal/mcp/                    # servidor MCP (JSON-RPC sobre stdio) e helpers de schema
internal/httpx/                  # cliente HTTPS: sem redirect, limite de resposta, token removido dos erros
internal/settings/               # leitura/validação de variáveis e arquivos de segredo
internal/provider/               # contratos Provider / Component / Setting e a ativação
internal/providers/registry.go   # lista de providers desta versão
internal/providers/azuredevops/  # provider Azure DevOps: componentes Boards e 7pace
internal/install/                # instalador: harnesses, apps, edição de config, prompts
```

## Adicionando um provider

1. Crie `internal/providers/<nome>/` com uma função que devolva um `provider.Provider`, com um `provider.Component` para cada módulo. Cada componente declara:
   - `Settings`: as variáveis que ele lê, com rótulo, ajuda, tipo (`String`, `URL`, `SecretFile`, `Bool`, `Duration`), se é obrigatória ou avançada, o valor padrão e a **mesma** função de validação usada pelo servidor. O instalador monta as perguntas a partir disso. Opcionalmente, `Guide` traz um passo a passo de onde obter o valor, e os passos podem citar respostas anteriores como `{VARIAVEL|alternativa}`. Nos tokens, o guia só aparece quando o arquivo ainda precisa ser criado.
   - `Enabled`: quando o ambiente liga o componente.
   - `Build`: cria a instância. `bc.Get("<provider>.<componente>")` dá acesso a componentes construídos antes.
   - `Instructions`: regras que entram nas instructions do servidor MCP.
   - A instância implementa `Register(*mcp.Server)` (as tools) e `Check(ctx, w)` (o `-check`).
2. Adicione o provider em `internal/providers/registry.go`.
3. Use um prefixo nos nomes das tools (ex.: `jira_…`). Um nome repetido derruba o servidor na inicialização.

Uma harness nova (Cursor, VS Code, Codex…) segue o mesmo modelo: implemente `install.Harness`/`install.App` num arquivo em `internal/install/` e adicione em `Harnesses()`.

## Testes

```bash
go test -race ./...
```

Os testes não acessam a rede:
- **Servidor e providers**: usam servidores 7pace e Azure DevOps falsos (`httptest`). Verificam payloads, parâmetros da API, PATCH, bloqueio de duplicidade, validação de lote, bloqueio de redirect, remoção do token de mensagens de erro, limite de tamanho de resposta, escape de WIQL, configuração e ativação dos componentes.
- **Instalador**: roda em diretórios temporários com comandos externos simulados. Cobre a edição de JSON (preserva chaves e ordem, faz backup, é idempotente, recusa JSON com comentários), os caminhos por sistema (incluindo o Claude Desktop MSIX), o uso seguro do CLI `claude`, os fluxos interativo e não interativo, o dry-run, a migração da entrada `7pace` e a gravação do token.

## Limitações

- 7pace **Azure DevOps Services** (nuvem). O 7pace do Azure DevOps Server on-premises usa autenticação Windows (NTLM), que não é suportada.
- O campo de coluna do Kanban (`WEF_..._Kanban.Column`) muda por quadro. O `update_work_item` move o item pelo **estado**. Para mover para uma coluna específica, descubra o nome do campo com `get_work_item` (`allFields: true`) e use `fields`.
- Os horários são enviados como hora local, sem fuso (`AAAA-MM-DDTHH:MM:SS`), como na documentação da 7pace. O padrão de início é 09:00.
- O instalador não reescreve arquivos com comentários (JSONC, como o `settings.json` do Zed ou do VS Code). Nesses casos, ele mostra o trecho para colar.
- Se o `claude` no Windows for o atalho `.cmd` do npm, o instalador edita o `~/.claude.json` direto em vez de chamar o CLI, porque o `cmd.exe` corrompe argumentos JSON. Feche as sessões do Claude Code antes de instalar.

## Licença

[MIT](LICENSE)
