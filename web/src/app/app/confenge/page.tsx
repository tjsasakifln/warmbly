import { useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  ArrowRight,
  Ban,
  Check,
  CircleCheckBig,
  Loader2,
  MessageSquareText,
  RefreshCw,
  SkipForward,
} from "lucide-react";
import { Page, PageTopbar } from "@/components/layout/Page";
import { Checkbox } from "@/components/ui/checkbox";
import { TextInput } from "@/components/ui/field";
import {
  useApproveAndQueueTouchpoint,
  useConfengeAccount,
  useConfengeAccountTimeline,
  useConfengeAccounts,
  useConfengeAttention,
  useConfengeAttentionDetail,
  useConfengeDispatchStatus,
  useConfengeReviewTouchpoints,
  useConfengeStatus,
  useConfengeSummary,
  useConfengeWorkingOverview,
  useConfengeWorkingQueue,
  useDncConfengeAccount,
  useGenerateConfengeReplyDraft,
  useGenerateConfengeTouchpoint,
  usePauseConfengeDispatch,
  usePlanConfengeCadence,
  usePrepareConfengePilotCohort,
  useResumeConfengeDispatch,
  useRejectConfengeTouchpoint,
  useSkipConfengeTouchpoint,
  useSyncConfengeFeed,
} from "@/lib/api/hooks/app/confenge/useConfenge";
import type {
  ConfengeAttentionFilter,
  ConfengeTouchpoint,
  ConfengeWorkingQueueItem,
} from "@/lib/api/models/app/confenge/Confenge";
import type { AppError } from "@/lib/api/client/normalizeError";
import { channelLabel, formatFeedAge, formatPtBrDate, intentLabel, purposeLabel, reasonLabel, stateLabel } from "./labels";
import { buildPilotGate, type PilotGate } from "./pilotGate";

const ATTENTION_FILTERS: { id: ConfengeAttentionFilter; label: string }[] = [
  { id: "needs_attention", label: "Precisa de atenção" },
  { id: "awaiting_approval", label: "Aguardando aprovação" },
];

