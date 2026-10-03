// Copia as marcações de ponto do Senior X para o host local (native
// messaging), que as grava em ~/.devpulse/ponto/AAAA-MM-DD.json.
//
// A extensão só lê: usa a sessão que o usuário já tem no Senior X e chama a
// mesma consulta que a tela de comprovantes usa. Ela nunca registra ponto e
// o token não sai do navegador: para o host vão só data e hora das batidas.

const SENIOR = "https://platform.senior.com.br";
const QUERY_URL = SENIOR + "/t/senior.com.br/bridge/1.0/rest/hcm/pontomobile/queries/clockingEventBetweenPeriodQuery";
const HOST = "com.devpulse.senior_ponto";
const DAYS_BACK = 7;
const PAGE_SIZE = 100;
const MAX_PAGES = 5;
const ALARM = "sync";
const ALARM_MINUTES = 30;
// Visitas e alarmes não sincronizam de novo antes disso; batida e clique sim.
const THROTTLE_MS = 2 * 60 * 1000;
// A batida é gravada antes da resposta chegar; espera a consulta já trazê-la.
const AFTER_PUNCH_MS = 3000;

chrome.runtime.onInstalled.addListener(() => { schedule(); sync("install"); });
chrome.runtime.onStartup.addListener(() => { schedule(); sync("startup"); });
chrome.alarms.onAlarm.addListener((a) => { if (a.name === ALARM) sync("alarm"); });
chrome.action.onClicked.addListener(() => sync("manual"));

// Registro de ponto pelo navegador (botão "Registrar Ponto").
chrome.webRequest.onCompleted.addListener((d) => {
  if (d.method === "POST" && d.statusCode >= 200 && d.statusCode < 300) {
    setTimeout(() => sync("punch"), AFTER_PUNCH_MS);
  }
}, { urls: [SENIOR + "/*clockingEventImportByBrowser*"] });

// Abertura da tela de marcação de ponto.
chrome.webRequest.onCompleted.addListener((d) => {
  if (d.statusCode >= 200 && d.statusCode < 300) sync("visit");
}, { urls: [SENIOR + "/*getEmployeeClockingConfigQuery*"] });

function schedule() {
  chrome.alarms.create(ALARM, { periodInMinutes: ALARM_MINUTES });
}

let running = null;

async function sync(trigger) {
  // Uma sincronização por vez; uma batida durante outra sync roda em seguida.
  while (running) await running;
  running = doSync(trigger).finally(() => { running = null; });
  return running;
}

async function doSync(trigger) {
  if (trigger !== "punch" && trigger !== "manual") {
    const { lastSync = 0 } = await chrome.storage.session.get("lastSync");
    if (Date.now() - lastSync < THROTTLE_MS) return;
  }

  let token;
  try {
    token = await seniorToken();
  } catch (e) {
    return status(false, e.message);
  }

  const today = new Date();
  const first = new Date(today);
  first.setDate(first.getDate() - (DAYS_BACK - 1));
  const from = ymd(first);
  const to = ymd(today);

  let events;
  try {
    events = await fetchEvents(token, from, to);
  } catch (e) {
    return status(false, e.message);
  }

  const days = groupByDay(events, from, to);
  let reply;
  try {
    reply = await chrome.runtime.sendNativeMessage(HOST, {
      type: "sync",
      source: "senior-x",
      trigger,
      syncedAt: new Date().toISOString(),
      days,
    });
  } catch (e) {
    return status(false, "O host local não respondeu (" + e.message + "). Rode integrations/senior-ponto/install.ps1.");
  }
  if (!reply || !reply.ok) return status(false, "O host local recusou: " + (reply && reply.error));

  await chrome.storage.session.set({ lastSync: Date.now() });
  status(true, "Sincronizado às " + hhmm(new Date()) + " (" + trigger + ").");
  if (trigger === "punch") notifyIfClosed(days.find((d) => d.date === to));
}

