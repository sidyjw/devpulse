# DevPulse

Servidor MCP **local** que conecta clientes de IA (Claude Code, Claude Desktop, Cowork e qualquer cliente MCP) às suas **ferramentas de gestão de projetos**.

O servidor é organizado em **providers** (a ferramenta de gestão) e **componentes** (os módulos dessa ferramenta). Você liga só os componentes que usa:

| Provider | Componente | O que faz |
|---|---|---|
| Azure DevOps | **Boards** | Sprints, quadro, épicos, features, user stories, tasks e bugs |
| Azure DevOps | **Repos** | Repositórios, branches, políticas de branch e pull requests |
| Azure DevOps | **7pace Timetracker** | Lançar, corrigir e resumir horas |

O DevPulse oferece as ferramentas e não impõe um fluxo de trabalho: convenções de branch, quando abrir uma PR ou quanto tempo lançar ficam com você e com a sua harness.

Outras ferramentas (Jira, GitLab…) entram como um novo provider. Veja [Adicionando um provider](#adicionando-um-provider).

## Início rápido

**macOS / Linux:**

```bash
curl -fsSL https://raw.githubusercontent.com/sidyjw/devpulse/main/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/sidyjw/devpulse/main/install.ps1 | iex
```

O script baixa a última release do seu sistema, confere o SHA256 com o `SHA256SUMS.txt` publicado e abre o [assistente de instalação](#2-rode-o-instalador). Depois, `devpulse update` mantém tudo atualizado. Veja também as [outras formas de instalar](#1-baixe-ou-compile).

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
| Somente leitura | não tem | `SEVENPACE_READ_ONLY` / `AZURE_DEVOPS_READ_ONLY` / `AZURE_DEVOPS_REPOS_READ_ONLY` |
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
| `get_work_item_updates` | Histórico de revisões: quem mudou o quê e quando, filtrável por pessoa e período. Marca as revisões que só mexeram em Completed/Remaining Work (`timeTrackingOnly`) |
| `create_work_item` | Cria Epic, Feature, User Story, Task, Bug… já com pai e sprint |
| `update_work_item` | Estado, responsável, sprint, estimativas, tags, pai, campos customizados |
| `add_work_item_comment` | Comenta num item |

Excluir work items não é suportado de propósito. Para isso, use `state: "Removed"`.

**Azure DevOps / Repos**

O Repos cobre o lado do servidor. Commits e push continuam com o `git` da sua máquina. Nas tools, `repository` aceita o nome, o ID ou a URL do remoto (`git remote get-url origin`); um remoto de outra organização é recusado antes de qualquer requisição.

| Tool | O que faz |
|---|---|
| `list_repositories`, `get_repository` | Repositórios do projeto, com a branch padrão e as URLs de clone |
| `list_branches` | Branches remotas, com o commit de cada uma |
| `get_branch_policies` | Políticas de uma branch (revisores, build, work item…) e se ela só aceita mudanças por PR |
| `list_pull_requests` | PRs por status, autor, revisor e branches |
| `get_pull_request` | Detalhes: revisores e votos, work items, checks das políticas e threads de comentários |
| `list_pushes` | Pushes de uma pessoa num período, com as branches e os commits de cada um, num repositório ou em todos os do projeto. Mostra também o trabalho em branches ainda não mescladas |
| `list_pull_request_activity` | O que uma pessoa fez em PRs num período: PRs criadas, votos, comentários, novos commits e mudanças de status, com os work items de cada PR |
| `create_branch` | Cria uma branch remota a partir de outra branch ou de um commit, opcionalmente vinculada a um work item |
| `create_pull_request` | Abre uma PR (ou um rascunho), com revisores, work items e tags |
| `update_pull_request` | Título, descrição, rascunho/publicada, branch de destino, novos revisores e work items |
| `add_pull_request_comment` | Comenta na PR, numa linha de arquivo ou responde a uma thread |

Excluir branches e completar ou abandonar PRs não são suportados. As regras de cada branch (por exemplo, exigir PR na `main`) ficam nas políticas do Azure DevOps.

**Geral**

| Tool | O que faz |
|---|---|
| `session_time` | Tempo desde o início da sessão (ou desde um horário informado) e as outras sessões do DevPulse que rodaram no mesmo período, com a sobreposição. É uma **sugestão** para lançar horas: nada é lançado. |
| `get_punches` | Marcações de ponto por dia, com os intervalos trabalhados e o total (o intervalo em aberto de hoje é contado até agora). Só lê arquivos locais: nada é consultado na rede. |

Cada processo do DevPulse registra o próprio início num arquivo em `~/.devpulse/sessions/` e o atualiza a cada minuto. É assim que uma sessão enxerga as outras. No Claude Code, cada sessão tem o seu processo. Clientes que mantêm o servidor aberto entre conversas, como o Claude Desktop, devem informar `since`.

`get_punches` lê os arquivos que uma integração de ponto grava em `~/.devpulse/ponto/` (ou em `DEVPULSE_PONTO_DIR`). Para o **Senior X**, a integração é uma extensão do Edge/Chrome com um host local em PowerShell: veja [integrations/senior-ponto](integrations/senior-ponto/README.md). Outra fonte de ponto pode gravar o mesmo formato: um `AAAA-MM-DD.json` por dia com `date`, `timeZone`, `punches` (`HH:MM:SS`), `source` e `syncedAt`.

As datas das tools de atividade (`list_pushes`, `list_pull_request_activity`, `get_work_item_updates`) entram e saem no fuso do usuário. O Azure DevOps responde em UTC, o que jogaria o fim da tarde no dia seguinte.

---

## Instalação rápida

### 1. Baixe ou compile

**Com o script (recomendado):** é o [Início rápido](#início-rápido). Os scripts fazem o mesmo que os passos manuais abaixo:

- Escolhem o arquivo do seu sistema e da sua arquitetura.
- Conferem o SHA256 e rodam o `devpulse install`.
- Recusam o download se o hash não bater.

Para passar flags ao instalador:

```bash
curl -fsSL https://raw.githubusercontent.com/sidyjw/devpulse/main/install.sh | sh -s -- --yes --harness claude --app code
```

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/sidyjw/devpulse/main/install.ps1))) --yes --harness claude --app code
```

Para fixar uma versão, defina `DEVPULSE_VERSION=0.2.0` (no PowerShell, `$env:DEVPULSE_VERSION = '0.2.0'`). Se preferir ler o script antes de rodar, baixe o `install.sh` ou o `install.ps1` da release: eles também estão no `SHA256SUMS.txt` e no atestado de proveniência.

**Binário pronto:** na página de [Releases](https://github.com/sidyjw/devpulse/releases), baixe o arquivo do seu sistema (`windows`, `darwin` = macOS ou `linux`; `amd64` = Intel/AMD, `arm64` = ARM/Apple Silicon) e extraia. Para conferir o download:

```bash
sha256sum -c SHA256SUMS.txt --ignore-missing                         # integridade
gh attestation verify devpulse_*_linux_amd64.tar.gz --repo sidyjw/devpulse   # foi gerado pelo workflow deste repositório
```

**Com o Go instalado:**

```bash
go install github.com/sidyjw/devpulse@latest
```

**Compilando o código** (precisa do [Go](https://go.dev/dl/) 1.22 ou superior):

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
6. Para cada token, mostra **antes da pergunta** o passo a passo para gerá-lo, com o link da sua organização. Isso vale para o PAT do Azure DevOps e para o token do 7pace. Se o arquivo ainda não existe, o instalador oferece criá-lo: **copie o token (Ctrl+C) e tecle Enter.** O instalador lê a área de transferência, grava um arquivo que só você pode ler (`chmod 600` ou `icacls`) e limpa a área de transferência. O token nunca aparece na tela nem no histórico do terminal.
7. Copia o executável para um lugar fixo: `%LOCALAPPDATA%\Programs\devpulse\` no Windows ou `~/.local/bin/` no macOS/Linux. Esse lugar também pode ser alterado. Se a pasta não está no `PATH`, o instalador oferece adicioná-la:
   - **Windows:** no PATH do usuário.
   - **zsh e bash:** no `~/.zshrc` ou no `~/.bashrc` (`~/.bash_profile` no macOS).
   - **fish:** em `conf.d`.

   Assim, `devpulse update` funciona de qualquer pasta. `--no-path` pula esse passo.
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
devpulse update      # baixa a última versão, confere o SHA256 e troca o executável de cada app
devpulse detect      # SO, apps detectados, arquivos de configuração e instalações existentes
devpulse uninstall   # remove a entrada (com backup); não apaga o executável nem os tokens
devpulse -check      # testa a configuração do ambiente atual
```

