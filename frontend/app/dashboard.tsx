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
  location_source: string | null; location_version: number; location_needs_review: boolean;
  observed_at: string; observed_version: number;
};
type SyncRun = { id: string; operation: string; source: string; trigger: string; status: string; orders_seen: number; started_at: string | null; finished_at: string | null; sanitized_error: string | null } | null;
type TeamMember = { id: string; external_id: string; name: string };
type Team = { id: string; external_id: string; name: string; source: string; members: TeamMember[] };
type ProposalOrder = Pick<Order, "id" | "external_id" | "customer_display" | "defect_summary" | "address" | "latitude" | "longitude" | "observed_version">;
type ProposalGroup = { id: string; name: string; position: number; is_fixed: boolean; orders: ProposalOrder[] };
type Proposal = {
  id: string; source: string; operation_id: string; operation: string; operational_date: string; status: "draft" | "approved" | "conflicted";
  version: number; team_id: string | null; agenda_responsible_id: string | null; generator: string;
  approved_at: string | null; groups: ProposalGroup[]; pending: ProposalOrder[];
};
type PreviewIssue = { code: string; message: string };
type SchedulingPreview = {
  proposal_id: string; proposal_version: number; proposal_status: string; operation: string; operational_date: string;
  team: string | null; agenda_responsible: string | null; can_create_requests: boolean; blockers: PreviewIssue[];
  items: { order_id: string; external_id: string; customer_display: string; address: string; current_status: string; issues: PreviewIssue[] }[];
};

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

function localDate() {
  const now = new Date();
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
}

function generatorLabel(generator: string) {
  switch (generator) {
    case "deterministic": return "cálculo local";
    case "deterministic-refresh": return "recálculo local";
    case "workers_ai": return "sugestão da IA";
    default: return generator;
  }
}

function geographicPoints(orders: Order[]) {
  const located = orders.filter((order): order is Order & { latitude: number; longitude: number } => order.latitude !== null && order.longitude !== null);
  if (located.length === 0) return [];
  const latitudes = located.map((order) => order.latitude);
  const longitudes = located.map((order) => order.longitude);
  const minLat = Math.min(...latitudes), maxLat = Math.max(...latitudes);
  const minLon = Math.min(...longitudes), maxLon = Math.max(...longitudes);
  const latRange = Math.max(maxLat - minLat, 0.002);
  const lonRange = Math.max(maxLon - minLon, 0.002);
  const south = (minLat + maxLat) / 2 - latRange / 2;
  const north = (minLat + maxLat) / 2 + latRange / 2;
  const west = (minLon + maxLon) / 2 - lonRange / 2;
  return located.map((order) => ({ order,
    left: 8 + ((order.longitude - west) / lonRange) * 84,
    top: 8 + ((north - order.latitude) / (north - south)) * 78
  }));
}