// O cookie da sessão é gravado para o domínio senior.com.br (não para
// platform.senior.com.br): por isso a permissão https://senior.com.br/*.
// Cada falha tem uma mensagem própria: o conserto é diferente em cada caso.
async function seniorToken() {
  if (!(await chrome.permissions.contains({ origins: ["https://senior.com.br/*"] }))) {
    throw new Error("A extensão está sem acesso a senior.com.br. Em edge://extensions, remova a extensão e carregue de novo.");
  }
  const cookies = await chrome.cookies.getAll({ name: "com.senior.token" });
  const mine = cookies
    .filter((c) => c.domain.replace(/^\./, "").endsWith("senior.com.br"))
    .sort((a, b) => (b.expirationDate || 0) - (a.expirationDate || 0));
  if (mine.length === 0) {
    const visible = await chrome.cookies.getAll({ domain: "senior.com.br" });
    throw new Error("Cookie da sessão do Senior X não encontrado (" + visible.length +
      " outros cookies de senior.com.br visíveis). Abra o Senior X neste perfil do Edge e faça login.");
  }
  for (const c of mine) {
    try {
      const t = JSON.parse(decodeURIComponent(c.value)).access_token;
      if (t) return t;
    } catch (_) { /* cookie em outro formato: tenta o próximo */ }
  }
  throw new Error("O cookie da sessão do Senior X veio num formato inesperado (" + mine.length + " encontrado(s)).");
}

async function fetchEvents(token, from, to) {
  const out = [];
  for (let page = 0; page < MAX_PAGES; page++) {
    let res;
    try {
      res = await fetch(QUERY_URL, {
        method: "POST",
        headers: { "Content-Type": "application/json", "Authorization": "Bearer " + token },
        body: JSON.stringify({
          filter: {
            period: { initialDate: from, finalDate: to },
            pageInfo: { page, pageSize: PAGE_SIZE },
            sort: { field: "dateEvent", order: "ASC" },
          },
        }),
      });
    } catch (e) {
      throw new Error("Falha de rede ao consultar o Senior X: " + e.message);
    }
    if (res.status === 401 || res.status === 403) {
      throw new Error("A sessão do Senior X expirou. Abra o Senior X no Edge.");
    }
    if (!res.ok) throw new Error("O Senior X respondeu HTTP " + res.status + ".");
    const body = await res.json();
    out.push(...(body.result || []));
    if (page + 1 >= (body.totalPages || 1)) break;
  }
  return out;
}

// Todos os dias do período entram, mesmo sem batidas: assim um dia vazio
// fica diferente de um dia que nunca foi sincronizado.
function groupByDay(events, from, to) {
  const byDate = new Map();
  for (let d = parseYmd(from); ymd(d) <= to; d.setDate(d.getDate() + 1)) {
    byDate.set(ymd(d), { date: ymd(d), timeZone: null, punches: [] });
  }
  for (const e of events) {
    const day = byDate.get(e.dateEvent);
    if (!day || typeof e.timeEvent !== "string") continue;
    day.punches.push(e.timeEvent.slice(0, 8));
    if (!day.timeZone && e.timeZone) day.timeZone = e.timeZone;
  }
  const days = [...byDate.values()];
  for (const d of days) d.punches.sort();
  return days;
}

function notifyIfClosed(day) {
  if (!day || day.punches.length < 4 || day.punches.length % 2 !== 0) return;
  let minutes = 0;
  for (let i = 0; i + 1 < day.punches.length; i += 2) {
    minutes += toMinutes(day.punches[i + 1]) - toMinutes(day.punches[i]);
  }
  const last = day.punches[day.punches.length - 1].slice(0, 5);
  chrome.notifications.create("closed-" + day.date, {
    type: "basic",
    iconUrl: "icon.png",
    title: "Expediente encerrado às " + last,
    message: Math.floor(minutes / 60) + "h" + String(minutes % 60).padStart(2, "0") +
      " trabalhadas hoje. Rode /lancar-horas para a proposta de lançamento.",
  });
}

function status(ok, text) {
  chrome.action.setBadgeText({ text: ok ? "" : "!" });
  chrome.action.setBadgeBackgroundColor({ color: "#c62828" });
  chrome.action.setTitle({ title: "DevPulse - Ponto\n" + text + "\nClique para sincronizar." });
}

function ymd(d) {
  return d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0");
}

function parseYmd(s) {
  const [y, m, d] = s.split("-").map(Number);
  return new Date(y, m - 1, d);
}

function hhmm(d) {
  return String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
}

function toMinutes(t) {
  const [h, m] = t.split(":").map(Number);
  return h * 60 + m;
}