O `devpulse update` faz isto:

1. Procura os executáveis usados pelas entradas `devpulse` de todos os apps e escopos, e inclui o executável em execução.
2. Mostra as novidades da versão e pede confirmação.
3. Baixa a release do GitHub e confere o SHA256 com o `SHA256SUMS.txt`.
4. Testa se o novo executável roda (`-version`).
5. Só então troca cada arquivo. O anterior fica ao lado como `.old`, e a troca funciona mesmo com o Claude Desktop aberto no Windows. Depois, é só reiniciar os apps.

| Flag do `update` | Para quê |
|---|---|
| `--check` | Só informa se há versão nova, sem baixar |
| `--version 0.2.0` | Instala essa versão. Também serve para voltar a uma anterior |
| `--dry-run` | Mostra quais executáveis seriam trocados |
| `--yes` / `--force` | Não pergunta / reinstala mesmo que já esteja na versão pedida |

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
| `--no-path` | Não adiciona a pasta do executável ao `PATH` |
| `--name <nome>` | Nome da entrada (padrão `devpulse`) |
| `--advanced` | Pergunta também as opções avançadas |
| `--force` | Substitui entradas existentes sem perguntar |
| `--skip-check` | Não roda o `-check` antes de gravar |

## Gerando os tokens

**7pace**: no Azure DevOps, abra o **7pace Timetracker** e vá em **Settings → API & Reporting** para criar um token. A organização é o prefixo da URL. Por exemplo, em `https://minhaorg.timehub.7pace.com`, ela é `minhaorg`.

