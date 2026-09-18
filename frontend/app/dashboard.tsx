"use client";

import { useCallback, useEffect, useMemo, useState } from "react";

type DashboardData = {
  mode: string; open: number; scheduled: number; closed: number; unknown: number;
  location_pending: number; jobs_pending: number; uncertain_scheduling: number;
  last_sync_success: string | null; last_sync_status: string | null;
};
type Operation = { id: string; slug: string; name: string; enabled: boolean; sync_interval_seconds: number; timezone: string; grouping_radius_meters: number; service_type_ids: string[] };
type ServiceType = { id: string; external_id: string; description: string; source: string; observed_at: string };
type Order = {
  id: string; external_id: string; operation_id: string; operation: string; type_description: string;
  source_status_code: string; status: string; scheduled_at: string | null; customer_display: string;
  defect_summary: string; address: string; latitude: number | null; longitude: number | null;
  location_source: string | null; observed_at: string; observed_version: number;
};
type SyncRun = { id: string; operation: string; source: string; trigger: string; status: string; orders_seen: number; started_at: string | null; finished_at: string | null; sanitized_error: string | null } | null;

const statusLabels: Record<string, string> = { open: "Aberta", scheduled: "Agendada", closed: "Encerrada", cancelled: "Cancelada", unknown: "Revisar" };

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/api/backend/${path}`, { ...init, cache: "no-store" });
  if (response.status === 401) { window.location.assign("/login"); throw new Error("Sessão expirada"); }
  const body = response.status === 204 ? null : await response.json();
  if (!response.ok) throw new Error(body?.error?.message ?? "Falha ao consultar a API local.");
  return body as T;
}

function formatDate(value: string | null) {
  if (!value) return "Ainda não ocorreu";
  return new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(new Date(value));
}

export default function Dashboard() {
  const [summary, setSummary] = useState<DashboardData | null>(null);
  const [orders, setOrders] = useState<Order[]>([]);
  const [operations, setOperations] = useState<Operation[]>([]);
  const [types, setTypes] = useState<ServiceType[]>([]);
  const [syncRun, setSyncRun] = useState<SyncRun>(null);
  const [status, setStatus] = useState("");
  const [operationID, setOperationID] = useState("");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Order | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [actionMessage, setActionMessage] = useState("");
  const [error, setError] = useState("");
  const todayLabel = new Intl.DateTimeFormat("pt-BR", { weekday: "long" }).format(new Date()).toLocaleUpperCase("pt-BR");
  const greeting = new Date().getHours() < 12 ? "Bom dia" : new Date().getHours() < 18 ? "Boa tarde" : "Boa noite";

  const load = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true);
    try {
      const query = new URLSearchParams(); if (status) query.set("status", status); if (operationID) query.set("operation_id", operationID);
      const [dashboard, orderResult, operationResult, typeResult, latest] = await Promise.all([
        api<DashboardData>("dashboard"), api<{ items: Order[] }>(`orders?${query}`),
        api<{ items: Operation[] }>("operations"), api<{ items: ServiceType[] }>("service-types"),
        api<SyncRun>("sync-runs/latest")
      ]);
      setSummary(dashboard); setOrders(orderResult.items); setOperations(operationResult.items); setTypes(typeResult.items); setSyncRun(latest); setError("");
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Falha inesperada."); }
    finally { setLoading(false); }
  }, [status, operationID]);

  useEffect(() => { void load(); const timer = window.setInterval(() => void load(true), 30_000); return () => window.clearInterval(timer); }, [load]);

  const filtered = useMemo(() => {
    const needle = search.trim().toLocaleLowerCase("pt-BR");
    if (!needle) return orders;
    return orders.filter((order) => [order.external_id, order.customer_display, order.address, order.defect_summary].some((value) => value.toLocaleLowerCase("pt-BR").includes(needle)));
  }, [orders, search]);

  async function synchronize() {
    const operation = operationID || operations[0]?.id;
    if (!operation) return;
    setActionMessage("Solicitando sincronização…");
    try {
      const result = await api<{ queued: boolean; message?: string }>("sync-runs", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ operation_id: operation }) });
      setActionMessage(result.queued ? "Sincronização enfileirada. O worker processará em instantes." : (result.message ?? "Sincronização já em andamento."));
      window.setTimeout(() => void load(true), 3500);
    } catch (reason) { setActionMessage(reason instanceof Error ? reason.message : "Falha ao sincronizar."); }
  }

  async function logout() { await fetch("/api/session/logout", { method: "POST" }); window.location.assign("/login"); }

  return (
    <main className="app-shell">
      <aside className="sidebar">
        <div className="brand"><span className="brand-mark small">OS</span><span><strong>Agenda O.S.</strong><small>Planejamento de campo</small></span></div>
        <nav aria-label="Navegação principal">
          <button className="nav-item active"><Icon name="grid" /> Visão geral</button>
          <button className="nav-item" onClick={() => document.getElementById("orders")?.scrollIntoView()}><Icon name="list" /> Ordens de serviço</button>
          <button className="nav-item" onClick={() => document.getElementById("map")?.scrollIntoView()}><Icon name="map" /> Localizações</button>
          <button className="nav-item disabled" title="Disponível no próximo marco"><Icon name="calendar" /> Planejamento <em>em breve</em></button>
        </nav>
        <div className="sidebar-bottom">
          <button className="nav-item" onClick={() => setSettingsOpen(true)}><Icon name="settings" /> Configurações</button>
          <button className="nav-item" onClick={logout}><Icon name="exit" /> Sair</button>
          <div className="mode-card"><span className="pulse-dot" /><div><strong>Modo simulado</strong><small>Nenhum dado vai ao MK</small></div></div>
        </div>
      </aside>

      <section className="workspace">
        <header className="topbar">
          <div><p className="eyebrow">{todayLabel} · OPERAÇÃO RURAL</p><h1>{greeting}, vamos organizar a rota?</h1></div>
          <div className="top-actions"><button className="icon-button" title="Configurações" onClick={() => setSettingsOpen(true)}><Icon name="settings" /></button><button className="button primary" onClick={synchronize}><Icon name="refresh" /> Sincronizar agora</button></div>
        </header>

        {summary?.mode === "simulation" && <div className="simulation-banner"><Icon name="flask" /><span><strong>Ambiente de simulação.</strong> Os dados abaixo são sintéticos e isolados da operação real.</span></div>}
        {(error || actionMessage) && <div className={error ? "notice error" : "notice"} role="status">{error || actionMessage}</div>}

        <section className="summary-grid" aria-label="Resumo operacional">
          <Metric label="Abertas sem agenda" value={summary?.open} detail="prontas para revisar" tone="orange" icon="clipboard" loading={loading} />
          <Metric label="Agendadas" value={summary?.scheduled} detail="observadas no sistema" tone="green" icon="check" loading={loading} />
          <Metric label="Encerradas" value={summary?.closed} detail="mantidas no histórico" tone="blue" icon="flag" loading={loading} />
          <Metric label="Sem localização" value={summary?.location_pending} detail="exigem revisão manual" tone="yellow" icon="pin" loading={loading} />
        </section>

        <section className="sync-strip">
          <div className="sync-icon"><Icon name="refresh" /></div>
          <div><span>Última sincronização bem-sucedida</span><strong>{formatDate(summary?.last_sync_success ?? null)}</strong></div>
          <div className="sync-source"><span>Origem</span><strong>{syncRun?.source === "simulation" ? "Simulador local" : "—"}</strong></div>
          <div className="sync-status"><span className={`status-dot ${summary?.last_sync_status === "completed" ? "ok" : "waiting"}`} /> {summary?.last_sync_status === "completed" ? `${syncRun?.orders_seen ?? 0} ordens observadas` : "Aguardando o worker"}</div>
        </section>

        <div className="content-grid">
          <section className="panel orders-panel" id="orders">
            <div className="panel-heading"><div><p className="eyebrow">FILA OPERACIONAL</p><h2>Ordens de serviço</h2></div><span className="count-pill">{filtered.length} itens</span></div>
            <div className="filters">
              <label className="search"><Icon name="search" /><input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Buscar por OS, cliente ou endereço" /></label>
              <select value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Filtrar situação"><option value="">Todas as situações</option><option value="open">Abertas</option><option value="scheduled">Agendadas</option><option value="closed">Encerradas</option><option value="unknown">Revisar</option></select>
              <select value={operationID} onChange={(e) => setOperationID(e.target.value)} aria-label="Filtrar operação"><option value="">Todas as operações</option>{operations.map((operation) => <option key={operation.id} value={operation.id}>{operation.name}</option>)}</select>
            </div>
            <div className="table-wrap">
              <table><thead><tr><th>Ordem</th><th>Cliente / ocorrência</th><th>Localização</th><th>Situação</th><th aria-label="Ações" /></tr></thead>
                <tbody>{filtered.map((order) => <tr key={order.id} onClick={() => setSelected(order)}>
                  <td><strong>#{order.external_id.replace("SIM-OS-", "")}</strong><small>{order.type_description}</small></td>
                  <td><strong>{order.customer_display}</strong><small>{order.defect_summary}</small></td>
                  <td><span className="address"><Icon name="pin" />{order.address}</span>{order.latitude === null && <small className="pending-text">posição pendente</small>}</td>
                  <td><span className={`status-badge ${order.status}`}>{statusLabels[order.status] ?? order.status}</span></td>
                  <td><button className="row-action" aria-label={`Abrir ${order.external_id}`}><Icon name="arrow" /></button></td>
                </tr>)}</tbody>
              </table>
              {!loading && filtered.length === 0 && <div className="empty-state"><Icon name="search" /><strong>Nenhuma ordem encontrada</strong><span>Ajuste os filtros para ampliar a busca.</span></div>}
            </div>
          </section>

          <section className="panel map-panel" id="map">
            <div className="panel-heading"><div><p className="eyebrow">VISÃO GEOGRÁFICA</p><h2>Proximidade</h2></div><span className="outline-pill">Esquemático</span></div>
            <div className="map-canvas">
              <div className="map-road road-a" /><div className="map-road road-b" /><div className="map-road road-c" />
              {filtered.filter((item) => item.latitude !== null).map((order, index) => <button key={order.id} className={`order-pin pin-${(index % 4) + 1}`} style={{ left: `${18 + ((index * 19) % 67)}%`, top: `${20 + ((index * 23) % 58)}%` }} onClick={() => setSelected(order)} title={order.external_id}>{index + 1}</button>)}
              <div className="map-legend"><span><i className="legend-pin" />Com posição confiável</span><span><i className="legend-pending" />{summary?.location_pending ?? 0} pendente(s)</span></div>
            </div>
            <p className="map-disclaimer">Representação visual local; não calcula trajeto rodoviário, ETA ou navegabilidade.</p>
          </section>
        </div>
      </section>

      {selected && <OrderDrawer order={selected} onClose={() => setSelected(null)} />}
      {settingsOpen && <SettingsPanel operations={operations} types={types} onClose={() => setSettingsOpen(false)} onSaved={() => load(true)} />}
    </main>
  );
}

function Metric({ label, value, detail, tone, icon, loading }: { label: string; value?: number; detail: string; tone: string; icon: string; loading: boolean }) {
  return <article className="metric"><div className={`metric-icon ${tone}`}><Icon name={icon} /></div><div><span>{label}</span><strong>{loading && value === undefined ? "—" : value ?? 0}</strong><small>{detail}</small></div></article>;
}

function OrderDrawer({ order, onClose }: { order: Order; onClose: () => void }) {
  return <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}><aside className="drawer">
    <div className="drawer-head"><div><p className="eyebrow">DETALHES DA ORDEM</p><h2>{order.external_id}</h2></div><button className="icon-button" onClick={onClose} aria-label="Fechar">×</button></div>
    <span className={`status-badge ${order.status}`}>{statusLabels[order.status] ?? order.status}</span>
    <dl className="details"><div><dt>Cliente</dt><dd>{order.customer_display}</dd></div><div><dt>Ocorrência</dt><dd>{order.defect_summary}</dd></div><div><dt>Endereço observado</dt><dd>{order.address}</dd></div><div><dt>Posição</dt><dd>{order.latitude === null ? "Não resolvida — requer revisão" : `${order.latitude.toFixed(5)}, ${order.longitude?.toFixed(5)} (${order.location_source})`}</dd></div><div><dt>Situação original</dt><dd>Código {order.source_status_code}</dd></div><div><dt>Última observação</dt><dd>{formatDate(order.observed_at)} · versão {order.observed_version}</dd></div></dl>
    <div className="drawer-warning"><Icon name="shield" />Esta é uma observação local. Ela não representa agendamento nem execução no MK.</div>
  </aside></div>;
}

function SettingsPanel({ operations, types, onClose, onSaved }: { operations: Operation[]; types: ServiceType[]; onClose: () => void; onSaved: () => void }) {
  const operation = operations[0]; const [selected, setSelected] = useState<string[]>(operation?.service_type_ids ?? []); const [message, setMessage] = useState("");
  async function save() { if (!operation) return; try { await api(`operations/${operation.id}/service-types`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ service_type_ids: selected }) }); setMessage("Configuração salva por ID."); onSaved(); } catch (reason) { setMessage(reason instanceof Error ? reason.message : "Falha ao salvar."); } }
  return <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}><aside className="drawer settings-drawer"><div className="drawer-head"><div><p className="eyebrow">CONFIGURAÇÃO</p><h2>Operação rural</h2></div><button className="icon-button" onClick={onClose}>×</button></div>
    <p className="muted">Escolha pelo código quais tipos do catálogo pertencem a esta operação. A descrição não decide a elegibilidade.</p>
    <div className="setting-block"><span>Tipos de O.S. elegíveis</span>{types.map((type) => <label className="check-row" key={type.id}><input type="checkbox" checked={selected.includes(type.id)} onChange={(event) => setSelected(event.target.checked ? [...selected, type.id] : selected.filter((id) => id !== type.id))} /><span><strong>{type.description}</strong><small>{type.external_id} · {type.source}</small></span></label>)}</div>
    <div className="setting-block compact"><span>Sincronização</span><strong>A cada {Math.round((operation?.sync_interval_seconds ?? 600) / 60)} minutos</strong><small>Executada pelo worker, mesmo com o painel fechado.</small></div>
    {message && <div className="notice">{message}</div>}<button className="button primary wide" onClick={save}>Salvar configuração</button>
  </aside></div>;
}

function Icon({ name }: { name: string }) {
  const paths: Record<string, React.ReactNode> = {
    grid: <><rect x="3" y="3" width="7" height="7" rx="2"/><rect x="14" y="3" width="7" height="7" rx="2"/><rect x="3" y="14" width="7" height="7" rx="2"/><rect x="14" y="14" width="7" height="7" rx="2"/></>,
    list: <><path d="M8 6h13M8 12h13M8 18h13"/><circle cx="3.5" cy="6" r=".5"/><circle cx="3.5" cy="12" r=".5"/><circle cx="3.5" cy="18" r=".5"/></>,
    map: <><path d="m3 6 5-3 8 3 5-3v15l-5 3-8-3-5 3Z"/><path d="M8 3v15M16 6v15"/></>, calendar: <><rect x="3" y="5" width="18" height="16" rx="2"/><path d="M16 3v4M8 3v4M3 10h18"/></>,
    settings: <><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1A1.7 1.7 0 0 0 9 4.6a1.7 1.7 0 0 0 1-1.6v-.2h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1Z"/></>,
    exit: <><path d="M10 17l5-5-5-5M15 12H3"/><path d="M14 3h5a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-5"/></>, refresh: <><path d="M20 7v5h-5"/><path d="M4 17v-5h5M6.1 9a7 7 0 0 1 11.2-2.6L20 9M4 15l2.7 2.6A7 7 0 0 0 17.9 15"/></>,
    flask: <><path d="M9 3h6M10 3v6l-5 9a2 2 0 0 0 1.8 3h10.4A2 2 0 0 0 19 18l-5-9V3"/><path d="M8 15h8"/></>, clipboard: <><rect x="5" y="4" width="14" height="17" rx="2"/><path d="M9 4a3 3 0 0 1 6 0v2H9ZM9 12h6M9 16h4"/></>,
    check: <><circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/></>, flag: <><path d="M5 21V4M5 5h12l-2 4 2 4H5"/></>, pin: <><path d="M20 10c0 5-8 11-8 11S4 15 4 10a8 8 0 1 1 16 0Z"/><circle cx="12" cy="10" r="2.5"/></>,
    search: <><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/></>, arrow: <path d="m9 18 6-6-6-6"/>, shield: <><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z"/><path d="m9 12 2 2 4-4"/></>
  };
  return <svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{paths[name]}</svg>;
}