export default function ConfengePage() {
  const status = useConfengeStatus();
  const enabled = !!status.data?.enabled;
  const summary = useConfengeSummary(enabled);
  const workingOverview = useConfengeWorkingOverview(enabled);
  const agoraQueue = useConfengeWorkingQueue("agora", enabled);
  const needsContactQueue = useConfengeWorkingQueue("needs_contact", enabled);
  const ready = useConfengeAccounts("READY_TO_GENERATE", enabled);
  const review = useConfengeReviewTouchpoints(enabled);
  const plan = usePlanConfengeCadence();
  const prepareCohort = usePrepareConfengePilotCohort();
  const syncFeed = useSyncConfengeFeed();
  const genTouch = useGenerateConfengeTouchpoint();
  const approveQueue = useApproveAndQueueTouchpoint();
  const skip = useSkipConfengeTouchpoint();
  const reject = useRejectConfengeTouchpoint();
  const dnc = useDncConfengeAccount();
  const dispatchStatus = useConfengeDispatchStatus(enabled);
  const pauseDispatch = usePauseConfengeDispatch();
  const resumeDispatch = useResumeConfengeDispatch();
  const [filter, setFilter] = useState<ConfengeAttentionFilter>("needs_attention");
  const attention = useConfengeAttention(filter, enabled);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const detail = useConfengeAttentionDetail(selectedId);
  const generateReply = useGenerateConfengeReplyDraft();
  const [idx, setIdx] = useState(0);
  const queue = review.data ?? [];
  const current: ConfengeTouchpoint | undefined = queue[idx];
  const timeline = useConfengeAccountTimeline(current?.account_id ?? null);
  const reviewAccount = useConfengeAccount(current?.account_id ?? null);
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [recipient, setRecipient] = useState("");
  const [pilotSelection, setPilotSelection] = useState<string[]>([]);

  useEffect(() => {
    const previousLang = document.documentElement.lang;
    const previousTitle = document.title;
    document.documentElement.lang = "pt-BR";
    document.title = "Central comercial | CONFENGE";
    return () => {
      document.documentElement.lang = previousLang;
      document.title = previousTitle;
    };
  }, []);

  useEffect(() => {
    if (current) {
      setSubject(current.subject ?? "");
      setBody(current.body_text ?? "");
      setRecipient(current.recipient ?? "");
    }
  }, [current]);

  useEffect(() => {
    const list = attention.data ?? [];
    if (!selectedId && list.length) {
      setSelectedId(list[0].account_id);
    }
  }, [attention.data, selectedId]);

  const item = detail.data ?? (attention.data ?? []).find((a) => a.account_id === selectedId);

  const stats = useMemo(() => {
    const w = workingOverview.data;
    const s = summary.data;
    // Always expose Sent/Replied/DNC (E2E + ops glance). Working-overview
    // activation metrics sit alongside when dynamic priority is on.
    const core = [
      { id: "ready", label: "Prontas", value: s?.ready_to_generate ?? 0 },
      { id: "review", label: "Para revisar", value: s?.needs_review ?? w?.needs_review ?? 0 },
      { id: "sent", label: "Enviadas", value: s?.sent ?? 0 },
      { id: "human-reply", label: "Respostas", value: s?.replied ?? 0 },
      { id: "meeting", label: "Reuniões", value: s?.meeting ?? 0 },
      { id: "proposal", label: "Propostas", value: s?.proposal ?? 0 },
      { id: "won", label: "Ganhas", value: s?.won ?? 0 },
      { id: "dnc", label: "Não contatar", value: s?.do_not_contact ?? 0 },
    ];
    if (w) {
      return [
        { id: "reservoir", label: "Monitoradas", value: w.reservoir_monitored },
        { id: "now", label: "Para agir agora", value: w.actionable_now },
        { id: "review", label: "Para revisar", value: w.needs_review },
        { id: "sent", label: "Enviadas", value: s?.sent ?? 0 },
        { id: "replied", label: "Respondidas", value: s?.replied ?? 0 },
        { id: "rate", label: "Envios na hora", value: dispatchStatus.data ? `${dispatchStatus.data.sent_last_hour}/${dispatchStatus.data.cap}` : "—" },
      ];
    }
    if (!s) return core;
    return core;
  }, [summary.data, workingOverview.data, dispatchStatus.data]);

  const capacityLabel = useMemo(() => {
    const w = workingOverview.data;
    if (!w || !w.theoretical_slots_24h) return null;
    return `${w.due_next_24h} ações previstas para ${w.theoretical_slots_24h} oportunidades de envio nas próximas 24 horas`;
  }, [workingOverview.data]);

  const pilotGate = buildPilotGate({
    readiness: status.data?.readiness,
    summary: summary.data,
    overview: workingOverview.data,
    reviewCount: review.data?.length,
  });
  const blockersBeforeDispatch = pilotGate.blockers.filter((blocker) => blocker.id !== "dispatch");
  const canResumeDispatch =
    !pilotGate.evaluating &&
    blockersBeforeDispatch.length === 0 &&
    pilotGate.blockers.some((blocker) => blocker.id === "dispatch");
  const cohortRemaining = Math.max(0, pilotGate.target - pilotGate.prepared);
  const cohortResult = prepareCohort.data;
  const cohortCandidates = ready.data ?? [];
  const selectedCandidates = pilotSelection.filter((id) => cohortCandidates.some((account) => account.id === id));
  const unavailableForCohort = summary.data
    ? Math.max(
        0,
        summary.data.total -
          summary.data.needs_contact -
          summary.data.ready_to_generate -
          summary.data.needs_review -
          summary.data.approved -
          summary.data.enrolled -
          summary.data.sent -
          summary.data.replied -
          summary.data.meeting -
          summary.data.proposal -
          summary.data.won -
          summary.data.blocked -
          summary.data.bounced -
          summary.data.do_not_contact,
      )
    : 0;

  const togglePilotAccount = (accountId: string) => {
    setPilotSelection((currentSelection) => {
      const currentCandidates = currentSelection.filter((id) => cohortCandidates.some((account) => account.id === id));
      if (currentCandidates.includes(accountId)) {
        return currentCandidates.filter((id) => id !== accountId);
      }
      if (currentCandidates.length >= cohortRemaining) {
        return currentCandidates;
      }
      return [...currentCandidates, accountId];
    });
  };

  const selectPilotCandidates = () => {
    setPilotSelection(cohortCandidates.slice(0, cohortRemaining).map((account) => account.id));
  };

  if (status.isLoading) {
    return (
      <Page>
        <div className="flex items-center gap-2 text-slate-500 text-[12.5px] p-6">
          <Loader2 className="h-4 w-4 animate-spin" /> Carregando a operação…
        </div>
      </Page>
    );
  }
  if (status.isError) {
    const requestId = (status.error as unknown as AppError)?.request_id;
    return (
      <Page>
        <div className="m-auto max-w-md p-6 text-center">
          <h1 className="text-base font-semibold text-slate-900">Não foi possível carregar a operação</h1>
          <p className="mt-2 text-[12.5px] text-slate-600">Verifique a conexão com o servidor e tente novamente.</p>
          {requestId && <p className="mt-3 text-[11px] text-slate-400">Código de atendimento: {requestId}</p>}
        </div>
      </Page>
    );
  }
  if (!enabled) {
    return (
      <Page>
        <div className="m-auto max-w-md p-6 text-center">
          <h1 className="text-base font-semibold text-slate-900">CONFENGE não está habilitado</h1>
          <p className="mt-2 text-[12.5px] text-slate-600">Ative a operação CONFENGE no servidor para acessar filas, mensagens e respostas.</p>
        </div>
      </Page>
    );
  }

  const company =
    current?.account?.nome_fantasia ||
    current?.account?.razao_social ||
    current?.account_id?.slice(0, 8) ||
    "—";

  return (
    <Page>
      <PageTopbar
        eyebrow="CONFENGE"
        subtitle="Priorize contas, gere mensagens, aprove cada envio e acompanhe respostas."
      >
        {dispatchStatus.data && (
          <div
            id="controle-envios"
            tabIndex={-1}
            className="flex scroll-mt-16 items-center gap-2 mr-2 focus:outline-none"
          >
            <span
              className={`h-7 px-2.5 inline-flex items-center rounded-md border text-[12.5px] tabular-nums ${
                dispatchStatus.data.paused
                  ? "border-amber-200 bg-amber-50 text-amber-800"
                  : "border-slate-200 bg-white text-slate-700"
              }`}
              title={
                dispatchStatus.data.paused
                  ? `Envios pausados: ${reasonLabel(dispatchStatus.data.pause_reason || "manual_pause")}`
                  : `Próximo envio: ${formatPtBrDate(dispatchStatus.data.next_slot_at) || "disponível"} · ${dispatchStatus.data.queued_approved} na fila`
              }
              data-testid="confenge-dispatch-quota"
            >
              {dispatchStatus.data.sent_last_hour}/{dispatchStatus.data.cap} na última hora
            </span>
            {dispatchStatus.data.paused ? (
              <button
                type="button"
                className="h-7 px-2.5 rounded-md border border-sky-200 bg-sky-50 text-sky-700 text-[12.5px] hover:bg-sky-100 disabled:cursor-not-allowed disabled:border-slate-200 disabled:bg-slate-100 disabled:text-slate-400"
                disabled={resumeDispatch.isPending || !canResumeDispatch}
                title={
                  canResumeDispatch
                    ? "Todos os gates anteriores estão concluídos. Confirme a retomada controlada."
                    : "Conclua todas as pendências críticas antes de retomar os envios."
                }
                onClick={() => resumeDispatch.mutate()}
              >
                Retomar envios
              </button>
            ) : (
              <button
                type="button"
                className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px] text-slate-700 hover:bg-slate-50"
                disabled={pauseDispatch.isPending}
                onClick={() => pauseDispatch.mutate("manual_pause")}
              >
                Pausar envios
              </button>
            )}
          </div>
        )}
      </PageTopbar>
      <div className="flex flex-col gap-4 p-4 md:p-6 max-w-6xl mx-auto w-full">
        <PilotCriticalPanel gate={pilotGate} />

        <section id="visao-geral" tabIndex={-1} className="scroll-mt-16 rounded-md border border-sky-200 bg-sky-50 px-3 py-3 focus:outline-none">
          <div className="text-[10px] uppercase tracking-[0.14em] text-sky-700 font-medium">Próxima ação</div>
          <p className="mt-1 text-[12.5px] text-slate-800">
            {pilotGate.blockers[0]
              ? `${pilotGate.blockers[0].title}. ${pilotGate.blockers[0].detail}`
              : dispatchStatus.data?.paused
                ? "Todos os gates anteriores estão concluídos. Faça a liberação humana final dos envios."
              : queue.length
                ? `Revise ${queue.length} ${queue.length === 1 ? "mensagem" : "mensagens"} antes de liberar os próximos envios.`
                : (agoraQueue.data?.length ?? 0) > 0
                  ? "Planeje as contas prontas para ação e gere as mensagens que vencem agora."
                  : "A operação está em dia. Acompanhe novas contas, respostas e atualizações do feed."}
          </p>
          <div className="mt-2 grid gap-2 sm:grid-cols-3 text-[11.5px] text-slate-600">
            <span><strong className="text-slate-800">1. Selecione</strong> a conta prioritária.</span>
            <span><strong className="text-slate-800">2. Revise</strong> destinatário, contexto e mensagem.</span>
            <span><strong className="text-slate-800">3. Aprove</strong> o conteúdo exato para colocá-lo na fila.</span>
          </div>
        </section>

        {status.data?.readiness && (
          <section className="rounded-md border border-slate-200 bg-white px-3 py-3">
            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Condições da operação</div>
            <p className="mt-1 text-[11.5px] text-slate-500">Mostra se dados, geração e envio estão prontos para o trabalho diário.</p>
            <div className="mt-2 flex flex-wrap gap-2 text-[11.5px]">
              <ReadinessItem label="E-mail" value={stateLabel(status.data.readiness.email)} />
              <ReadinessItem label="Feed de dados" value={status.data.readiness.feed_configured ? `Atualizado ${formatFeedAge(status.data.readiness.feed_age_seconds)}` : "Não configurado"} />
              <ReadinessItem label="Geração" value={stateLabel(status.data.readiness.ai)} />
              <ReadinessItem label="Envios" value={status.data.readiness.sending_allowed ? "Liberados" : "Pausados"} />
              <ReadinessItem label="Limite por hora" value={String(status.data.readiness.governor_cap)} />
              <ReadinessItem label="Capacidade diária" value={String(status.data.readiness.effective_daily_cap)} />
            </div>
          </section>
        )}

        <section id="indicadores" tabIndex={-1} className="scroll-mt-16 focus:outline-none">
          <div className="mb-2">
            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Visão geral</div>
            <p className="text-[11.5px] text-slate-500">Resumo do funil comercial e do ritmo de envio.</p>
          </div>
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-2">
            {stats.map((s) => (
              <div key={s.id} className="rounded-md border border-slate-200 bg-white px-2.5 py-2">
                <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">{s.label}</div>
                <div
                  className="text-lg font-semibold text-slate-900 tabular-nums"
                  data-testid={`confenge-stat-${s.id}`}
                >
                  {s.value}
                </div>
              </div>
            ))}
          </div>
        </section>
        {capacityLabel && (
          <div
            className="text-[12.5px] text-slate-600 tabular-nums"
            data-testid="confenge-capacity-load"
          >
            Capacidade de planejamento: {capacityLabel}.
            {workingOverview.data?.dynamic_priority_enabled ? " Priorização dinâmica ativa." : " Priorização dinâmica em observação."}
          </div>
        )}
        <p className="text-[12.5px] text-slate-500" data-testid="confenge-activation-hint">
          A prioridade comercial vem da análise de dados e serve para ordenar o trabalho, não para prever uma compra.
          Conta com contexto desatualizado exige regenerar e reaprovar antes do envio.
        </p>

        {/* Agora — ACTIONABLE_NOW + operationally due */}
        <section id="agir-agora" tabIndex={-1} className="scroll-mt-16 rounded-md border border-slate-200 bg-white focus:outline-none" data-testid="confenge-agora">
          <div className="shrink-0 px-3 h-10 flex items-center border-b border-slate-200">
            <span className="text-[12.5px] font-medium text-slate-900">Agora</span>
            <span className="ml-2 text-[12.5px] text-slate-500 tabular-nums">
              {(agoraQueue.data ?? []).length}
            </span>
          </div>
          <p className="px-3 pt-2 text-[11.5px] text-slate-500">Contas cuja próxima ação já pode ser planejada ou executada.</p>
          <WorkingLaneList
            items={agoraQueue.data ?? []}
            empty="Nenhuma conta exige ação agora. Você pode seguir para a fila de revisão."
            onPlan={(id) => plan.mutate({ accountId: id })}
          />
        </section>

        {/* Precisa de contato */}
        <section id="precisa-contato" tabIndex={-1} className="scroll-mt-16 rounded-md border border-slate-200 bg-white focus:outline-none" data-testid="confenge-needs-contact">
          <div className="shrink-0 px-3 h-10 flex items-center border-b border-slate-200">
            <span className="text-[12.5px] font-medium text-slate-900">Precisa de contato</span>
            <span className="ml-2 text-[12.5px] text-slate-500 tabular-nums">
              {(needsContactQueue.data ?? []).length}
            </span>
          </div>
          <p className="px-3 pt-2 text-[11.5px] text-slate-500">Contas priorizadas que ainda não possuem um destinatário elegível.</p>
          <WorkingLaneList
            items={needsContactQueue.data ?? []}
            empty="Nenhuma conta prioritária sem destinatário"
            showContactGap
          />
        </section>

        {/* Needs attention cockpit */}
        <section id="pendencias" tabIndex={-1} className="scroll-mt-16 rounded-md border border-slate-200 bg-white focus:outline-none" data-testid="confenge-needs-attention">
          <div className="shrink-0 px-3 flex items-center gap-1 border-b border-slate-200 overflow-x-auto">
            {ATTENTION_FILTERS.map((f) => {
              const active = filter === f.id;
              return (
                <button
                  key={f.id}
                  type="button"
                  onClick={() => setFilter(f.id)}
                  className={
                    "relative h-10 px-2.5 inline-flex items-center gap-1.5 text-[12.5px] " +
                    (active ? "text-slate-900 font-medium" : "text-slate-500 hover:text-slate-800")
                  }
                >
                  {f.label}
                  {active && (
                    <span className="absolute left-2 right-2 bottom-0 h-0.5 bg-sky-600 rounded-full" />
                  )}
                </button>
              );
            })}
          </div>
          <p className="px-3 pt-2 text-[11.5px] text-slate-500">Respostas recebidas e situações que dependem de uma decisão humana.</p>
          <div className="grid md:grid-cols-5 min-h-[200px]">
            <ul className="md:col-span-2 divide-y divide-slate-100 max-h-72 overflow-auto border-r border-slate-100">
              {(attention.data ?? []).map((a) => {
                const sel = a.account_id === selectedId;
                return (
                  <li key={a.account_id}>
                    <button
                      type="button"
                      onClick={() => setSelectedId(a.account_id)}
                      className={
                        "w-full text-left px-3 py-2.5 text-[12.5px] " +
                        (sel ? "bg-sky-50" : "hover:bg-slate-50")
                      }
                    >
                      <div className="font-medium text-slate-900 truncate">
                        {a.company_name || a.cnpj14}
                      </div>
                      <div className="text-slate-500 truncate">
                        {a.intent ? intentLabel(a.intent) : stateLabel(a.commercial_state || a.queue_state)}
                        {a.contact_email ? ` · ${a.contact_email}` : ""}
                      </div>
                    </button>
                  </li>
                );
              })}
              {!attention.data?.length && (
                <li className="px-3 py-8 text-center text-slate-400 text-[12.5px]">
                  Nenhuma pendência neste filtro.
                </li>
              )}
            </ul>
            <div className="md:col-span-3 p-3 space-y-2 text-[12.5px]">
              {!item ? (
                <div className="py-10 text-center text-slate-400">
                  Selecione uma conta para consultar o contexto e a resposta.
                </div>
              ) : (
                <>
                  <div>
                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Empresa</div>
                    <div className="font-medium">{item.company_name || "—"}</div>
                  </div>
                  <div>
                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Intenção identificada</div>
                    <div data-testid="confenge-attention-intent">
                      {item.intent ? intentLabel(item.intent) : stateLabel(item.commercial_state)}
                    </div>
                  </div>
                  <div>
                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Conversa</div>
                    <div className="text-slate-600">
                      {[item.thread_subject, item.thread || item.last_snippet].filter(Boolean).join(" · ") ||
                        "—"}
                    </div>
                  </div>
                  <button
                    type="button"
                    data-testid="confenge-generate-reply"
                    disabled={generateReply.isPending || item.do_not_contact || item.blocked}
                    onClick={() => generateReply.mutate({ accountId: item.account_id })}
                    className="h-7 px-2.5 inline-flex items-center gap-1 rounded-md border border-sky-200 bg-sky-50 text-sky-700 text-[12.5px] hover:bg-sky-100 disabled:opacity-50"
                  >
                    <MessageSquareText className="h-3.5 w-3.5" />
                    Gerar rascunho de resposta
                  </button>
                  <p className="text-[11px] text-slate-400">
                    O rascunho ficará aguardando aprovação. A IA nunca envia automaticamente.
                  </p>
                </>
              )}
            </div>
          </div>
        </section>

        <section id="planejamento" tabIndex={-1} className="scroll-mt-16 rounded-md border border-slate-200 bg-white focus:outline-none">
          <div className="px-3 min-h-10 py-1.5 flex flex-wrap items-center gap-2 border-b border-slate-200">
            <div>
              <div className="text-[12.5px] font-medium text-slate-900">Cohort manual do piloto</div>
              <div className="text-[10.5px] text-slate-500">
                {(cohortResult?.cohort_prepared ?? pilotGate.prepared)}/{pilotGate.target} preparadas · {cohortCandidates.length} elegíveis
              </div>
            </div>
            {cohortCandidates.length > 0 && cohortRemaining > 0 && (
              <div className="ml-auto flex items-center gap-1.5">
                <button
                  type="button"
                  className="h-7 px-2.5 rounded-md border border-slate-200 text-[12px] text-slate-700 hover:bg-slate-50 disabled:opacity-50"
                  disabled={prepareCohort.isPending}
                  onClick={selectPilotCandidates}
                >
                  Selecionar até {Math.min(cohortRemaining, cohortCandidates.length)}
                </button>
                <button
                  type="button"
                  className="h-7 px-2.5 rounded-md bg-sky-600 text-white text-[12px] font-medium hover:bg-sky-700 disabled:opacity-50"
                  disabled={prepareCohort.isPending || selectedCandidates.length === 0}
                  onClick={() =>
                    prepareCohort.mutate(selectedCandidates, {
                      onSuccess: () => setPilotSelection([]),
                    })
                  }
                >
                  {prepareCohort.isPending ? (
                    <><Loader2 className="inline h-3.5 w-3.5 mr-1 animate-spin" /> Preparando cohort…</>
                  ) : (
                    `Preparar ${selectedCandidates.length} selecionadas`
                  )}
                </button>
              </div>
            )}
          </div>
          <p className="px-3 pt-2 text-[11.5px] text-slate-500">Selecione as empresas elegíveis que farão parte do piloto. Cada primeira mensagem será gerada e continuará exigindo revisão e aprovação individual.</p>
          <div className="mx-3 mt-2 rounded-md border border-slate-200 bg-slate-50 px-3 py-2" data-testid="confenge-cohort-operation-summary">
            <div className="flex flex-wrap gap-x-4 gap-y-1 text-[12px] tabular-nums text-slate-700">
              <span><strong>{cohortResult?.selected ?? selectedCandidates.length}</strong> selecionadas</span>
              <span className="text-emerald-700"><strong>{cohortResult?.prepared ?? 0}</strong> preparadas</span>
              <span className="text-rose-700"><strong>{cohortResult?.blocked ?? 0}</strong> bloqueadas</span>
              <span><strong>{cohortResult?.remaining ?? cohortRemaining}</strong> restantes para 30</span>
              {(cohortResult?.contact_needed ?? 0) > 0 && <span className="text-amber-700"><strong>{cohortResult!.contact_needed}</strong> precisam de contato</span>}
            </div>
            {cohortResult && (
              <div className="mt-2 space-y-1.5" data-testid="confenge-cohort-results">
                {cohortResult.results.map((account) => (
                  <div
                    key={account.account_id}
                    className={
                      "rounded-md border px-2.5 py-2 text-[11.5px] " +
                      (account.status === "PREPARED"
                        ? "border-emerald-200 bg-emerald-50 text-emerald-900"
                        : "border-rose-200 bg-rose-50 text-rose-900")
                    }
                  >
                    <div className="flex items-start gap-1.5">
                      {account.status === "PREPARED" ? (
                        <CircleCheckBig className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                      ) : (
                        <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                      )}
                      <div className="min-w-0">
                        <div className="font-medium">{account.company || account.cnpj14 || account.account_id}</div>
                        <div className="text-[10.5px] opacity-80">CNPJ {account.cnpj14 || "não informado"}</div>
                        {account.status === "PREPARED" ? (
                          <div>Preparada em <strong>needs_review</strong> para {account.recipient || "destinatário validado"}{account.idempotent ? " (já existente, sem duplicação)" : ""}.</div>
                        ) : (
                          <>
                            <div><strong>Bloqueada:</strong> {account.human_readable_reason || reasonLabel(account.reason_code)}</div>
                            <div><strong>Como resolver:</strong> {account.remediation || "Use o reason code exibido para revisar os dados da conta."}</div>
                            <div className="font-mono text-[10px] opacity-75">reason_code={account.reason_code || "missing_reason_code"}</div>
                          </>
                        )}
                        {(account.warnings?.length ?? 0) > 0 && (
                          <div className="mt-0.5">Avisos: {account.warnings!.map(reasonLabel).join(", ")}</div>
                        )}
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
          <ul className="divide-y divide-slate-100 max-h-48 overflow-auto">
            {(ready.data ?? []).map((a) => (
              <li key={a.id} className="px-3 py-2 flex items-center justify-between gap-2 text-[12.5px]">
                <Checkbox
                  checked={selectedCandidates.includes(a.id)}
                  disabled={prepareCohort.isPending || (!selectedCandidates.includes(a.id) && selectedCandidates.length >= cohortRemaining)}
                  aria-label={`Selecionar ${a.nome_fantasia || a.razao_social} para o cohort`}
                  className="border-slate-300 data-[state=checked]:border-sky-600 data-[state=checked]:bg-sky-600 data-[state=checked]:text-white"
                  onCheckedChange={() => togglePilotAccount(a.id)}
                />
                <div className="min-w-0">
                  <div className="font-medium truncate">{a.nome_fantasia || a.razao_social}</div>
                  <div className="text-slate-500 truncate">
                    {a.cnpj14} · {a.service_code}
                  </div>
                </div>
                <button
                  type="button"
                  className="shrink-0 h-7 px-2 rounded-md bg-sky-50 text-sky-700 border border-sky-200"
                  disabled={plan.isPending}
                  onClick={() =>
                    plan.mutate(
                      { accountId: a.id, channel: "EMAIL" },
                      {
                        onSuccess: (list) => {
                          const due = list.find((t) => t.state === "DUE" || t.ordinal === 1);
                          if (due) genTouch.mutate(due.id);
                        },
                      },
                    )
                  }
                >
                  Planejar e gerar
                </button>
              </li>
            ))}
            {!ready.data?.length && (
              <li className="px-3 py-6 text-center text-[12.5px]">
                <div className="text-slate-600">Nenhuma conta está elegível para o cohort manual.</div>
                <div className="mx-auto mt-3 grid max-w-md grid-cols-2 gap-2 text-left">
                  <div className="rounded-md border border-slate-200 bg-slate-50 px-2.5 py-2">
                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-400">Monitoradas</div>
                    <div className="mt-0.5 text-base font-semibold tabular-nums text-slate-900">{summary.data?.total ?? 0}</div>
                  </div>
                  <div className="rounded-md border border-amber-200 bg-amber-50 px-2.5 py-2">
                    <div className="text-[10px] uppercase tracking-[0.14em] text-amber-700">Suprimidas por target-fit</div>
                    <div className="mt-0.5 text-base font-semibold tabular-nums text-amber-900">{unavailableForCohort}</div>
                  </div>
                </div>
                <div className="mx-auto mt-2 max-w-lg text-[11px] text-slate-500">A base contém empresas, mas elas não podem entrar no go-live até o feed confirmar target-fit válido, contato e prontidão de envio.</div>
                <button
                  type="button"
                  className="mt-3 h-7 px-2.5 rounded-md border border-sky-200 bg-sky-50 text-sky-700 text-[12px] hover:bg-sky-100 disabled:opacity-50"
                  disabled={syncFeed.isPending}
                  onClick={() => syncFeed.mutate()}
                >
                  {syncFeed.isPending && <Loader2 className="inline h-3.5 w-3.5 mr-1 animate-spin" />}
                  Verificar nova versão do feed
                </button>
              </li>
            )}
          </ul>
        </section>

        <section id="revisao" tabIndex={-1} className="scroll-mt-16 rounded-md border border-slate-200 bg-white focus:outline-none" data-testid="confenge-review-queue">
          <div className="px-3 h-10 flex items-center justify-between border-b border-slate-200">
            <span className="text-[12.5px] font-medium">
              Fila de revisão ({queue.length})
              {current ? ` · ${idx + 1}/${queue.length}` : ""}
            </span>
            {current && (
              <span className="text-[10px] uppercase tracking-[0.14em] px-1.5 py-0.5 rounded bg-sky-50 text-sky-700">
                Etapa {current.ordinal} · {channelLabel(current.channel)} · {stateLabel(current.state)}
              </span>
            )}
          </div>
          <p className="px-3 pt-2 text-[11.5px] text-slate-500">Confira destinatário, contexto e conteúdo exato. Somente mensagens aprovadas entram na fila de envio.</p>
          {!current ? (
            <div className="px-3 py-10 text-center text-slate-400 text-[12.5px]">
              Nenhuma mensagem aguarda revisão. Planeje uma conta e gere a mensagem quando ela estiver pronta.
            </div>
          ) : (
            <div className="p-3 grid md:grid-cols-2 gap-4">
              <div className="space-y-2 text-[12.5px]">
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Empresa</div>
                  <div className="font-medium text-slate-900" data-testid="confenge-company">
                    {company}
                  </div>
                  <div className="text-[11px] text-slate-500">CNPJ {current.account?.cnpj14 || reviewAccount.data?.cnpj14 || "não informado"}</div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Destinatário exato
                  </div>
                  <div className="font-medium break-all" data-testid="confenge-recipient">
                    {current.channel === "WHATSAPP" ? "Telefone" : "E-mail"}: {current.recipient || "—"}
                  </div>
                  {(current.draft?.recipient_name || current.draft?.recipient_role) && (
                    <div className="text-[11px] text-slate-500">
                      {[current.draft?.recipient_name, current.draft?.recipient_role].filter(Boolean).join(" · ")}
                    </div>
                  )}
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Canal e serviço
                  </div>
                  <div data-testid="confenge-channel-service">
                    {channelLabel(current.channel)} · {current.service_code || "—"}
                  </div>
                </div>

                {/* Operator strategy cockpit (not prospect-facing) */}
                <div
                  className="rounded-md border border-slate-200 bg-slate-50 p-2 space-y-1.5"
                  data-testid="confenge-strategy-explain"
                >
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Estratégia aplicada
                  </div>
                  <StrategyRow label="Por que agora" value={current.strategy_explain?.why_now || current.account?.moment_summary} />
                  <StrategyRow label="Fato usado" value={current.strategy_explain?.fact_used || current.fact_used} />
                  <StrategyRow label="Hipótese" value={current.strategy_explain?.hypothesis} />
                  <StrategyRow label="Serviço" value={current.strategy_explain?.service || current.service_code} />
                  <StrategyRow label="Oferta" value={current.strategy_explain?.offer} />
                  <StrategyRow label="Destinatário" value={current.strategy_explain?.recipient || current.recipient} />
                  <StrategyRow
                    label="Fontes"
                    value={(current.strategy_explain?.sources || current.evidence_ids || []).join(", ")}
                  />
                  <StrategyRow label="Etapa" value={current.strategy_explain?.touch || String(current.ordinal)} />
                  <StrategyRow label="Experimento" value={current.strategy_explain?.experiment} />
                </div>
                {(current.doctrine_alerts?.length ?? 0) > 0 && (
                  <div className="space-y-1" data-testid="confenge-doctrine-alerts">
                    {current.doctrine_alerts!.map((a) => (
                      <div
                        key={a}
                        className="text-[11px] text-amber-800 bg-amber-50 border border-amber-100 rounded px-2 py-1"
                      >
                        ⚠ {reasonLabel(a)}
                      </div>
                    ))}
                  </div>
                )}
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Evidências utilizadas
                  </div>
                  <div data-testid="confenge-evidence">{current.fact_used || "—"}</div>
                  <ul className="mt-1 space-y-1 text-[11px] text-slate-600">
                    {(reviewAccount.data?.evidence ?? [])
                      .filter((evidence) => (current.evidence_ids ?? []).includes(evidence.id) || (current.evidence_ids ?? []).includes(evidence.source_evidence_id))
                      .map((evidence) => (
                        <li key={evidence.id} className="rounded border border-slate-100 bg-slate-50 px-2 py-1">
                          <div>{evidence.title || evidence.synthesis || evidence.excerpt || evidence.source_evidence_id}</div>
                          <div className="text-[10px] text-slate-400">
                            {[formatPtBrDate(evidence.evidence_date), evidence.reliability, evidence.epistemic_class].filter(Boolean).join(" · ")}
                          </div>
                        </li>
                      ))}
                  </ul>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Status e timestamps</div>
                  <div>{stateLabel(current.state)} · criada {formatPtBrDate(current.created_at) || "sem data"} · atualizada {formatPtBrDate(current.updated_at) || "sem data"}</div>
                  {(current.draft?.risk_flags?.length ?? 0) > 0 && (
                    <div className="mt-1 text-amber-700">Avisos: {current.draft!.risk_flags!.map(reasonLabel).join(", ")}</div>
                  )}
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Histórico</div>
                  <ul className="max-h-24 overflow-auto text-slate-600">
                    {(timeline.data ?? []).map((t) => (
                      <li key={t.id}>
                        #{t.ordinal} {purposeLabel(t.purpose)} · {channelLabel(t.channel)} · {stateLabel(t.state)}
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
              <div className="space-y-2">
                <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                  Destinatário exato
                  <TextInput
                    data-testid="confenge-recipient-input"
                    className="mt-1 w-full"
                    value={recipient}
                    onChange={setRecipient}
                  />
                </label>
                {current.channel !== "WHATSAPP" && (
                  <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Assunto
                    <TextInput
                      data-testid="confenge-subject-input"
                      className="mt-1 w-full"
                      value={subject}
                      onChange={setSubject}
                    />
                  </label>
                )}
                <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                  Conteúdo exato que será enviado
                  <textarea
                    data-testid="confenge-body-input"
                    className="mt-1 w-full min-h-[160px] rounded-md border border-slate-200 px-2 py-1.5 text-[12.5px]"
                    value={body}
                    onChange={(e) => setBody(e.target.value)}
                  />
                </label>
                <div className="flex flex-wrap gap-1.5 pt-1">
                  {(current.state === "DUE" || !current.body_text) && (
                    <button
                      type="button"
                      className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px]"
                      disabled={genTouch.isPending}
                      onClick={() => genTouch.mutate(current.id)}
                    >
                      <RefreshCw className="inline h-3.5 w-3.5 mr-1" />
                      Gerar mensagem
                    </button>
                  )}
                  <button
                    type="button"
                    data-testid="confenge-approve-queue"
                    className="h-7 px-2.5 rounded-md bg-sky-600 text-white text-[12.5px] disabled:opacity-50"
                    disabled={approveQueue.isPending || !body.trim() || !recipient.trim()}
                    onClick={() =>
                      approveQueue.mutate({
                        id: current.id,
                        subject,
                        body_text: body,
                        recipient,
                      })
                    }
                  >
                    <Check className="inline h-3.5 w-3.5 mr-1" />
                    Aprovar e colocar na fila
                  </button>
                  <button
                    type="button"
                    className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px]"
                    disabled={skip.isPending}
                    onClick={() =>
                      skip.mutate(current.id, {
                        onSuccess: () => setIdx((i) => Math.min(i, Math.max(0, queue.length - 2))),
                      })
                    }
                  >
                    <SkipForward className="inline h-3.5 w-3.5 mr-1" />
                    Ignorar etapa
                  </button>
                  <button
                    type="button"
                    className="h-7 px-2.5 rounded-md border border-rose-200 text-rose-700 text-[12.5px]"
                    disabled={dnc.isPending}
                    onClick={() => dnc.mutate(current.account_id)}
                  >
                    <Ban className="inline h-3.5 w-3.5 mr-1" />
                    Não contatar
                  </button>
                </div>
                <div
                  className="flex flex-wrap gap-1 pt-1"
                  data-testid="confenge-reject-reasons"
                >
                  <span className="text-[10px] uppercase tracking-[0.14em] text-slate-400 w-full">
                    Motivo da rejeição
                  </span>
                  {(
                    [
                      "factual_error",
                      "too_generic",
                      "too_salesy",
                      "wrong_offer",
                      "unsupported_claim",
                      "creepy",
                      "too_long",
                      "tone",
                      "other",
                    ] as const
                  ).map((reason) => (
                    <button
                      key={reason}
                      type="button"
                      disabled={reject.isPending}
                      className="h-6 px-2 rounded border border-slate-200 text-[11px] text-slate-600 hover:bg-rose-50 hover:border-rose-200 hover:text-rose-700 disabled:opacity-50"
                      onClick={() =>
                        reject.mutate(
                          { id: current.id, reason },
                          {
                            onSuccess: () =>
                              setIdx((i) => Math.min(i, Math.max(0, queue.length - 2))),
                          },
                        )
                      }
                    >
                      {reasonLabel(reason)}
                    </button>
                  ))}
                </div>
                <p className="text-[11px] text-slate-400">
                  Qualquer edição remove a aprovação anterior. Cada mensagem deve ser aprovada separadamente.
                </p>
              </div>
            </div>
          )}
        </section>
      </div>
    </Page>
  );
}

function PilotCriticalPanel({ gate }: { gate: PilotGate }) {
  const [navigationStatus, setNavigationStatus] = useState("");

  const navigateToBlocker = (href: string, title: string) => {
    const targetId = href.replace(/^#/, "");
    const target = document.getElementById(targetId);
    if (!target) {
      setNavigationStatus("A área indicada não está disponível.");
      return;
    }

    window.history.replaceState(null, "", href);
    target.scrollIntoView({ behavior: "smooth", block: "start" });
    target.focus({ preventScroll: true });
    setNavigationStatus(`Abrindo: ${title}.`);

    if (!window.matchMedia("(prefers-reduced-motion: reduce)").matches && typeof target.animate === "function") {
      target.animate(
        [
          { boxShadow: "0 0 0 0 rgba(2, 132, 199, 0)" },
          { boxShadow: "0 0 0 4px rgba(2, 132, 199, 0.28)" },
          { boxShadow: "0 0 0 0 rgba(2, 132, 199, 0)" },
        ],
        { duration: 1_400, easing: "ease-out" },
      );
    }
  };

  if (gate.evaluating) {
    return (
      <section
        id="pendencias-criticas"
        className="scroll-mt-16 rounded-md border border-slate-200 bg-white px-4 py-4"
        aria-busy="true"
      >
        <div className="flex items-center gap-2 text-[12.5px] text-slate-500">
          <Loader2 className="h-4 w-4 animate-spin" /> Verificando os gates do piloto…
        </div>
      </section>
    );
  }

  if (gate.ready) {
    return (
      <section
        id="pendencias-criticas"
        className="scroll-mt-16 rounded-md border-2 border-emerald-300 bg-emerald-50 px-4 py-4 shadow-sm"
        data-testid="confenge-pilot-gate"
      >
        <div className="flex items-start gap-3">
          <CircleCheckBig className="mt-0.5 h-6 w-6 shrink-0 text-emerald-600" />
          <div>
            <div className="text-[10px] font-bold uppercase tracking-[0.18em] text-emerald-700">Pronto para piloto controlado</div>
            <p className="mt-1 text-sm font-semibold text-emerald-950">Todos os gates estão verdes.</p>
            <p className="mt-1 text-[12px] text-emerald-800">As {gate.target} mensagens foram preparadas e liberadas individualmente.</p>
          </div>
        </div>
      </section>
    );
  }

  const progress = Math.min(100, Math.round((gate.released / gate.target) * 100));

  return (
    <section
      id="pendencias-criticas"
      role="alert"
      tabIndex={-1}
      className="scroll-mt-16 overflow-hidden rounded-md border-2 border-rose-400 bg-rose-50 shadow-[0_0_0_4px_rgba(244,63,94,0.08)] focus:outline-none"
      data-testid="confenge-pilot-gate"
    >
      <div className="flex flex-col gap-4 border-b border-rose-200 bg-rose-100/70 px-4 py-4 sm:flex-row sm:items-center">
        <div className="flex min-w-0 items-start gap-3">
          <span className="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-rose-600 text-white shadow-sm">
            <AlertTriangle className="h-5 w-5" />
          </span>
          <div className="min-w-0">
            <div className="text-[10px] font-bold uppercase tracking-[0.2em] text-rose-700">Piloto bloqueado</div>
            <h2 className="mt-0.5 text-base font-bold text-rose-950">
              {gate.blockers.length} {gate.blockers.length === 1 ? "pendência crítica impede" : "pendências críticas impedem"} a operação real
            </h2>
            <p className="mt-1 text-[12px] text-rose-800">Nenhum envio será retomado enquanto os gates anteriores não forem concluídos.</p>
          </div>
        </div>
        <div className="sm:ml-auto sm:w-56 shrink-0 rounded-md border border-rose-200 bg-white px-3 py-2">
          <div className="flex items-center justify-between text-[11px] font-medium text-slate-700">
            <span>Aprovação manual</span>
            <span className="tabular-nums text-rose-700">{gate.released}/{gate.target}</span>
          </div>
          <div className="mt-2 h-2 overflow-hidden rounded-full bg-rose-100">
            <div className="h-full rounded-full bg-rose-600 transition-[width]" style={{ width: `${progress}%` }} />
          </div>
          <div className="mt-1 text-[10.5px] text-slate-500">{gate.prepared}/{gate.target} mensagens preparadas</div>
        </div>
      </div>
      <ol className="grid gap-px bg-rose-200 sm:grid-cols-2">
        {gate.blockers.map((blocker, index) => (
          <li key={blocker.id} className="bg-white px-4 py-3">
            <div className="flex items-start gap-2.5">
              <span className="mt-0.5 inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-rose-100 px-1 text-[10px] font-bold text-rose-700">
                {index + 1}
              </span>
              <div className="min-w-0">
                <div className="text-[12.5px] font-semibold text-slate-900">{blocker.title}</div>
                <p className="mt-0.5 text-[11.5px] leading-relaxed text-slate-600">{blocker.detail}</p>
                <button
                  type="button"
                  className="mt-2 inline-flex items-center gap-1 text-[11.5px] font-medium text-rose-700 hover:text-rose-900"
                  onClick={() => navigateToBlocker(blocker.href, blocker.title)}
                >
                  {blocker.action} <ArrowRight className="h-3 w-3" />
                </button>
              </div>
            </div>
          </li>
        ))}
      </ol>
      <span className="sr-only" role="status" aria-live="polite">{navigationStatus}</span>
    </section>
  );
}

function StrategyRow({ label, value }: { label: string; value?: string | null }) {
  if (!value) return null;
  return (
    <div>
      <span className="text-[10px] uppercase tracking-[0.12em] text-slate-400">{label}</span>
      <div className="text-slate-800 text-[12.5px] leading-snug">{value}</div>
    </div>
  );
}

function WorkingLaneList({
  items,
  empty,
  showContactGap,
  onPlan,
}: {
  items: ConfengeWorkingQueueItem[];
  empty: string;
  showContactGap?: boolean;
  onPlan?: (accountId: string) => void;
}) {
  if (!items.length) {
    return <div className="px-3 py-6 text-center text-slate-400 text-[12.5px]">{empty}</div>;
  }
  return (
    <ul className="divide-y divide-slate-100 max-h-64 overflow-auto">
      {items.map((it) => {
        const a = it.account;
        const name = a.nome_fantasia || a.razao_social || a.cnpj14;
        const reasons = (it.reason_codes ?? a.activation_reason_codes ?? []).map(reasonLabel).join(", ");
        const score = it.activation_score ?? a.activation_score;
        const stale = it.context_stale || a.context_stale;
        const nextDate = formatPtBrDate(it.next_best_action_at);
        const expirationDate = formatPtBrDate(it.activation_expires_at);
        return (
          <li key={a.id} className="px-3 py-2.5 text-[12.5px] flex items-start gap-2">
            <div className="min-w-0 flex-1">
              <div className="font-medium text-slate-900 truncate">{name}</div>
              <div className="text-slate-500 truncate">
                {a.service_name || a.service_code || "—"}
                {reasons ? ` · ${reasons}` : ""}
                {typeof score === "number" ? ` · prioridade ${score.toFixed(1)}` : ""}
              </div>
              <div className="text-slate-400 truncate">
                {it.why_now || a.moment_summary || a.fact_to_mention || "—"}
                {nextDate ? ` · próxima ação em ${nextDate}` : ""}
                {expirationDate ? ` · prioridade válida até ${expirationDate}` : ""}
              </div>
              {showContactGap && (
                <div className="text-amber-700 text-[11px] mt-0.5">
                  Pendência: {reasons || "nenhum destinatário elegível foi resolvido"}. Ação: valide fonte, data e política do contato no feed.
                </div>
              )}
              {stale && (
                <div className="text-rose-700 text-[11px] mt-0.5" data-testid="confenge-stale-banner">
                  Os dados desta conta mudaram desde a aprovação. Revise a nova versão antes do envio.
                </div>
              )}
            </div>
            {onPlan && (
              <button
                type="button"
                className="shrink-0 h-7 px-2 rounded-md border border-slate-200 text-[12.5px] hover:bg-slate-50"
                onClick={() => onPlan(a.id)}
              >
                Planejar
              </button>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function ReadinessItem({ label, value }: { label: string; value: string }) {
  return (
    <span className="rounded-md border border-slate-200 bg-slate-50 px-2 py-1">
      <strong className="text-slate-700">{label}:</strong> {value}
    </span>
  );
}