export default function Dashboard() {
  const [summary, setSummary] = useState<DashboardData | null>(null);
  const [orders, setOrders] = useState<Order[]>([]);
  const [orderTotal, setOrderTotal] = useState(0);
  const [orderPage, setOrderPage] = useState(1);
  const [operations, setOperations] = useState<Operation[]>([]);
  const [types, setTypes] = useState<ServiceType[]>([]);
  const [syncRun, setSyncRun] = useState<SyncRun>(null);
  const [teams, setTeams] = useState<Team[]>([]);
  const [proposal, setProposal] = useState<Proposal | null>(null);
  const [planningDate, setPlanningDate] = useState(localDate);
  const [planningLoading, setPlanningLoading] = useState(false);
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
      const query = new URLSearchParams({ page: String(orderPage), page_size: "100" }); if (status) query.set("status", status); if (operationID) query.set("operation_id", operationID); if (search.trim()) query.set("q", search.trim());
      const [dashboard, orderResult, operationResult, typeResult, latest, teamResult] = await Promise.all([
        api<DashboardData>("dashboard"), api<{ items: Order[]; total: number }>(`orders?${query}`),
        api<{ items: Operation[] }>("operations"), api<{ items: ServiceType[] }>("service-types"),
        api<SyncRun>("sync-runs/latest"), api<{ items: Team[] }>("teams")
      ]);
      setSummary(dashboard); setOrders(orderResult.items); setOrderTotal(orderResult.total); setOperations(operationResult.items); setTypes(typeResult.items); setSyncRun(latest); setTeams(teamResult.items); setError("");
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Falha inesperada."); }
    finally { setLoading(false); }
  }, [status, operationID, search, orderPage]);

  useEffect(() => { void load(); const timer = window.setInterval(() => void load(true), 30_000); return () => window.clearInterval(timer); }, [load]);

  const planningOperationID = operationID || operations[0]?.id || "";
  const loadProposal = useCallback(async () => {
    if (!planningOperationID) return;
    setPlanningLoading(true);
    try {
      const query = new URLSearchParams({ operation_id: planningOperationID, date: planningDate });
      setProposal(await api<Proposal | null>(`planning-proposals?${query}`));
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Falha ao carregar planejamento."); }
    finally { setPlanningLoading(false); }
  }, [planningDate, planningOperationID]);

  useEffect(() => { void loadProposal(); }, [loadProposal]);

  async function createProposal() {
    if (!planningOperationID) return;
    setPlanningLoading(true); setActionMessage("");
    try {
      const result = await api<Proposal>("planning-proposals", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ operation_id: planningOperationID, operational_date: planningDate }) });
      setProposal(result); setActionMessage("Proposta calculada. Revise os grupos antes de aprovar.");
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Falha ao criar proposta."); }
    finally { setPlanningLoading(false); }
  }

  const filtered = useMemo(() => {
    const needle = search.trim().toLocaleLowerCase("pt-BR");
    if (!needle) return orders;
    return orders.filter((order) => [order.external_id, order.customer_display, order.address, order.defect_summary].some((value) => value.toLocaleLowerCase("pt-BR").includes(needle)));
  }, [orders, search]);
  const mapPoints = useMemo(() => geographicPoints(filtered), [filtered]);

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
          <button className="nav-item" onClick={() => document.getElementById("planning")?.scrollIntoView()}><Icon name="calendar" /> Planejamento</button>
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
            <div className="panel-heading"><div><p className="eyebrow">FILA OPERACIONAL</p><h2>Ordens de serviço</h2></div><span className="count-pill">{orderTotal} itens</span></div>
            <div className="filters">
              <label className="search"><Icon name="search" /><input value={search} onChange={(e) => { setSearch(e.target.value); setOrderPage(1); }} placeholder="Buscar por OS, cliente ou endereço" /></label>
              <select value={status} onChange={(e) => { setStatus(e.target.value); setOrderPage(1); }} aria-label="Filtrar situação"><option value="">Todas as situações</option><option value="open">Abertas</option><option value="scheduled">Agendadas</option><option value="closed">Encerradas</option><option value="unknown">Revisar</option></select>
              <select value={operationID} onChange={(e) => { setOperationID(e.target.value); setOrderPage(1); }} aria-label="Filtrar operação"><option value="">Todas as operações</option>{operations.map((operation) => <option key={operation.id} value={operation.id}>{operation.name}</option>)}</select>
            </div>
            <div className="table-wrap">
              <table><thead><tr><th>Ordem</th><th>Cliente / ocorrência</th><th>Localização</th><th>Situação</th><th aria-label="Ações" /></tr></thead>
                <tbody>{filtered.map((order) => <tr key={order.id} onClick={() => setSelected(order)}>
                  <td><strong>#{order.external_id.replace("SIM-OS-", "")}</strong><small>{order.type_description}</small></td>
                  <td><strong>{order.customer_display}</strong><small>{order.defect_summary}</small></td>
                  <td><span className="address"><Icon name="pin" />{order.address}</span>{order.location_needs_review && <small className="pending-text">posição pendente de revisão</small>}</td>
                  <td><span className={`status-badge ${order.status}`}>{statusLabels[order.status] ?? order.status}</span></td>
                  <td><button className="row-action" aria-label={`Abrir ${order.external_id}`}><Icon name="arrow" /></button></td>
                </tr>)}</tbody>
              </table>
              {!loading && filtered.length === 0 && <div className="empty-state"><Icon name="search" /><strong>Nenhuma ordem encontrada</strong><span>Ajuste os filtros para ampliar a busca.</span></div>}
            </div>
            {orderTotal > 100 && <div className="pagination"><button disabled={orderPage === 1} onClick={() => setOrderPage((page) => page - 1)}>Anterior</button><span>Página {orderPage} de {Math.ceil(orderTotal / 100)}</span><button disabled={orderPage >= Math.ceil(orderTotal / 100)} onClick={() => setOrderPage((page) => page + 1)}>Próxima</button></div>}
          </section>

          <section className="panel map-panel" id="map">
            <div className="panel-heading"><div><p className="eyebrow">VISÃO GEOGRÁFICA</p><h2>Coordenadas observadas</h2></div><span className="outline-pill">Geográfico</span></div>
            <div className="map-canvas">
              <div className="map-axis north">N</div><div className="map-axis south">S</div>
              {mapPoints.map(({ order, left, top }, index) => <button key={order.id} className={`order-pin ${order.location_needs_review ? "needs-review" : ""}`} style={{ left: `${left}%`, top: `${top}%` }} onClick={() => setSelected(order)} title={`${order.external_id}: ${order.latitude?.toFixed(5)}, ${order.longitude?.toFixed(5)}`}>{index + 1}</button>)}
              {mapPoints.length === 0 && <div className="map-empty">Nenhuma ordem filtrada possui coordenadas.</div>}
              <div className="map-legend"><span><i className="legend-pin" />Com posição confiável</span><span><i className="legend-pending" />{summary?.location_pending ?? 0} pendente(s)</span></div>
            </div>
            <p className="map-disclaimer">Posições relativas calculadas das coordenadas armazenadas; sem mapa viário, trajeto, ETA ou inferência de acesso.</p>
          </section>
        </div>

        <PlanningPanel proposal={proposal} teams={teams} date={planningDate} loading={planningLoading}
          onDateChange={setPlanningDate} onCreate={createProposal} onChange={setProposal}
          onMessage={setActionMessage} onReload={loadProposal} />
      </section>

      {selected && <OrderDrawer order={selected} onClose={() => setSelected(null)} onUpdated={(updated) => { setSelected(updated); setOrders((current) => current.map((order) => order.id === updated.id ? updated : order)); void load(true); }} />}
      {settingsOpen && <SettingsPanel operations={operations} types={types} onClose={() => setSettingsOpen(false)} onSaved={() => load(true)} />}
    </main>
  );
}

