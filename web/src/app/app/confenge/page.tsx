import { useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";
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
import { useConfirm } from "@/hooks/context/confirm";
import {
	useApproveConfengeTouchpoint,
  useConfengeAccount,
  useConfengeAccountTimeline,
  useConfengeAccounts,
  useConfengeAttention,
  useConfengeAttentionDetail,
  useConfengeDispatchStatus,
  useConfengeReviewTouchpoints,
  useConfengeStatus,
  useConfengeSummary,
  useApplyConfengeManualAction,
  useConfengeCockpit,
  useConfengeExecutiveIntel,
  useConfengeTruthScoreboard,
  useConfengeHumanEnvelopes,
  useRecordConfengeHumanOutcome,
  useConfengeIntelException,
  useConfengeIntelExceptions,
  useResolveConfengeIntelException,
  useRecordConfengeActionOutcome,
  useRecordConfengeInboundOutcome,
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
  ConfengeActionCard,
  ConfengeActionCopy,
  ConfengeInboundNowItem,
  ConfengeExecutiveView,
  ConfengeScoreboard,
  ConfengeHumanEnvelope,
  ConfengeHumanOutcomeEntry,
  ConfengeIntelException,
  ConfengeIntelExceptionFilter,
  ConfengeAttentionFilter,
  ConfengeTouchpoint,
  ConfengeWorkingQueueItem,
} from "@/lib/api/models/app/confenge/Confenge";
import type { AppError } from "@/lib/api/client/normalizeError";
import { channelLabel, formatFeedAge, formatPtBrDate, intentLabel, purposeLabel, reasonLabel, stateLabel } from "./labels";
import { buildPilotGate, type PilotGate } from "./pilotGate";
import { isAuthorizeReady } from "./reviewReady";

const ATTENTION_FILTERS: { id: ConfengeAttentionFilter; label: string }[] = [
  { id: "needs_attention", label: "Precisa de atenção" },
  { id: "awaiting_approval", label: "Aguardando aprovação" },
];

export default function ConfengePage() {
	const confirm = useConfirm();
  const status = useConfengeStatus();
  const enabled = !!status.data?.enabled;
  const summary = useConfengeSummary(enabled);
  const workingOverview = useConfengeWorkingOverview(enabled);
  const cockpit = useConfengeCockpit(enabled);
  const executive = useConfengeExecutiveIntel(enabled);
  const scoreboard = useConfengeTruthScoreboard(enabled);
  const envelopes = useConfengeHumanEnvelopes(enabled);
  const recordHuman = useRecordConfengeHumanOutcome();
  const manualAction = useApplyConfengeManualAction();
  const recordOutcome = useRecordConfengeActionOutcome();
  const recordInbound = useRecordConfengeInboundOutcome();
  const agoraQueue = useConfengeWorkingQueue("agora", enabled);
  const needsContactQueue = useConfengeWorkingQueue("needs_contact", enabled);
  const ready = useConfengeAccounts("READY_TO_GENERATE", enabled);
  const review = useConfengeReviewTouchpoints(enabled);
  const plan = usePlanConfengeCadence();
  const prepareCohort = usePrepareConfengePilotCohort();
  const syncFeed = useSyncConfengeFeed();
  const genTouch = useGenerateConfengeTouchpoint();
  const approve = useApproveConfengeTouchpoint();
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
  const [reviewLane, setReviewLane] = useState<"ready" | "exception">("ready");
  const allReview = review.data ?? [];
  const autorizaveis = allReview.filter(isAuthorizeReady);
  const queue = allReview.filter((tp) => {
    const ready = isAuthorizeReady(tp);
    return reviewLane === "ready" ? ready : !ready;
  });
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
                onClick={() =>
				  confirm.show(
					"Retomar o dispatch CONFENGE agora? Mensagens já enfileiradas poderão chegar ao transporte.",
					async () => { await resumeDispatch.mutateAsync(); },
				  )
				}
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
            <span><strong className="text-slate-800">3. Aprove</strong> o conteúdo exato e mantenha o dispatch pausado até o GO/NO-GO.</span>
          </div>
        </section>

        {cockpit.data && (
          <section id="inbound-agora" data-testid="confenge-inbound-now" className="rounded-md border border-sky-200 bg-white">
            <div className="px-3 h-10 flex items-center border-b border-slate-200">
              <span className="text-[12.5px] font-medium text-slate-900">INBOUND NOW</span>
              <span className="ml-2 text-[12.5px] text-slate-500 tabular-nums">{cockpit.data?.inbound_now?.length ?? 0}</span>
            </div>
            <p className="px-3 pt-2 text-[11.5px] text-slate-500">
              Leads inbound do web-cfg. Sem copiar dados. Sem auto-envio.
            </p>
            <ul className="divide-y divide-slate-100">
              {(cockpit.data?.inbound_now ?? []).map((item) => (
                <li key={item.lead_id}>
                  <InboundNowCard
                    item={item}
                    submitting={recordInbound.isPending}
                    onOutcome={(payload) => recordInbound.mutate({ leadId: item.lead_id, ...payload })}
                  />
                </li>
              ))}
              {!(cockpit.data?.inbound_now?.length) && (
                <li className="px-3 py-8 text-center text-slate-400 text-[12.5px]">Nenhum lead inbound real na fila. Synthetic/qa/internal ficam de fora.</li>
              )}
            </ul>
          </section>
        )}

        {scoreboard.data && (
          <TruthScoreboardPanel view={scoreboard.data} />
        )}

        {executive.data && (
          <ExecutiveIntelPanel view={executive.data} />
        )}

        <HumanOutcomePanel
          envelopes={envelopes.data ?? []}
          submitting={recordHuman.isPending}
          onSubmit={(payload) => recordHuman.mutate(payload)}
        />

        <ExceptionQueuePanel enabled={enabled} />

        {cockpit.data?.today && (
          <section id="hoje" data-testid="confenge-today" className="rounded-md border border-slate-200 bg-white">
            <div className="px-3 h-10 flex items-center border-b border-slate-200">
              <span className="text-[12.5px] font-medium text-slate-900">O que fazer agora</span>
              <span className="ml-2 text-[12.5px] text-slate-500 tabular-nums">{cockpit.data.today.summary.total}</span>
            </div>
            <p className="px-3 pt-2 text-[11.5px] text-slate-500">
              Trabalho comercial humano executavel agora. Ligacao, WhatsApp, perfil e formulario nao sao enviados pelo sistema.
            </p>
            <div className="px-3 py-2 flex flex-wrap gap-2 text-[11.5px]" data-testid="confenge-today-summary">
              {[
                ["Ligacoes", cockpit.data.today.summary.calls],
                ["Ligacoes roteadas", cockpit.data.today.summary.routed_calls],
                ["E-mails para revisar", cockpit.data.today.summary.emails_to_review],
                ["E-mails inferidos", cockpit.data.today.summary.inferred_emails],
                ["Caixas funcionais", cockpit.data.today.summary.role_emails],
                ["WhatsApp", cockpit.data.today.summary.whatsapp],
                ["Perfil profissional", cockpit.data.today.summary.professional_social],
                ["Formularios", cockpit.data.today.summary.contact_forms],
              ].map(([label, n]) => (
                <span key={String(label)} className="rounded border border-slate-200 px-2 py-1">
                  <strong className="text-slate-800 tabular-nums">{n}</strong> {label}
                </span>
              ))}
            </div>
            <ul className="divide-y divide-slate-100">
              {(cockpit.data.today.actions ?? []).map((card) => (
                <li key={card.action_id}>
                  <TodayActionCard card={card} submitting={recordOutcome.isPending} onOutcome={(payload) => recordOutcome.mutate({ actionId: card.action_id, ...payload })} />
                </li>
              ))}
              {!(cockpit.data.today.actions?.length) && (
                <li className="px-3 py-8 text-center text-slate-400 text-[12.5px]">Nenhuma acao humana executavel agora.</li>
              )}
            </ul>
          </section>
        )}

        {cockpit.data?.funnel && (
          <section id="funil" data-testid="confenge-funnel" className="rounded-md border border-slate-200 bg-white px-3 py-3">
            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Funil operacional</div>
            <div className="mt-2 flex flex-wrap gap-2 text-[11.5px]">
              {[
                ["Importadas", cockpit.data.funnel.imported],
                ["TIER A", cockpit.data.funnel.tier_a],
                ["TIER B", cockpit.data.funnel.tier_b],
                ["TIER C", cockpit.data.funnel.tier_c],
                ["TIER D", cockpit.data.funnel.tier_d],
                ["Bloqueadas", cockpit.data.funnel.blocked_exhausted],
                ["Prontas para revisar", cockpit.data.funnel.needs_review],
                ["Fila manual", cockpit.data.funnel.manual_outreach_ready],
                ["Aprovadas", cockpit.data.funnel.approved],
                ["Acionaveis", cockpit.data.funnel.actionable ?? 0],
                ["Planejadas", cockpit.data.funnel.action_planned ?? 0],
                ["Contatadas", cockpit.data.funnel.contacted],
                ["Alvo alcancado", cockpit.data.funnel.target_reached ?? 0],
                ["Conversas", cockpit.data.funnel.conversation ?? 0],
                ["Respostas", cockpit.data.funnel.replied],
                ["Reuniões", cockpit.data.funnel.meeting],
              ].map(([label, n]) => (
                <ReadinessItem key={String(label)} label={String(label)} value={String(n)} />
              ))}
            </div>
          </section>
        )}

        <section id="fila-manual" data-testid="confenge-manual-queue" className="rounded-md border border-slate-200 bg-white">
          <div className="px-3 h-10 flex items-center border-b border-slate-200">
            <span className="text-[12.5px] font-medium">Fila manual honesta</span>
            <span className="ml-2 text-[12.5px] text-slate-500 tabular-nums">{cockpit.data?.manual?.length ?? 0}</span>
          </div>
          <p className="px-3 pt-2 text-[11.5px] text-slate-500">TIER B, C e D nao entram em e-mail nominal. Aprovar nao aparece aqui.</p>
          <ul className="divide-y divide-slate-100 max-h-80 overflow-auto">
            {(cockpit.data?.manual ?? []).map((item) => (
              <li key={`${item.canonical_target_id}-${item.lane}`} className="px-3 py-2.5 text-[12.5px] space-y-1">
                <div className="font-medium text-slate-900">{item.company} <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">{stateLabel(item.contact_tier)}</span></div>
                <div data-testid="confenge-manual-route-class" className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                  {[item.channel, item.mailbox_label || item.channel_value].filter(Boolean).join(" · ") || stateLabel(item.lane)}
                </div>
                <div className="text-slate-600">{[item.person, item.role, item.channel, item.service].filter(Boolean).join(" · ") || "Sem pessoa nomeada"}</div>
                {item.why_now && <div data-testid="confenge-manual-why-now" className="text-slate-500">Por que agora: {item.why_now}</div>}
                {item.factual_hook && <div data-testid="confenge-manual-hook" className="text-slate-500">Gancho: {item.factual_hook}</div>}
                {item.recommended_action && <div data-testid="confenge-manual-next" className="text-slate-500">Próxima ação: {item.recommended_action}</div>}
                {item.confidence && <div data-testid="confenge-manual-confidence" className="text-[11px] text-slate-400">Confiança: {item.confidence}</div>}
                {item.source && <div className="text-[11px] text-slate-400 break-all">Fonte: {item.source}</div>}
                {item.suggested_text && <div className="rounded border border-slate-100 bg-slate-50 px-2 py-1 text-[11.5px] whitespace-pre-wrap">{item.suggested_text}</div>}
                {item.blocking_warning && <div className="text-amber-800">{item.blocking_warning}</div>}
                <div className="flex flex-wrap gap-1.5 pt-1">
                  {item.source && <a href={item.source} target="_blank" rel="noreferrer" className="h-7 px-2.5 inline-flex items-center rounded-md border border-slate-200 text-[12.5px]">Abrir fonte</a>}
                  {item.suggested_text && (
                    <button
                      type="button"
                      data-testid="confenge-manual-copy-text"
                      className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px]"
                      onClick={() => { void navigator.clipboard.writeText(item.suggested_text || ""); }}
                    >
                      Revisar / copiar
                    </button>
                  )}
                  {(item.actions || []).filter((a) => a !== "OPEN_SOURCE" && a !== "APPROVE").map((action) => (
                    action === "COPY_TEXT" ? null : (
                    <button key={action} type="button" className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px]" disabled={manualAction.isPending || !item.canonical_target_id} onClick={() => item.canonical_target_id && manualAction.mutate({ accountId: item.canonical_target_id, action })}>
                      {stateLabel(action)}
                    </button>
                    )
                  ))}
                </div>
              </li>
            ))}
            {!(cockpit.data?.manual?.length) && <li className="px-3 py-8 text-center text-slate-400 text-[12.5px]">Nenhuma conta manual no recorte atual.</li>}
          </ul>
        </section>

        {status.data?.readiness && (
          <section className="rounded-md border border-slate-200 bg-white px-3 py-3">
            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Condições da operação</div>
            <p className="mt-1 text-[11.5px] text-slate-500">Mostra se dados, geração e envio estão prontos para o trabalho diário.</p>
            <div className="mt-2 flex flex-wrap gap-2 text-[11.5px]">
              <ReadinessItem label="E-mail" value={stateLabel(status.data.readiness.email)} />
              <ReadinessItem label="Feed de dados" value={status.data.readiness.feed_configured ? `Atualizado ${formatFeedAge(status.data.readiness.feed_age_seconds)}` : "Não configurado"} />
              <ReadinessItem label="Inbound" value={stateLabel(status.data.readiness.inbound || "not_configured")} />
              <ReadinessItem label="Auto-envio" value={status.data.readiness.auto_send_enabled ? "Ligado (proibido)" : "Desligado"} />
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
                {(cohortResult?.cohort_prepared ?? pilotGate.prepared)}/{pilotGate.target} no cohort · {autorizaveis.length} autorizáveis
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
            <div className="flex items-center gap-1">
              <button
                type="button"
                className={`relative h-10 px-2.5 inline-flex items-center text-[12.5px] ${reviewLane === "ready" ? "text-slate-900 font-medium" : "text-slate-500"}`}
                onClick={() => { setReviewLane("ready"); setIdx(0); }}
              >
                Prontas para autorizar ({autorizaveis.length})
                {reviewLane === "ready" && <span className="absolute inset-x-2 -bottom-px h-0.5 bg-sky-600" />}
              </button>
              <button
                type="button"
                className={`relative h-10 px-2.5 inline-flex items-center text-[12.5px] ${reviewLane === "exception" ? "text-slate-900 font-medium" : "text-slate-500"}`}
                onClick={() => { setReviewLane("exception"); setIdx(0); }}
              >
                Exceções
                {reviewLane === "exception" && <span className="absolute inset-x-2 -bottom-px h-0.5 bg-sky-600" />}
              </button>
              {current && (
                <span className="text-[10px] uppercase tracking-[0.14em] px-1.5 py-0.5 rounded bg-sky-50 text-sky-700">
                  Etapa {current.ordinal} · {channelLabel(current.channel)} · {stateLabel(current.state)}
                </span>
              )}
            </div>
          </div>
          <p className="px-3 pt-2 text-[11.5px] text-slate-500">Confira destinatário, contexto e conteúdo exato. Aprovar não coloca a mensagem na fila nem libera o envio.</p>
          {!current ? (
            <div className="px-3 py-10 text-center text-slate-400 text-[12.5px]">
              {reviewLane === "ready"
                ? "Nenhuma mensagem pronta para autorização. Destinatários sem identidade comprovada ficam em Exceções."
                : "Nenhuma exceção aberta nesta fila."}
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
				  {(current.recipient_generic || (current.recipient_state && current.recipient_state.toUpperCase() !== "VALIDATED")) && (
					<div className="mt-1 rounded-md border border-amber-200 bg-amber-50 px-2 py-1.5 text-[11px] text-amber-900">
						{current.recipient_state && current.recipient_state.toUpperCase() !== "VALIDATED"
							? `Destinatário ${current.recipient_state}${current.recipient_reason ? `: ${current.recipient_reason}` : ""}. Aprovar fica bloqueado até extra-cli publicar uma pessoa comprovada.`
							: `Caixa genérica (${current.recipient_mailbox_purpose || "finalidade desconhecida"}). Não presuma nome, cargo ou uma pessoa específica.`}
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
                  <StrategyRow
                    label="Messageability"
                    value={
                      current.strategy_explain?.messageability
                        ? `${stateLabel(current.strategy_explain.messageability)}${
                            current.strategy_explain.messageability_reason
                              ? `: ${current.strategy_explain.messageability_reason}`
                              : ""
                          }`
                        : undefined
                    }
                  />
                  <StrategyRow label="Por que esta conta" value={current.strategy_explain?.why_this_account} />
                  <StrategyRow label="Por que agora" value={current.strategy_explain?.why_now || current.account?.moment_summary} />
                  <StrategyRow label="Fato usado" value={current.strategy_explain?.fact_used || current.fact_used} />
                  <StrategyRow label="Raciocínio interno (não é copy)" value={current.strategy_explain?.hypothesis} />
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
                  {current.consultant_sendability && (
                    <div className="mt-2 rounded-md border border-slate-200 bg-white p-2 space-y-1" data-testid="confenge-sendability-pack">
                      <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Envio sem editar?</div>
                      <div className="font-medium text-slate-900">
                        {current.consultant_sendability.send_without_editing === "sim" ? "Sim, só autorizar" : "Não"}
                      </div>
                      <StrategyRow label="Pessoa" value={current.consultant_sendability.person} />
                      <StrategyRow label="Por que esta pessoa" value={current.consultant_sendability.why_this_person} />
                      <StrategyRow label="E-mail" value={current.consultant_sendability.email} />
                      <StrategyRow label="Derivação" value={current.consultant_sendability.derivation} />
                      <StrategyRow label="Verificação" value={current.consultant_sendability.verification_status} />
                      <StrategyRow label="Frescor" value={current.consultant_sendability.freshness} />
                      <StrategyRow label="Supressão" value={current.consultant_sendability.suppression} />
                      <StrategyRow label="Serviço" value={current.consultant_sendability.service_code} />
                      <StrategyRow label="Fato" value={current.consultant_sendability.supporting_fact} />
                      {current.consultant_sendability.warnings && current.consultant_sendability.warnings.length > 0 && (
                        <div className="text-[11px] text-amber-800">{current.consultant_sendability.warnings.join(" · ")}</div>
                      )}
                    </div>
                  )}
                  {current.generation_error && (
                    <div className="mt-2 rounded-md border border-rose-200 bg-rose-50 px-2 py-1.5 text-[11.5px] text-rose-800" data-testid="confenge-generate-error">
                      {current.generation_error}
                    </div>
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
                  {reviewLane === "ready" && (
                  <button
                    type="button"
                    data-testid="confenge-approve"
                    className="h-7 px-2.5 rounded-md bg-sky-600 text-white text-[12.5px] disabled:opacity-50"
                    disabled={approve.isPending || !body.trim() || !recipient.trim() || !isAuthorizeReady(current)}
                    onClick={() =>
                      approve.mutate({
                        id: current.id,
                        subject,
                        body_text: body,
                        recipient,
                      })
                    }
                  >
                    <Check className="inline h-3.5 w-3.5 mr-1" />
                    Aprovar mensagem
                  </button>
                  )}
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
					onClick={() =>
					  confirm.show(
						"Marcar esta conta como não contatar? Isso cancela todo trabalho de outreach ainda aberto.",
						async () => { await dnc.mutateAsync(current.account_id); },
					  )
					}
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

const INBOUND_OUTCOME_OPTIONS = [
  "CONTACTED",
  "QUALIFIED_CONVERSATION",
  "FOLLOW_UP",
  "MEETING_SCHEDULED",
  "PROPOSAL",
  "NO_RESPONSE",
  "DNC",
  "LOST",
  "WON",
  "CLIENT",
];

function InboundNowCard({
  item,
  submitting,
  onOutcome,
}: {
  item: ConfengeInboundNowItem;
  submitting: boolean;
  onOutcome: (payload: { outcome_code: string; notes?: string }) => void;
}) {
  const [outcome, setOutcome] = useState("");
  return (
    <article data-testid="confenge-inbound-card" className="px-3 py-3 text-[12.5px] space-y-1.5">
      <div className="flex flex-wrap items-baseline gap-2">
        <div className="font-medium text-slate-900">{item.company}</div>
        {item.person ? <span className="text-slate-700">{item.person}</span> : <span className="text-slate-400">Pessoa UNKNOWN</span>}
        <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">{stateLabel(item.next_action)}</span>
      </div>
      <div data-testid="confenge-inbound-lead-id">lead_id: {item.lead_id}</div>
      <div data-testid="confenge-inbound-receipt">receipt_id: {item.receipt_id || item.lead_id}</div>
      <div data-testid="confenge-inbound-enrichment">Enrichment: {item.enrichment_status || "UNKNOWN"}</div>
      <div data-testid="confenge-inbound-latency">
        Capturado: {item.latency?.lead_created_at || "UNKNOWN"} · Ingerido: {item.latency?.warmbly_ingested_at || "UNKNOWN"}
        {item.latency?.enrichment_completed_at ? ` · Enriquecido: ${item.latency.enrichment_completed_at}` : ""}
      </div>
      {item.contract_context && <div>Contrato/contexto: {item.contract_context}</div>}
      <div>Origem: {item.origin || "UNKNOWN"}{item.asset ? ` · Asset: ${item.asset}` : " · Asset: UNKNOWN"}</div>
      <div data-testid="confenge-inbound-query">Query: {item.query || "UNKNOWN"} · CTA: {item.cta || "UNKNOWN"}</div>
      <div data-testid="confenge-inbound-trigger">Trigger: {item.trigger || "UNKNOWN"} · Oferta: {item.offer || "UNKNOWN"}</div>
      <div data-testid="confenge-inbound-ids">
        Conta: {item.account_id || item.entity_id || "UNKNOWN"}
        {item.person_id ? ` · person_id: ${item.person_id}` : " · person_id: UNKNOWN"}
      </div>
      <div data-testid="confenge-inbound-why">Por que agora: {item.why_now}</div>
      <div data-testid="confenge-inbound-next">Ação recomendada: {item.recommended_action}</div>
      <div>Canal: {item.channel || "UNKNOWN"} · Alcance: {item.reachability || "UNKNOWN"}</div>
      <div data-testid="confenge-inbound-freshness">Freshness: {item.freshness || "UNKNOWN"} · Confiança: {item.confidence || "UNKNOWN"}</div>
      {item.suggested_copy && (
        <div data-testid="confenge-inbound-suggested" className="rounded-md border border-slate-200 bg-slate-50 px-2 py-1.5 text-[12px] text-slate-700">
          <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
            Sugestão {item.suggested_copy_route || "UNKNOWN"} · revisão humana
          </div>
          <div className="mt-0.5 whitespace-pre-wrap">{item.suggested_copy}</div>
        </div>
      )}
      {item.evidence && item.evidence.length > 0 && <div className="text-[11px] text-slate-400">Evidências: {item.evidence.join(", ")}</div>}
      <div>Owner: {item.owner} · Idade: {item.lead_age} · Status: {stateLabel(item.status)}</div>
      <div>Próxima ação: {stateLabel(item.next_action)}</div>
      {item.warnings?.map((w) => (
        <div key={w} className="text-amber-800">{w}</div>
      ))}
      <div className="flex flex-wrap items-end gap-1.5 pt-1">
        <label className="text-[11px] text-slate-500">
          Outcome
          <select
            data-testid="confenge-inbound-outcome"
            className="mt-0.5 h-7 block min-w-[180px] rounded-md border border-slate-200 bg-white px-2 text-[12.5px]"
            value={outcome}
            onChange={(e) => setOutcome(e.target.value)}
          >
            <option value="">Registrar resultado</option>
            {INBOUND_OUTCOME_OPTIONS.map((code) => (
              <option key={code} value={code}>{stateLabel(code)}</option>
            ))}
          </select>
        </label>
        <button
          type="button"
          data-testid="confenge-inbound-record-outcome"
          disabled={!outcome || submitting}
          className="h-7 px-2.5 rounded-md bg-sky-600 text-white text-[12.5px] disabled:opacity-50"
          onClick={() => onOutcome({ outcome_code: outcome })}
        >
          Registrar
        </button>
      </div>
    </article>
  );
}

const OUTCOME_OPTIONS = [
  "ATTEMPTED",
  "NO_ANSWER",
  "BUSY",
  "CONTACTED",
  "GATEKEEPER_REACHED",
  "REFERRED_TO_OTHER_PERSON",
  "TARGET_REACHED",
  "CALLBACK_REQUESTED",
  "FOLLOW_UP",
  "REPLIED",
  "INTERESTED",
  "NOT_INTERESTED",
  "MEETING_SCHEDULED",
  "WRONG_PERSON",
  "INVALID_ROUTE",
  "DNC",
  "SKIPPED",
];

const EXCEPTION_TYPES = ["", "orphan", "duplicate", "conflicting_account", "missing_version", "stale_attribution", "out_of_order", "unconfirmed_won", "unconfirmed_lost", "ledger_unavailable"];
const EXCEPTION_LANES = ["", "inbound", "outbound", "partner", "expansion", "UNKNOWN"];
const EXCEPTION_SOURCES = ["", "web-cfg", "extra-cli", "partner", "expansion", "UNKNOWN"];
const EXCEPTION_SEVERITIES = ["", "high", "medium", "low"];
const EXCEPTION_AGES: { id: number; label: string }[] = [
  { id: 0, label: "qualquer idade" },
  { id: 3600, label: ">1h" },
  { id: 86400, label: ">24h" },
  { id: 604800, label: ">7d" },
];

function ExceptionQueuePanel({ enabled }: { enabled: boolean }) {
  const confirm = useConfirm();
  const [filter, setFilter] = useState<ConfengeIntelExceptionFilter>({});
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [linkIdentity, setLinkIdentity] = useState("");
  const list = useConfengeIntelExceptions(filter, enabled);
  const detail = useConfengeIntelException(selectedId);
  const resolve = useResolveConfengeIntelException();
  const items = list.data ?? [];
  const current: ConfengeIntelException | undefined = detail.data ?? items.find((x) => x.id === selectedId);

  const act = (action: "link" | "defer" | "reject" | "mark_external_evidence_required") => {
    if (!current) return;
    const why = reason.trim();
    if (!why) {
      toast.error("Informe o motivo. A fila não inventa outcome.");
      return;
    }
    if (action === "link" && !linkIdentity.trim()) {
      toast.error("Informe um identity já existente. A fila não inventa identidade.");
      return;
    }
    const label = action === "link" ? "vincular ao identity existente" : action === "defer" ? "adiar" : action === "reject" ? "rejeitar" : "marcar evidência externa";
    void confirm.show(`Confirmar ${label}? Isso não cria WON, LOST, receita ou identidade.`, async () => {
      await resolve.mutateAsync({
        id: current.id,
        action,
        reason: why,
        link_identity: action === "link" ? linkIdentity.trim() : undefined,
      });
    });
  };

  return (
    <section id="fila-excecoes" data-testid="confenge-intel-exceptions" className="rounded-md border border-slate-200 bg-white">
      <div className="px-3 h-10 flex items-center border-b border-slate-200">
        <span className="text-[12.5px] font-medium text-slate-900">Fila de exceções</span>
        <span className="ml-2 text-[12.5px] text-slate-500 tabular-nums">{items.length}</span>
      </div>
      <p className="px-3 pt-2 text-[11.5px] text-slate-500">
        Órfãos, conflitos e transições impossíveis. Cada item mostra evidência e a próxima ação, ou permanece aberto.
        Sem botão para inventar WON, LOST, receita ou identidade.
      </p>
      <div className="px-3 py-2 flex flex-wrap gap-1.5" data-testid="confenge-intel-exception-filters">
        <FilterChips label="tipo" value={filter.type ?? ""} options={EXCEPTION_TYPES} onChange={(type) => setFilter((f) => ({ ...f, type: type || undefined }))} />
        <FilterChips label="lane" value={filter.lane ?? ""} options={EXCEPTION_LANES} onChange={(lane) => setFilter((f) => ({ ...f, lane: lane || undefined }))} />
        <FilterChips label="fonte" value={filter.source ?? ""} options={EXCEPTION_SOURCES} onChange={(source) => setFilter((f) => ({ ...f, source: source || undefined }))} />
        <FilterChips label="severidade" value={filter.severity ?? ""} options={EXCEPTION_SEVERITIES} onChange={(severity) => setFilter((f) => ({ ...f, severity: severity || undefined }))} />
        <div className="flex flex-wrap items-center gap-1">
          <span className="text-[10px] uppercase tracking-[0.14em] text-slate-400">idade</span>
          {EXCEPTION_AGES.map((opt) => (
            <button
              key={opt.id}
              type="button"
              className={`h-7 px-2 rounded-md border text-[11.5px] ${ (filter.ageMinSeconds ?? 0) === opt.id ? "border-sky-400 bg-sky-50 text-sky-700" : "border-slate-200 text-slate-600"}`}
              onClick={() => setFilter((f) => ({ ...f, ageMinSeconds: opt.id || undefined }))}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>
      <ul className="divide-y divide-slate-100 max-h-72 overflow-auto" data-testid="confenge-intel-exception-list">
        {items.map((ex) => (
          <li key={ex.id}>
            <button
              type="button"
              className={`w-full text-left px-3 py-2 text-[12.5px] ${selectedId === ex.id ? "bg-sky-50" : ""}`}
              onClick={() => setSelectedId(ex.id)}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium text-slate-900">{ex.code}</span>
                <span className="text-[11px] text-slate-500">{ex.severity} · {ex.lane} · {ex.status || "open"}</span>
              </div>
              <div className="text-slate-500 truncate">{ex.next_action || "permanece aberta"}</div>
              <div className="text-[11px] text-slate-400">{ex.source} · {formatAge(ex.age_seconds)}</div>
            </button>
          </li>
        ))}
        {!items.length && (
          <li className="px-3 py-8 text-center text-slate-400 text-[12.5px]">
            Nenhuma exceção persistida. Sem evento real a fila fica vazia e UNKNOWN permanece UNKNOWN.
          </li>
        )}
      </ul>
      {current && (
        <div className="border-t border-slate-200 px-3 py-3 space-y-2" data-testid="confenge-intel-exception-detail">
          <div className="text-[12.5px] font-medium text-slate-900">{current.code} · {current.status || "open"}</div>
          <p className="text-[12px] text-slate-600">{current.reason}</p>
          <p className="text-[12px] text-slate-700">Próxima ação: {current.next_action || "permanece aberta"}</p>
          <div>
            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-400">Evidência</div>
            <ul className="mt-1 space-y-0.5 text-[11.5px] text-slate-600">
              {(current.evidence ?? []).map((ev) => (
                <li key={`${ev.kind}-${ev.key}`}>{ev.kind}: {ev.key}={ev.value}</li>
              ))}
            </ul>
          </div>
          <div>
            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-400">Histórico</div>
            <ul className="mt-1 space-y-0.5 text-[11.5px] text-slate-600">
              {(current.history ?? []).map((ev, i) => (
                <li key={`${ev.at}-${i}`}>{ev.kind}{ev.action ? ` · ${ev.action}` : ""}{ev.actor ? ` · ${ev.actor}` : ""}{ev.reason ? ` · ${ev.reason}` : ""}</li>
              ))}
            </ul>
          </div>
          <TextInput value={reason} onChange={setReason} placeholder="Motivo obrigatório (ator vem da sessão)" />
          {(current.allowed_actions ?? []).includes("link") && (
            <TextInput value={linkIdentity} onChange={setLinkIdentity} placeholder="Identity existente para vincular" />
          )}
          <div className="flex flex-wrap gap-1.5" data-testid="confenge-intel-exception-actions">
            {(current.allowed_actions ?? []).includes("link") && (
              <button type="button" className="h-7 px-2.5 rounded-md border border-slate-200 text-[12px] text-slate-700" onClick={() => act("link")}>Vincular</button>
            )}
            {(current.allowed_actions ?? []).includes("defer") && (
              <button type="button" className="h-7 px-2.5 rounded-md border border-slate-200 text-[12px] text-slate-700" onClick={() => act("defer")}>Adiar</button>
            )}
            {(current.allowed_actions ?? []).includes("reject") && (
              <button type="button" className="h-7 px-2.5 rounded-md border border-slate-200 text-[12px] text-slate-700" onClick={() => act("reject")}>Rejeitar</button>
            )}
            {(current.allowed_actions ?? []).includes("mark_external_evidence_required") && (
              <button type="button" className="h-7 px-2.5 rounded-md border border-slate-200 text-[12px] text-slate-700" onClick={() => act("mark_external_evidence_required")}>Evidência externa</button>
            )}
          </div>
        </div>
      )}
    </section>
  );
}

function formatAge(seconds: number): string {
  if (!seconds || seconds < 60) return `${seconds || 0}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

function FilterChips({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: string[];
  onChange: (next: string) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-1">
      <span className="text-[10px] uppercase tracking-[0.14em] text-slate-400">{label}</span>
      {options.map((opt) => (
        <button
          key={opt || "any"}
          type="button"
          className={`h-7 px-2 rounded-md border text-[11.5px] ${value === opt ? "border-sky-400 bg-sky-50 text-sky-700" : "border-slate-200 text-slate-600"}`}
          onClick={() => onChange(opt)}
        >
          {opt || "todas"}
        </button>
      ))}
    </div>
  );
}

function TruthScoreboardPanel({ view }: { view: ConfengeScoreboard }) {
  return (
    <section id="placar-inbound" data-testid="confenge-truth-scoreboard" className="rounded-md border border-slate-200 bg-white">
      <div className="px-3 h-10 flex items-center border-b border-slate-200">
        <span className="text-[12.5px] font-medium text-slate-900">Placar inbound</span>
        <span className="ml-2 text-[10px] uppercase tracking-[0.14em] text-slate-500">{view.production_path}</span>
      </div>
      <p className="px-3 pt-2 text-[11.5px] text-slate-500">
        Sete estágios. Synthetic excluído. causal_proof={String(view.causal_proof)}. auto_send={String(view.auto_send_enabled)}.
        {view.human_blocker ? ` Blocker: ${view.human_blocker}` : ""}
      </p>
      <div className="px-3 py-2 space-y-1.5">
        {(view.stages ?? []).map((stage) => (
          <div key={stage.id} data-testid={`scoreboard-stage-${stage.id}`} className="rounded-md border border-slate-200 px-2 py-1.5">
            <div className="flex flex-wrap items-baseline gap-2">
              <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">{stage.order}</span>
              <span className="text-[12.5px] font-medium text-slate-900">{stage.label}</span>
              <span className="text-[11px] tabular-nums text-slate-700">{stage.status}</span>
              {stage.numerator != null && (
                <span className="text-[11px] text-slate-500 tabular-nums">{stage.numerator}/{stage.denominator ?? "?"}</span>
              )}
            </div>
            <p className="mt-0.5 text-[11px] text-slate-500">{stage.observation}</p>
            <p className="text-[11px] text-slate-400">owner {stage.owner} · {stage.next_action}</p>
          </div>
        ))}
      </div>
      <div className="px-3 pb-3 flex flex-wrap gap-2 text-[11px] text-slate-500">
        {(view.separate_metrics ?? []).map((m) => (
          <span key={m.id} className="rounded-md border border-slate-200 px-2 py-1">
            {m.label}: {m.status} · {m.value_cents || m.count || 0}
          </span>
        ))}
      </div>
    </section>
  );
}

const HUMAN_ACTIONS = [
  { id: "attempted", label: "Tentativa" },
  { id: "reached", label: "Alcançou" },
  { id: "not_reached", label: "Não alcançou" },
  { id: "routed", label: "Roteado" },
  { id: "wrong_route", label: "Rota errada" },
  { id: "reply", label: "Resposta" },
  { id: "meeting_scheduled", label: "Reunião marcada" },
  { id: "meeting_held", label: "Reunião feita" },
  { id: "follow_up", label: "Follow-up" },
  { id: "disqualified", label: "Desqualificado" },
  { id: "proposal_emitted", label: "Proposta emitida" },
  { id: "won", label: "WON (com evidência)" },
  { id: "lost", label: "LOST (com evidência)" },
  { id: "revenue_received", label: "Receita (com documento)" },
];

function HumanOutcomePanel({
  envelopes,
  submitting,
  onSubmit,
}: {
  envelopes: ConfengeHumanEnvelope[];
  submitting: boolean;
  onSubmit: (payload: ConfengeHumanOutcomeEntry) => void;
}) {
  const [envelope, setEnvelope] = useState("EXTRA");
  const [leadId, setLeadId] = useState("");
  const [accountId, setAccountId] = useState("");
  const [action, setAction] = useState("attempted");
  const [followUp, setFollowUp] = useState("");
  const [evidence, setEvidence] = useState("");
  const [doc, setDoc] = useState("");
  const [cents, setCents] = useState("");
  const [notes, setNotes] = useState("");
  const selected = envelopes.find((e) => e.slot === envelope);
  const needsEvidence = action === "won" || action === "lost" || action === "revenue_received";
  return (
    <section id="registro-humano" data-testid="confenge-human-outcome" className="rounded-md border border-slate-200 bg-white">
      <div className="px-3 h-10 flex items-center border-b border-slate-200">
        <span className="text-[12.5px] font-medium text-slate-900">Registrar contato</span>
      </div>
      <p className="px-3 pt-2 text-[11.5px] text-slate-500">
        Menos passos que uma planilha. WON, LOST e receita exigem evidência. IDs em branco ficam em branco.
      </p>
      <form
        className="px-3 py-2 grid gap-2 sm:grid-cols-2 text-[12.5px]"
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit({
            envelope_id: envelope,
            idempotency_key: selected?.idempotency_key || `human:${leadId || envelope}:${action}`,
            lead_id: leadId || undefined,
            account_id: accountId || undefined,
            action,
            follow_up_at: followUp || undefined,
            evidence_ref: evidence || undefined,
            revenue_document_id: doc || undefined,
            revenue_cents: cents ? Number(cents) : undefined,
            human_confirmed: needsEvidence,
            notes: notes || undefined,
          });
        }}
      >
        <label className="grid gap-1">
          <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Envelope</span>
          <select className="h-7 rounded-md border border-slate-200 px-2" value={envelope} onChange={(e) => setEnvelope(e.target.value)}>
            {(envelopes.length ? envelopes : [{ slot: "EXTRA" }, { slot: "ACCOUNT_1" }, { slot: "ACCOUNT_2" }, { slot: "ACCOUNT_3" }]).map((env) => (
              <option key={env.slot} value={env.slot}>{env.slot}</option>
            ))}
          </select>
        </label>
        <label className="grid gap-1">
          <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Ação</span>
          <select className="h-7 rounded-md border border-slate-200 px-2" value={action} onChange={(e) => setAction(e.target.value)}>
            {HUMAN_ACTIONS.map((a) => (
              <option key={a.id} value={a.id}>{a.label}</option>
            ))}
          </select>
        </label>
        <label className="grid gap-1">
          <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">lead_id (depois)</span>
          <TextInput value={leadId} onChange={setLeadId} placeholder="vazio até existir" />
        </label>
        <label className="grid gap-1">
          <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">account_id (depois)</span>
          <TextInput value={accountId} onChange={setAccountId} placeholder="vazio até existir" />
        </label>
        <label className="grid gap-1">
          <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Follow-up (RFC3339)</span>
          <TextInput value={followUp} onChange={setFollowUp} />
        </label>
        <label className="grid gap-1">
          <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Evidência</span>
          <TextInput value={evidence} onChange={setEvidence} />
        </label>
        <label className="grid gap-1">
          <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Documento financeiro</span>
          <TextInput value={doc} onChange={setDoc} />
        </label>
        <label className="grid gap-1">
          <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Receita (centavos)</span>
          <TextInput value={cents} onChange={setCents} />
        </label>
        <label className="grid gap-1 sm:col-span-2">
          <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Notas</span>
          <TextInput value={notes} onChange={setNotes} />
        </label>
        <div className="sm:col-span-2">
          <button type="submit" disabled={submitting} className="h-7 px-3 rounded-md bg-sky-600 text-white text-[12.5px] disabled:opacity-50">
            {submitting ? "Gravando" : "Gravar registro"}
          </button>
        </div>
      </form>
    </section>
  );
}

function ExecutiveIntelPanel({ view }: { view: ConfengeExecutiveView }) {
  const unknownLabel = view.real_empty ? "UNKNOWN" : String(view.unknown);
  return (
    <section id="inteligencia-executiva" data-testid="confenge-executive-intel" className="rounded-md border border-slate-200 bg-white">
      <div className="px-3 h-10 flex items-center border-b border-slate-200">
        <span className="text-[12.5px] font-medium text-slate-900">Inteligência comercial</span>
        <span className="ml-2 text-[10px] uppercase tracking-[0.14em] text-slate-500">{view.month}</span>
      </div>
      <p className="px-3 pt-2 text-[11.5px] text-slate-500">
        Associação observada apenas. causal_proof={String(view.causal_proof)}. Families inbound/outbound/parceiro/expansão ficam separadas.
        {view.real_empty ? " Sem evento real: zeros e UNKNOWN." : ""}
      </p>
      <div className="px-3 py-2 grid gap-2 sm:grid-cols-4 text-[12px]">
        <Metric label="IQP/mês" value={view.inbound_qualified_pipeline} />
        <Metric label="Leads válidos" value={view.denominators.leads} />
        <Metric label="QCO" value={view.qco} />
        <Metric label="Conversas" value={view.conversations} />
        <Metric label="Reuniões" value={view.meetings} />
        <Metric label="Propostas" value={view.proposals} />
        <Metric label="Pipeline" value={view.pipeline} />
        <Metric label="WON" value={view.won} />
        <Metric label="LOST" value={view.lost} />
        <Metric label="UNKNOWN" value={unknownLabel} />
        <Metric label="Latência" value={view.latency.baseline || "insufficient_data"} />
        <Metric label="Freshness stale" value={view.freshness?.stale_chains ?? 0} />
      </div>
      <div className="px-3 pb-3 flex flex-wrap gap-2 text-[11px] text-slate-500">
        {(view.families ?? []).map((f) => (
          <span key={f.route_family} className="rounded-md border border-slate-200 px-2 py-1">
            {f.route_family}: QCO {f.qco} · WON {f.won} · LOST {f.lost} · UNKNOWN {f.unknown}
          </span>
        ))}
      </div>
    </section>
  );
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-md border border-slate-200 px-2 py-1.5">
      <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">{label}</div>
      <div className="text-slate-900 tabular-nums">{value}</div>
    </div>
  );
}

function founderFacingCopy(copy: ConfengeActionCopy): string {
  const body = (copy.body || "").trim();
  if (body) return body;
  return [copy.opening, copy.reason_for_call, copy.ask].filter((part) => !!part && part.trim()).join(" ").trim();
}

function TodayActionCard({
  card,
  submitting,
  onOutcome,
}: {
  card: ConfengeActionCard;
  submitting: boolean;
  onOutcome: (payload: { outcome_code: string; notes?: string; referral_name?: string; referral_role?: string; next_action_type?: string; next_action_at?: string }) => void;
}) {
  const [outcome, setOutcome] = useState("");
  const [referralName, setReferralName] = useState("");
  const [referralRole, setReferralRole] = useState("");
  const [nextActionAt, setNextActionAt] = useState("");
  const copy = card.copy || {};
  const phone = card.channel_value || (card.route_type === "phone" ? card.channel : "");
  return (
    <article data-testid="confenge-action-card" className="px-3 py-3 text-[12.5px] space-y-1.5">
      <div className="flex flex-wrap items-baseline gap-2">
        <div className="font-medium text-slate-900">{card.company}</div>
        <span data-testid="confenge-today-route-class" className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
          {stateLabel(card.action_type)}
          {card.reachability_class ? ` · ${stateLabel(card.reachability_class)}` : ""}
          {card.mailbox_label || copy.mailbox_label ? ` · ${card.mailbox_label || copy.mailbox_label}` : ""}
        </span>
      </div>
      <div className="text-slate-700">
        {card.person ? (
          <>
            Falar com <strong>{card.person}</strong>
            {card.role ? ` (${card.role})` : ""}
          </>
        ) : (
          <>Alvo por funcao: <strong>{card.target_role || card.role || "equipe"}</strong></>
        )}
      </div>
      {card.recommended_action && <div data-testid="confenge-today-next" className="text-slate-800">{card.recommended_action}</div>}
      {card.why_now && <div data-testid="confenge-today-why">Por que agora: {card.why_now}</div>}
      {card.offer && <div>Oferta: {card.offer}</div>}
      {card.channel && <div>Canal: {card.channel}{card.route_type ? ` · ${card.route_type}` : ""}</div>}
      {phone && (
        <button
          type="button"
          data-testid="confenge-copy-phone"
          className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px]"
          onClick={() => { void navigator.clipboard.writeText(phone); }}
        >
          Copiar telefone
        </button>
      )}
      {card.person_id && <div className="text-[11px] text-slate-400">Pessoa extra-cli: {card.person_id}</div>}
      {card.factual_hook && <div>Gancho: {card.factual_hook}</div>}
      {card.confidence && <div className="text-[11px] text-slate-400">Confianca: {card.confidence}</div>}
      {card.route_epistemology && (
        <div data-testid="confenge-route-epistemology" className="rounded border border-amber-200 bg-amber-50 px-2 py-1 text-amber-900">
          {card.route_epistemology}
        </div>
      )}
      {(copy.opening || copy.body) && (
        <div data-testid="confenge-today-copy" className="rounded border border-slate-100 bg-slate-50 px-2 py-1.5 space-y-0.5 whitespace-pre-wrap">
          {copy.subject && <div>Assunto: {copy.subject}</div>}
          {copy.opening ? (
            <>
              <div>Abertura: {copy.opening}</div>
              {copy.reason_for_call && <div>Motivo: {copy.reason_for_call}</div>}
              {copy.ask && <div>Pedido: {copy.ask}</div>}
            </>
          ) : (
            copy.body && <div>{copy.body}</div>
          )}
          {copy.do_not_claim?.map((line) => (
            <div key={line} className="text-amber-800">Não alegar: {line}</div>
          ))}
        </div>
      )}
      {founderFacingCopy(copy) && (
        <button
          type="button"
          data-testid="confenge-today-copy-text"
          className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px]"
          onClick={() => { void navigator.clipboard.writeText(founderFacingCopy(copy)); }}
        >
          Revisar / copiar
        </button>
      )}
      {card.evidence && card.evidence.length > 0 && (
        <div data-testid="confenge-today-evidence" className="text-[11px] text-slate-400">Evidência: {card.evidence.join(", ")}</div>
      )}
      {card.warnings?.map((w) => (
        <div key={w} className="text-amber-800">{w}</div>
      ))}
      {card.last_outcome && <div>Ultimo outcome: {stateLabel(card.last_outcome)}</div>}
      {card.next_action && <div>Proxima acao: {stateLabel(card.next_action)}{card.next_action_at ? ` · ${card.next_action_at}` : ""}</div>}
      <div className="flex flex-wrap items-end gap-1.5 pt-1">
        <label className="text-[11px] text-slate-500">
          Outcome
          <select
            data-testid="confenge-outcome-select"
            className="mt-0.5 h-7 block min-w-[180px] rounded-md border border-slate-200 bg-white px-2 text-[12.5px]"
            value={outcome}
            onChange={(e) => setOutcome(e.target.value)}
          >
            <option value="">Registrar resultado</option>
            {OUTCOME_OPTIONS.map((code) => (
              <option key={code} value={code}>{stateLabel(code)}</option>
            ))}
          </select>
        </label>
        {outcome === "REFERRED_TO_OTHER_PERSON" && (
          <>
            <TextInput value={referralName} onChange={setReferralName} placeholder="Nome indicado" />
            <TextInput value={referralRole} onChange={setReferralRole} placeholder="Funcao" />
          </>
        )}
        {(outcome === "FOLLOW_UP" || outcome === "NO_ANSWER" || outcome === "CALLBACK_REQUESTED" || outcome === "ATTEMPTED") && (
          <label className="text-[11px] text-slate-500">
            Proximo prazo
            <input
              type="datetime-local"
              data-testid="confenge-next-action-at"
              className="mt-0.5 h-7 block rounded-md border border-slate-200 bg-white px-2 text-[12.5px]"
              value={nextActionAt}
              onChange={(e) => setNextActionAt(e.target.value)}
            />
          </label>
        )}
        <button
          type="button"
          data-testid="confenge-mark-attempted"
          className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px]"
          disabled={submitting}
          onClick={() => onOutcome({ outcome_code: "ATTEMPTED" })}
        >
          Marcar tentativa
        </button>
        <button
          type="button"
          data-testid="confenge-record-outcome"
          className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px]"
          disabled={submitting || !outcome}
          onClick={() => onOutcome({
            outcome_code: outcome,
            referral_name: referralName,
            referral_role: referralRole,
            next_action_type: outcome === "FOLLOW_UP" ? card.action_type : undefined,
            next_action_at: nextActionAt ? new Date(nextActionAt).toISOString() : undefined,
          })}
        >
          Registrar
        </button>
      </div>
    </article>
  );
}

function ReadinessItem({ label, value }: { label: string; value: string }) {
  return (
    <span className="rounded-md border border-slate-200 bg-slate-50 px-2 py-1">
      <strong className="text-slate-700">{label}:</strong> {value}
    </span>
  );
}
