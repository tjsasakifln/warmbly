import { useEffect, useMemo, useState } from "react";
import { Navigate } from "react-router-dom";
import { Check, Loader2, SkipForward, Ban, RefreshCw, MessageSquareText } from "lucide-react";
import { Page, PageTopbar } from "@/components/layout/Page";
import {
  useApproveAndQueueTouchpoint,
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
  useResumeConfengeDispatch,
  useRejectConfengeTouchpoint,
  useSkipConfengeTouchpoint,
} from "@/lib/api/hooks/app/confenge/useConfenge";
import type {
  ConfengeAttentionFilter,
  ConfengeTouchpoint,
  ConfengeWorkingQueueItem,
} from "@/lib/api/models/app/confenge/Confenge";

const ATTENTION_FILTERS: { id: ConfengeAttentionFilter; label: string }[] = [
  { id: "needs_attention", label: "Needs attention" },
  { id: "awaiting_approval", label: "Awaiting approval" },
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
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [recipient, setRecipient] = useState("");

  useEffect(() => {
    if (current) {
      setSubject(current.subject ?? "");
      setBody(current.body_text ?? "");
      setRecipient(current.recipient ?? "");
    }
  }, [current?.id, current?.content_hash]);

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
      { label: "Ready", value: s?.ready_to_generate ?? 0 },
      { label: "Review", value: s?.needs_review ?? w?.needs_review ?? 0 },
      { label: "Sent", value: s?.sent ?? 0 },
      { label: "Human reply", value: s?.replied ?? 0 },
      { label: "Meeting", value: s?.meeting ?? 0 },
      { label: "Proposal", value: s?.proposal ?? 0 },
      { label: "Won", value: s?.won ?? 0 },
      { label: "DNC", value: s?.do_not_contact ?? 0 },
    ];
    if (w) {
      return [
        { label: "Reservatório", value: w.reservoir_monitored },
        { label: "Agora", value: w.actionable_now },
        { label: "EMAIL ready", value: w.actionable_now },
        { label: "Sent", value: s?.sent ?? 0 },
        { label: "Replied", value: s?.replied ?? 0 },
        { label: "Rate", value: dispatchStatus.data ? `${dispatchStatus.data.sent_last_hour}/${dispatchStatus.data.cap}` : "—" },
      ];
    }
    if (!s) return core;
    return core;
  }, [summary.data, workingOverview.data, dispatchStatus.data]);

  const capacityLabel = useMemo(() => {
    const w = workingOverview.data;
    if (!w || !w.theoretical_slots_24h) return null;
    return `${w.due_next_24h} due / ${w.theoretical_slots_24h} slots teóricos (24h)`;
  }, [workingOverview.data]);

  if (status.isLoading) {
    return (
      <Page>
        <div className="flex items-center gap-2 text-slate-500 text-[12.5px] p-6">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading…
        </div>
      </Page>
    );
  }
  if (!enabled) return <Navigate to="/app" replace />;

  const company =
    current?.account?.nome_fantasia ||
    current?.account?.razao_social ||
    current?.account_id?.slice(0, 8) ||
    "—";

  return (
    <Page>
      <PageTopbar
        eyebrow="CONFENGE"
        subtitle="Per-message human approval. Every touch needs Approve & Queue. AI never approves or sends. No whole-sequence approve."
      >
        {dispatchStatus.data && (
          <div className="flex items-center gap-2 mr-2">
            <span
              className={`h-7 px-2.5 inline-flex items-center rounded-md border text-[12.5px] tabular-nums ${
                dispatchStatus.data.paused
                  ? "border-amber-200 bg-amber-50 text-amber-800"
                  : "border-slate-200 bg-white text-slate-700"
              }`}
              title={
                dispatchStatus.data.paused
                  ? `Paused: ${dispatchStatus.data.pause_reason || "manual"}`
                  : `Next slot: ${dispatchStatus.data.next_slot_at || "available"} · queued ${dispatchStatus.data.queued_approved}`
              }
              data-testid="confenge-dispatch-quota"
            >
              {dispatchStatus.data.sent_last_hour}/{dispatchStatus.data.cap} na última hora
            </span>
            {dispatchStatus.data.paused ? (
              <button
                type="button"
                className="h-7 px-2.5 rounded-md border border-sky-200 bg-sky-50 text-sky-700 text-[12.5px] hover:bg-sky-100"
                disabled={resumeDispatch.isPending}
                onClick={() => resumeDispatch.mutate()}
              >
                Resume
              </button>
            ) : (
              <button
                type="button"
                className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px] text-slate-700 hover:bg-slate-50"
                disabled={pauseDispatch.isPending}
                onClick={() => pauseDispatch.mutate("manual_pause")}
              >
                Pause
              </button>
            )}
          </div>
        )}
      </PageTopbar>
      <div className="flex flex-col gap-4 p-4 md:p-6 max-w-6xl mx-auto w-full">
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-2">
          {stats.map((s) => (
            <div key={s.label} className="rounded-md border border-slate-200 bg-white px-2.5 py-2">
              <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">{s.label}</div>
              <div
                className="text-lg font-semibold text-slate-900 tabular-nums"
                data-testid={`confenge-stat-${s.label.toLowerCase().replace(/\s+/g, "-")}`}
              >
                {s.value}
              </div>
            </div>
          ))}
        </div>
        {capacityLabel && (
          <div
            className="text-[12.5px] text-slate-600 tabular-nums"
            data-testid="confenge-capacity-load"
          >
            Capacidade (planejamento, não governor): {capacityLabel}
            {workingOverview.data?.dynamic_priority_enabled ? " · prioridade dinâmica ON" : " · prioridade dinâmica OFF (shadow)"}
          </div>
        )}
        <p className="text-[12.5px] text-slate-500" data-testid="confenge-activation-hint">
          Prioridade comercial vem do extra-cli (activation score = ordenação, não chance de compra).
          Conta com contexto desatualizado exige regenerar e reaprovar antes do envio.
        </p>

        {/* Agora — ACTIONABLE_NOW + operationally due */}
        <section className="rounded-md border border-slate-200 bg-white" data-testid="confenge-agora">
          <div className="shrink-0 px-3 h-10 flex items-center border-b border-slate-200">
            <span className="text-[12.5px] font-medium text-slate-900">Agora</span>
            <span className="ml-2 text-[12.5px] text-slate-500 tabular-nums">
              {(agoraQueue.data ?? []).length}
            </span>
          </div>
          <WorkingLaneList
            items={agoraQueue.data ?? []}
            empty="Nenhuma conta due agora"
            onPlan={(id) => plan.mutate({ accountId: id })}
          />
        </section>

        {/* Precisa de contato */}
        <section className="rounded-md border border-slate-200 bg-white" data-testid="confenge-needs-contact">
          <div className="shrink-0 px-3 h-10 flex items-center border-b border-slate-200">
            <span className="text-[12.5px] font-medium text-slate-900">Precisa de contato</span>
            <span className="ml-2 text-[12.5px] text-slate-500 tabular-nums">
              {(needsContactQueue.data ?? []).length}
            </span>
          </div>
          <WorkingLaneList
            items={needsContactQueue.data ?? []}
            empty="Nenhuma conta prioritária sem destinatário"
            showContactGap
          />
        </section>

        {/* Needs attention cockpit */}
        <section className="rounded-md border border-slate-200 bg-white" data-testid="confenge-needs-attention">
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
                        {a.intent || a.commercial_state || a.queue_state}
                        {a.contact_email ? ` · ${a.contact_email}` : ""}
                      </div>
                    </button>
                  </li>
                );
              })}
              {!attention.data?.length && (
                <li className="px-3 py-8 text-center text-slate-400 text-[12.5px]">
                  Nothing in this filter
                </li>
              )}
            </ul>
            <div className="md:col-span-3 p-3 space-y-2 text-[12.5px]">
              {!item ? (
                <div className="py-10 text-center text-slate-400">
                  Select an account to inspect the reply handoff
                </div>
              ) : (
                <>
                  <div>
                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Company</div>
                    <div className="font-medium">{item.company_name || "—"}</div>
                  </div>
                  <div>
                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Intent</div>
                    <div data-testid="confenge-attention-intent">
                      {item.intent || item.commercial_state || "—"}
                    </div>
                  </div>
                  <div>
                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Thread</div>
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
                    Generate reply draft
                  </button>
                  <p className="text-[11px] text-slate-400">
                    Reply drafts land in Awaiting approval. AI never auto-sends.
                  </p>
                </>
              )}
            </div>
          </div>
        </section>

        <section className="rounded-md border border-slate-200 bg-white">
          <div className="px-3 h-10 flex items-center border-b border-slate-200 text-[12.5px] font-medium">
            Ready to plan ({ready.data?.length ?? 0})
          </div>
          <ul className="divide-y divide-slate-100 max-h-48 overflow-auto">
            {(ready.data ?? []).map((a) => (
              <li key={a.id} className="px-3 py-2 flex items-center justify-between gap-2 text-[12.5px]">
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
                  Plan & generate
                </button>
              </li>
            ))}
            {!ready.data?.length && (
              <li className="px-3 py-6 text-center text-slate-400 text-[12.5px]">None ready</li>
            )}
          </ul>
        </section>

        <section className="rounded-md border border-slate-200 bg-white" data-testid="confenge-review-queue">
          <div className="px-3 h-10 flex items-center justify-between border-b border-slate-200">
            <span className="text-[12.5px] font-medium">
              Review queue ({queue.length})
              {current ? ` · ${idx + 1}/${queue.length}` : ""}
            </span>
            {current && (
              <span className="text-[10px] uppercase tracking-[0.14em] px-1.5 py-0.5 rounded bg-sky-50 text-sky-700">
                Touch {current.ordinal} · {current.channel} · {current.state}
              </span>
            )}
          </div>
          {!current ? (
            <div className="px-3 py-10 text-center text-slate-400 text-[12.5px]">
              No messages waiting. Plan cadence, then generate when due.
            </div>
          ) : (
            <div className="p-3 grid md:grid-cols-2 gap-4">
              <div className="space-y-2 text-[12.5px]">
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Company</div>
                  <div className="font-medium text-slate-900" data-testid="confenge-company">
                    {company}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Exact recipient
                  </div>
                  <div className="font-medium break-all" data-testid="confenge-recipient">
                    {current.channel === "WHATSAPP" ? "Phone" : "Email"}: {current.recipient || "—"}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Channel / service
                  </div>
                  <div data-testid="confenge-channel-service">
                    {current.channel} · {current.service_code || "—"}
                  </div>
                </div>

                {/* Operator strategy cockpit (not prospect-facing) */}
                <div
                  className="rounded-md border border-slate-200 bg-slate-50 p-2 space-y-1.5"
                  data-testid="confenge-strategy-explain"
                >
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Strategy {current.strategy_explain?.doctrine_version || "confenge-outreach-v1"}
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
                  <StrategyRow label="Touch" value={current.strategy_explain?.touch || String(current.ordinal)} />
                  <StrategyRow label="Experimento" value={current.strategy_explain?.experiment} />
                </div>
                {(current.doctrine_alerts?.length ?? 0) > 0 && (
                  <div className="space-y-1" data-testid="confenge-doctrine-alerts">
                    {current.doctrine_alerts!.map((a) => (
                      <div
                        key={a}
                        className="text-[11px] text-amber-800 bg-amber-50 border border-amber-100 rounded px-2 py-1"
                      >
                        ⚠ {a}
                      </div>
                    ))}
                  </div>
                )}
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Fact / evidence
                  </div>
                  <div data-testid="confenge-evidence">{current.fact_used || "—"}</div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">History</div>
                  <ul className="max-h-24 overflow-auto text-slate-600">
                    {(timeline.data ?? []).map((t) => (
                      <li key={t.id}>
                        #{t.ordinal} {t.purpose} · {t.channel} · {t.state}
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
              <div className="space-y-2">
                <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                  Exact recipient
                  <input
                    data-testid="confenge-recipient-input"
                    className="mt-1 w-full h-7 rounded-md border border-slate-200 px-2 text-[12.5px]"
                    value={recipient}
                    onChange={(e) => setRecipient(e.target.value)}
                  />
                </label>
                {current.channel !== "WHATSAPP" && (
                  <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Subject
                    <input
                      data-testid="confenge-subject-input"
                      className="mt-1 w-full h-7 rounded-md border border-slate-200 px-2 text-[12.5px]"
                      value={subject}
                      onChange={(e) => setSubject(e.target.value)}
                    />
                  </label>
                )}
                <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                  Exact send preview
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
                      Generate
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
                    Approve & Queue
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
                    Skip
                  </button>
                  <button
                    type="button"
                    className="h-7 px-2.5 rounded-md border border-rose-200 text-rose-700 text-[12.5px]"
                    disabled={dnc.isPending}
                    onClick={() => dnc.mutate(current.account_id)}
                  >
                    <Ban className="inline h-3.5 w-3.5 mr-1" />
                    DNC
                  </button>
                </div>
                <div
                  className="flex flex-wrap gap-1 pt-1"
                  data-testid="confenge-reject-reasons"
                >
                  <span className="text-[10px] uppercase tracking-[0.14em] text-slate-400 w-full">
                    Reject reason
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
                      {reason.replace(/_/g, " ")}
                    </button>
                  ))}
                </div>
                <p className="text-[11px] text-slate-400">
                  Editing clears approval. No approve whole sequence.
                </p>
              </div>
            </div>
          )}
        </section>
      </div>
    </Page>
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
        const reasons = (it.reason_codes ?? a.activation_reason_codes ?? []).join(", ");
        const score = it.activation_score ?? a.activation_score;
        const stale = it.context_stale || a.context_stale;
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
                {it.next_best_action_at ? ` · next ${it.next_best_action_at.slice(0, 10)}` : ""}
                {it.activation_expires_at ? ` · exp ${it.activation_expires_at.slice(0, 10)}` : ""}
              </div>
              {showContactGap && (
                <div className="text-amber-700 text-[11px] mt-0.5">
                  Momento comercial forte, mas contato ainda não resolvido
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
                Plan
              </button>
            )}
          </li>
        );
      })}
    </ul>
  );
}