**Azure DevOps (Boards e Repos)**: em `https://dev.azure.com/<org>`, abra o ícone de usuário e depois **Personal access tokens → New Token**. Em **Scopes → Custom defined**, marque:
- **Work Items: Read & write**. Se quiser só consultar, basta **Read**.
- **Project and Team: Read**
- **Code: Read & write**, só se for usar o Repos. Para só consultar, basta **Read**.

O Boards e o Repos usam o mesmo PAT.

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

**Azure DevOps / Repos**

O Repos usa a conexão do Boards (`AZURE_DEVOPS_ORG_URL`, o PAT e o projeto padrão), então ligar o Repos liga também o Boards.

| Variável | Obrigatória | Descrição |
|---|---|---|
| `AZURE_DEVOPS_REPOS` | sim | `true` liga o Repos (o instalador grava ao escolher o componente) |
| `AZURE_DEVOPS_REPOS_READ_ONLY` | não | `true` esconde as tools que criam branches e alteram pull requests |

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
| `DEVPULSE_PONTO_DIR` | não | Pasta das marcações de ponto lidas por `get_punches` (padrão `~/.devpulse/ponto`) |

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
- "Abre uma PR da minha branch atual para a develop, vinculada à 4312, com a Ana como revisora."
- "A main exige PR? Quantos revisores?"
- "O que falta resolver nos comentários da PR 57?"
- "Quanto tempo passei nesta sessão? Sugere o lançamento na 4312."

Antes de qualquer escrita, o assistente mostra o que vai fazer. O Claude Desktop também pede sua permissão a cada chamada de tool que grava.

## Estrutura do código

```
main.go                          # serve | -check | -version | install | uninstall | detect | update
install.sh, install.ps1          # instalação em uma linha (baixa a release, confere o SHA256, roda o install)
internal/mcp/                    # servidor MCP (JSON-RPC sobre stdio) e helpers de schema
internal/httpx/                  # cliente HTTPS: sem redirect, limite de resposta, token removido dos erros
internal/settings/               # leitura/validação de variáveis e arquivos de segredo
internal/provider/               # contratos Provider / Component / Setting e a ativação
internal/providers/registry.go   # lista de providers desta versão
internal/providers/azuredevops/  # provider Azure DevOps: componentes Boards, Repos e 7pace
internal/session/                # tempo da sessão e sessões paralelas (session_time)
internal/install/                # instalador: harnesses, apps, edição de config, prompts, PATH e update
internal/release/                # releases do GitHub: consulta, download, SHA256 e extração (devpulse update)
.github/workflows/release.yml    # build e publicação das releases a cada tag vX.Y.Z
```

## Adicionando um provider