function PlanningPanel({ proposal, teams, date, loading, onDateChange, onCreate, onChange, onMessage, onReload }: {
  proposal: Proposal | null; teams: Team[]; date: string; loading: boolean; onDateChange: (date: string) => void;
  onCreate: () => void; onChange: (proposal: Proposal) => void; onMessage: (message: string) => void; onReload: () => Promise<void>;
}) {
  const editable = proposal?.status === "draft";
  const [preview, setPreview] = useState<SchedulingPreview | null>(null);
  const allTechnicians = useMemo(() => {
    const unique = new Map<string, TeamMember>();
    teams.flatMap((team) => team.members).forEach((member) => unique.set(member.id, member));
    return [...unique.values()].sort((a, b) => a.name.localeCompare(b.name, "pt-BR"));
  }, [teams]);

  function moveOrder(orderID: string, target: string) {
    if (!proposal || !editable) return;
    let moved: ProposalOrder | undefined;
    const groups = proposal.groups.map((group) => {
      const found = group.orders.find((order) => order.id === orderID);
      if (found) moved = found;
      return { ...group, orders: group.orders.filter((order) => order.id !== orderID) };
    });
    const pendingFound = proposal.pending.find((order) => order.id === orderID);
    if (pendingFound) moved = pendingFound;
    const pending = proposal.pending.filter((order) => order.id !== orderID);
    if (!moved) return;
    if (target === "pending") pending.push(moved);
    else {
      const group = groups.find((item) => item.id === target);
      if (group) group.orders.push(moved);
    }
    onChange({ ...proposal, groups, pending });
  }

  function addGroup() {
    if (!proposal || !editable) return;
    const position = proposal.groups.length + 1;
    onChange({ ...proposal, groups: [...proposal.groups, { id: `new-${crypto.randomUUID()}`, name: `Grupo ${position}`, position, is_fixed: false, orders: [] }] });
  }

  function removeGroup(groupID: string) {
    if (!proposal || !editable) return;
    const removed = proposal.groups.find((group) => group.id === groupID);
    if (!removed) return;
    onChange({ ...proposal, groups: proposal.groups.filter((group) => group.id !== groupID), pending: [...proposal.pending, ...removed.orders] });
  }

  async function save() {
    if (!proposal) return null;
    try {
      const updated = await api<Proposal>(`planning-proposals/${proposal.id}`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({
        version: proposal.version, team_id: proposal.team_id, agenda_responsible_id: proposal.agenda_responsible_id,
        groups: proposal.groups.filter((group) => group.orders.length > 0).map((group) => ({ name: group.name.trim(), is_fixed: group.is_fixed, order_ids: group.orders.map((order) => order.id) })),
        pending_order_ids: proposal.pending.map((order) => order.id)
      }) });
      onChange(updated); onMessage("Planejamento salvo. A versão local foi atualizada.");
      return updated;
    } catch (reason) { onMessage(reason instanceof Error ? reason.message : "Falha ao salvar planejamento."); await onReload(); return null; }
  }

  async function approve() {
    if (!proposal) return;
    try {
      const saved = await save();
      if (!saved) return;
      const approved = await api<Proposal>(`planning-proposals/${saved.id}/approve`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ version: saved.version }) });
      onChange(approved); onMessage("Proposta aprovada localmente. Nenhum agendamento foi enviado ao MK.");
    } catch (reason) { onMessage(reason instanceof Error ? reason.message : "Falha ao aprovar proposta."); await onReload(); }
  }

  async function refresh() {
    if (!proposal || proposal.status === "approved") return;
    try {
      const current = proposal.status === "draft" ? await save() : proposal;
      if (!current) return;
      const refreshed = await api<Proposal>(`planning-proposals/${current.id}/refresh`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ version: current.version }) });
      onChange(refreshed); onMessage("Proposta recalculada. Grupos fixados foram preservados e novas ordens foram incorporadas.");
    } catch (reason) { onMessage(reason instanceof Error ? reason.message : "Falha ao recalcular proposta."); await onReload(); }
  }

  async function loadPreview() {
    if (!proposal) return;
    try { setPreview(await api<SchedulingPreview>(`planning-proposals/${proposal.id}/scheduling-preview`)); }
    catch (reason) { onMessage(reason instanceof Error ? reason.message : "Falha ao preparar prévia."); }
  }

  const renderOrder = (order: ProposalOrder, current: string) => <article className="plan-order" key={order.id}>
    <div><strong>{order.external_id}</strong><span>{order.customer_display}</span><small>{order.address}</small></div>
    <select aria-label={`Destino de ${order.external_id}`} value={current} disabled={!editable} onChange={(event) => moveOrder(order.id, event.target.value)}>
      {proposal?.groups.map((group) => <option value={group.id} key={group.id}>{group.name}</option>)}
      <option value="pending">Pendência</option>
    </select>
  </article>;

  return <section className="panel planning-panel" id="planning">
    <div className="panel-heading planning-heading"><div><p className="eyebrow">PLANEJAMENTO DIÁRIO</p><h2>Proposta de atendimento</h2></div>
      <div className="planning-date"><label htmlFor="planning-date">Data operacional</label><input id="planning-date" type="date" value={date} onChange={(event) => onDateChange(event.target.value)} /></div>
    </div>
    {loading ? <div className="planning-empty">Carregando planejamento…</div> : !proposal ? <div className="planning-empty"><Icon name="calendar" /><strong>Nenhuma proposta para este dia</strong><span>O agrupamento usa distância em metros e mantém ordens sem posição como pendências.</span><button className="button primary" onClick={onCreate}>Gerar proposta</button></div> : <>
      <div className="planning-toolbar">
        <div><span className={`proposal-status ${proposal.status}`}>{proposal.status === "draft" ? "Rascunho" : proposal.status === "approved" ? "Aprovada" : "Conflito"}</span><small>versão {proposal.version} · {generatorLabel(proposal.generator)}</small></div>
        <label>Equipe<select value={proposal.team_id ?? ""} disabled={!editable} onChange={(event) => onChange({ ...proposal, team_id: event.target.value || null })}><option value="">Selecione</option>{teams.map((team) => <option value={team.id} key={team.id}>{team.name}</option>)}</select></label>
        <label>Agenda responsável<select value={proposal.agenda_responsible_id ?? ""} disabled={!editable} onChange={(event) => onChange({ ...proposal, agenda_responsible_id: event.target.value || null })}><option value="">Selecione</option>{allTechnicians.map((member) => <option value={member.id} key={member.id}>{member.name}</option>)}</select></label>
        {(proposal.status === "draft" || proposal.status === "conflicted") && <button className="button" onClick={refresh}><Icon name="refresh" /> Recalcular</button>}
        {editable && <button className="button" onClick={addGroup}>Adicionar grupo</button>}
      </div>
      <div className="planning-groups">
        {proposal.groups.map((group) => <section className="plan-group" key={group.id}>
          <header><input value={group.name} disabled={!editable} aria-label="Nome do grupo" onChange={(event) => onChange({ ...proposal, groups: proposal.groups.map((item) => item.id === group.id ? { ...item, name: event.target.value } : item) })} />
            {editable && <button className={group.is_fixed ? "fix-button fixed" : "fix-button"} title={group.is_fixed ? "Liberar grupo para recálculo" : "Preservar grupo no próximo recálculo"} onClick={() => onChange({ ...proposal, groups: proposal.groups.map((item) => item.id === group.id ? { ...item, is_fixed: !item.is_fixed } : item) })}>{group.is_fixed ? "Fixado" : "Fixar"}</button>}
            {!editable && group.is_fixed && <span className="fixed-label">Fixado</span>}<span>{group.orders.length} O.S.</span>{editable && <button title="Mover ordens para pendências e remover grupo" onClick={() => removeGroup(group.id)}>×</button>}</header>
          <div>{group.orders.map((order) => renderOrder(order, group.id))}{group.orders.length === 0 && <small className="group-empty">Grupo vazio</small>}</div>
        </section>)}
        <section className="plan-group pending-group"><header><strong>Pendências</strong><span>{proposal.pending.length} O.S.</span></header><div>{proposal.pending.map((order) => renderOrder(order, "pending"))}{proposal.pending.length === 0 && <small className="group-empty">Nenhuma pendência</small>}</div></section>
      </div>
      {editable && <footer className="planning-actions"><p>Salvar não altera o ERP. Aprovar congela esta versão local após revalidar cada ordem.</p><button className="button" onClick={save}>Salvar rascunho</button><button className="button primary" disabled={!proposal.team_id || !proposal.agenda_responsible_id} onClick={approve}>Aprovar proposta</button></footer>}
      {proposal.status === "approved" && <div className="approved-note"><Icon name="shield" /><span>Proposta aprovada localmente. O envio ao MK ainda não faz parte deste fluxo.</span><button className="button" onClick={loadPreview}>Abrir prévia</button></div>}
      {preview && <section className="scheduling-preview"><header><div><p className="eyebrow">PRÉVIA SEM ENVIO</p><h3>{preview.operation} · {preview.operational_date}</h3></div><button className="icon-button" onClick={() => setPreview(null)} aria-label="Fechar prévia">×</button></header><div className="preview-assignment"><span>Equipe<strong>{preview.team ?? "Não selecionada"}</strong></span><span>Agenda responsável<strong>{preview.agenda_responsible ?? "Não selecionada"}</strong></span></div><div className="preview-blockers">{preview.blockers.map((blocker) => <p key={blocker.code}><Icon name="shield" />{blocker.message}</p>)}</div><div className="preview-items">{preview.items.map((item) => <article key={item.order_id}><div><strong>{item.external_id}</strong><span>{item.customer_display}</span><small>{item.address}</small></div><span className={item.issues.length ? "preview-state blocked" : "preview-state ok"}>{item.issues.length ? `${item.issues.length} revisão(ões)` : "Dados coerentes"}</span>{item.issues.map((issue) => <small className="preview-issue" key={issue.code}>{issue.message}</small>)}</article>)}</div><footer><strong>Envio indisponível</strong><span>Nenhum comando foi criado e nenhuma chamada foi feita ao MK.</span></footer></section>}
      {proposal.status === "conflicted" && <div className="notice error">Uma ou mais ordens mudaram após a criação. Recalcule para remover itens inelegíveis, incorporar novidades e preservar os grupos fixados.</div>}
    </>}
  </section>;
}

