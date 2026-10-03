# Ponto Senior X

Extensão para o Edge (ou o Chrome) que copia as suas marcações de ponto do **Senior X** para `~/.devpulse/ponto/`, um arquivo por dia. Com isso, a harness sabe a que horas você entrou, saiu para o almoço e encerrou o expediente, e sugere quanto lançar no 7pace.

Só funciona no Windows: o host local é um script do Windows PowerShell 5.1, sem dependências.

## Como funciona

```
Edge (sua sessão do Senior X)
 └─ extensão
     ├─ percebe quando você registra o ponto pelo navegador
     └─ consulta as suas batidas dos últimos 7 dias (a mesma consulta da tela de comprovantes)
          │ native messaging: o Edge inicia o host sob demanda
          ▼
   host\host.cmd → host\host.ps1 → ~/.devpulse/ponto/AAAA-MM-DD.json
```

A extensão sincroniza:
- 3 segundos depois de você **registrar o ponto**;
- quando você **abre a tela de marcação de ponto**;
- a cada **30 minutos**, com o Edge aberto;
- ao **clicar no ícone** da extensão.

Visitas e alarmes respeitam um intervalo mínimo de 2 minutos. No 4º ponto do dia (ou em qualquer número par a partir dele), aparece uma notificação com o total trabalhado.

Um dia sincronizado sem batidas vira um arquivo com `"punches": []`. Assim, ele não se confunde com um dia que nunca foi sincronizado.

```json
{
    "date": "2026-10-02",
    "timeZone": "-03:00",
    "punches": ["08:01:12", "12:00:40", "13:02:05", "17:05:31"],
    "source": "senior-x",
    "syncedAt": "2026-10-02T17:05:35-03:00"
}
```

## Segurança e privacidade

- **Só leitura.** A extensão nunca registra ponto. Ela observa a requisição do botão "Registrar Ponto" (`webRequest`, sem bloquear nem alterar nada) e chama apenas a consulta de batidas.
- **O token não sai do navegador.** A extensão usa o cookie da sessão que você já tem no Senior X. Para o host vão apenas data, hora e fuso de cada batida. O Senior grava esse cookie no domínio `senior.com.br`, por isso a extensão pede acesso a `https://senior.com.br/*` além de `https://platform.senior.com.br/*`.
- **O host valida tudo** antes de gravar: data, horário (`HH:MM:SS`), fuso, até 62 dias e 24 batidas por dia. Qualquer outro campo é descartado.
- **Nada fica rodando.** O Edge inicia o host a cada sincronização e ele termina em seguida. Nenhuma porta é aberta.
- **Só esta extensão pode chamar o host.** Isso é definido pelo `allowed_origins` do manifesto do host, que usa o ID fixo `oopkcnbpkledhmkpomeflaomigfnlgdm`.
- A instalação não precisa de administrador: a chave de registro fica em `HKCU`.

## Instalação

1. Registre o host:

   ```powershell
   powershell -ExecutionPolicy Bypass -File .\integrations\senior-ponto\install.ps1
   ```

   Use `-Chrome` para registrar também no Google Chrome. O `-ExecutionPolicy Bypass` vale só para essa execução.
2. Em `edge://extensions`, ligue o **Modo de desenvolvedor**, clique em **Carregar sem pacote** e escolha a pasta `integrations\senior-ponto\extension`. O ID mostrado deve ser `oopkcnbpkledhmkpomeflaomigfnlgdm`.
3. Com o Senior X logado no Edge, clique no ícone da extensão. As batidas da última semana aparecem em `~/.devpulse/ponto/`.

Um `!` vermelho no ícone indica falha. Passe o mouse no ícone para ver o motivo: sessão expirada, host não registrado etc.

Para gravar em outra pasta, defina `DEVPULSE_PONTO_DIR` antes de abrir o Edge e de rodar o `install.ps1`.

## Desinstalação

```powershell
powershell -ExecutionPolicy Bypass -File .\integrations\senior-ponto\uninstall.ps1
```

O script remove as chaves de registro e o manifesto do host. As batidas gravadas ficam, a menos que você use `-RemoveData`. A extensão você remove em `edge://extensions`.

## Limitações

- O endereço da consulta (`/t/senior.com.br/bridge/1.0/rest/hcm/pontomobile/queries/clockingEventBetweenPeriodQuery`) não é uma API pública documentada: é a que a interface do Senior X usa. Se a Senior mudar a interface, a extensão pode precisar de ajuste.
- Batidas feitas por outros meios (aplicativo, relógio físico) aparecem na próxima sincronização, mas sem a notificação imediata.
- O cookie da sessão do Senior X é de sessão: ao fechar o Edge, ele pode sumir. Até você abrir o Senior X de novo, a sincronização a cada 30 minutos mostra o `!`. Como você abre o Senior X para bater o ponto, a batida já resolve isso.