1. Crie `internal/providers/<nome>/` com uma função que devolva um `provider.Provider`, com um `provider.Component` para cada módulo. Cada componente declara:
   - `Settings`: as variáveis que ele lê, com rótulo, ajuda, tipo (`String`, `URL`, `SecretFile`, `Bool`, `Duration`, `Flag`), se é obrigatória ou avançada, o valor padrão e a **mesma** função de validação usada pelo servidor. O instalador monta as perguntas a partir disso. Opcionalmente, `Guide` traz um passo a passo de onde obter o valor, e os passos podem citar respostas anteriores como `{VARIAVEL|alternativa}`. `Guide.Show` diz quando o guia aparece:
     - `provider.GuideWhenNeeded` (padrão): nos campos comuns, antes da pergunta. Nos tokens, só depois de a pessoa informar o caminho e só se o arquivo ainda precisa ser criado.
     - `provider.GuideAlways`: sempre antes da pergunta, mesmo que o arquivo já exista. É o que usam o PAT do Azure DevOps e o token do 7pace.

     Componentes do mesmo provider podem declarar as mesmas variáveis (como o Boards e o Repos fazem com a conexão): o instalador pergunta cada uma só uma vez. Uma variável `Flag` é gravada como `true` sem pergunta quando o componente é escolhido. Ela serve para ligar um componente cujas outras variáveis são compartilhadas.
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
- **Servidor e providers**: usam servidores 7pace e Azure DevOps falsos (`httptest`). Verificam payloads, parâmetros da API, PATCH, bloqueio de duplicidade, validação de lote, bloqueio de redirect, remoção do token de mensagens de erro, limite de tamanho de resposta, escape de WIQL, configuração e ativação dos componentes. No Repos: leitura da URL do remoto (e recusa de outras organizações), validação de nomes de branch, criação de branch e de PR, vínculo com work items e mensagens de erro do Azure DevOps. Nas tools de atividade: filtro por pessoa e período (sem confiar só no filtro do servidor), repositórios desativados ou sem permissão, refs internas de PR, votos e comentários por autor, campos internos e revisões só de time tracking.
- **Ponto**: relógio simulado e diretório temporário. Cobrem batidas fora de ordem e com segundos, arquivo com BOM, intervalo em aberto hoje (contado até agora) e em dia passado, dia sem batidas, dia não sincronizado, arquivo inválido ou com a data trocada, sincronização atrasada e os limites do período.
- **Sessões**: relógio simulado e diretório temporário. Cobrem a sobreposição com sessões ativas, encerradas e interrompidas (sem contar a mesma hora duas vezes), o `since` e a limpeza dos registros antigos.
- **Instalador**: roda em diretórios temporários com comandos externos simulados. Cobre a edição de JSON (preserva chaves e ordem, faz backup, é idempotente, recusa JSON com comentários), os caminhos por sistema (incluindo o Claude Desktop MSIX), o uso seguro do CLI `claude`, os fluxos interativo e não interativo, o dry-run, a migração da entrada `7pace`, a gravação do token, a ordem dos guias, o PATH (sem duplicar a linha e sem mexer no sistema real) e o `update`: troca do executável, `.old`, recusa quando o novo não roda, `--check` e `--dry-run`.
- **Releases**: usam uma API do GitHub falsa (`httptest` com TLS). Cobrem o download conferido pelo SHA256, a recusa de hash errado, o bloqueio de http e de redirecionamento para outros hosts, a extração de zip e tar.gz e a comparação de versões.

## Versões e releases

O projeto segue o [Versionamento Semântico](https://semver.org/lang/pt-BR/) e ainda está na série `0.x`: enquanto não chegar à `1.0.0`, uma versão MINOR (`0.1` → `0.2`) pode trazer mudanças incompatíveis, sempre descritas no [CHANGELOG](CHANGELOG.md). `devpulse -version` mostra a versão instalada, e `devpulse update --check` diz se há uma mais nova.

Para publicar uma versão:

1. Mova o que está em **[Não lançado]** no `CHANGELOG.md` para uma seção `## [X.Y.Z] - AAAA-MM-DD` e atualize os links do fim do arquivo.
2. Faça o commit e crie a tag anotada: `git tag -a vX.Y.Z -m "vX.Y.Z"`.
3. Envie: `git push origin main vX.Y.Z`.

O workflow [`release.yml`](.github/workflows/release.yml) roda os testes, compila para Windows, macOS e Linux (amd64 e arm64) com a versão embutida, anexa o `install.sh` e o `install.ps1`, gera o `SHA256SUMS.txt`, o atestado de proveniência e publica a release com as notas do CHANGELOG. O `devpulse update` usa essas notas, o `SHA256SUMS.txt` e os nomes dos arquivos (`devpulse_<versão>_<os>_<arch>.zip|.tar.gz`). Por isso, não mude esse formato sem atualizar o `internal/release`. Tags com sufixo (`v0.2.0-rc.1`) viram pre-release.

## Limitações

- 7pace **Azure DevOps Services** (nuvem). O 7pace do Azure DevOps Server on-premises usa autenticação Windows (NTLM), que não é suportada.
- O campo de coluna do Kanban (`WEF_..._Kanban.Column`) muda por quadro. O `update_work_item` move o item pelo **estado**. Para mover para uma coluna específica, descubra o nome do campo com `get_work_item` (`allFields: true`) e use `fields`.
- Os horários são enviados como hora local, sem fuso (`AAAA-MM-DDTHH:MM:SS`), como na documentação da 7pace. O padrão de início é 09:00.
- O instalador não reescreve arquivos com comentários (JSONC, como o `settings.json` do Zed ou do VS Code). Nesses casos, ele mostra o trecho para colar.
- Se o `claude` no Windows for o atalho `.cmd` do npm, o instalador edita o `~/.claude.json` direto em vez de chamar o CLI, porque o `cmd.exe` corrompe argumentos JSON. Feche as sessões do Claude Code antes de instalar.

## Licença

[MIT](LICENSE)