function Metric({ label, value, detail, tone, icon, loading }: { label: string; value?: number; detail: string; tone: string; icon: string; loading: boolean }) {
  return <article className="metric"><div className={`metric-icon ${tone}`}><Icon name={icon} /></div><div><span>{label}</span><strong>{loading && value === undefined ? "—" : value ?? 0}</strong><small>{detail}</small></div></article>;
}

function OrderDrawer({ order, onClose, onUpdated }: { order: Order; onClose: () => void; onUpdated: (order: Order) => void }) {
  const [latitude, setLatitude] = useState(order.latitude?.toString() ?? "");
  const [longitude, setLongitude] = useState(order.longitude?.toString() ?? "");
  const [locationMessage, setLocationMessage] = useState("");
  const [saving, setSaving] = useState(false);
  async function saveLocation() {
    const lat = Number(latitude), lon = Number(longitude);
    if (!Number.isFinite(lat) || !Number.isFinite(lon) || lat < -90 || lat > 90 || lon < -180 || lon > 180) { setLocationMessage("Informe latitude e longitude válidas."); return; }
    setSaving(true);
    try {
      const updated = await api<Order>(`orders/${order.id}/location`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ latitude: lat, longitude: lon, expected_order_version: order.observed_version, expected_location_version: order.location_version }) });
      onUpdated(updated); setLocationMessage("Localização corrigida e marcada como revisão manual.");
    } catch (reason) { setLocationMessage(reason instanceof Error ? reason.message : "Falha ao corrigir localização."); }
    finally { setSaving(false); }
  }
  return <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}><aside className="drawer">
    <div className="drawer-head"><div><p className="eyebrow">DETALHES DA ORDEM</p><h2>{order.external_id}</h2></div><button className="icon-button" onClick={onClose} aria-label="Fechar">×</button></div>
    <span className={`status-badge ${order.status}`}>{statusLabels[order.status] ?? order.status}</span>
    <dl className="details"><div><dt>Cliente</dt><dd>{order.customer_display}</dd></div><div><dt>Ocorrência</dt><dd>{order.defect_summary}</dd></div><div><dt>Endereço observado</dt><dd>{order.address}</dd></div><div><dt>Posição</dt><dd>{order.latitude === null ? "Não resolvida — requer revisão" : `${order.latitude.toFixed(5)}, ${order.longitude?.toFixed(5)} (${order.location_source})`}{order.location_needs_review && " — revisar após mudança de endereço"}</dd></div><div><dt>Situação original</dt><dd>Código {order.source_status_code}</dd></div><div><dt>Última observação</dt><dd>{formatDate(order.observed_at)} · versão {order.observed_version}</dd></div></dl>
    <section className="location-editor"><h3>Correção manual da localização</h3><p>Use coordenadas confirmadas. A correção não altera o endereço original no ERP.</p><div><label>Latitude<input inputMode="decimal" value={latitude} onChange={(event) => setLatitude(event.target.value)} placeholder="-23.55052" /></label><label>Longitude<input inputMode="decimal" value={longitude} onChange={(event) => setLongitude(event.target.value)} placeholder="-46.63331" /></label></div><button className="button primary wide" disabled={saving} onClick={saveLocation}>{saving ? "Salvando…" : "Confirmar coordenadas"}</button>{locationMessage && <div className="notice">{locationMessage}</div>}</section>
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
