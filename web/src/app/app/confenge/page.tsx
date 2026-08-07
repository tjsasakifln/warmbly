import { useEffect, useMemo, useState } from "react";
import { Navigate } from "react-router-dom";
import { Check, Loader2, SkipForward, Ban, RefreshCw } from "lucide-react";
import { Page, PageTopbar } from "@/components/layout/Page";
import {
  useApproveAndQueueTouchpoint,
  useConfengeAccountTimeline,
  useConfengeAccounts,
  useConfengeReviewTouchpoints,
  useConfengeStatus,
  useConfengeSummary,
  useDncConfengeAccount,
  useGenerateConfengeTouchpoint,
  usePlanConfengeCadence,
  useSkipConfengeTouchpoint,
} from "@/lib/api/hooks/app/confenge/useConfenge";
import type { ConfengeTouchpoint } from "@/lib/api/models/app/confenge/Confenge";

export default function ConfengePage() {
  const status = useConfengeStatus();
  const enabled = !!status.data?.enabled;
  const summary = useConfengeSummary(enabled);
  const ready = useConfengeAccounts("READY_TO_GENERATE", enabled);
  const review = useConfengeReviewTouchpoints(enabled);
  const plan = usePlanConfengeCadence();
  const genTouch = useGenerateConfengeTouchpoint();
  const approveQueue = useApproveAndQueueTouchpoint();
  const skip = useSkipConfengeTouchpoint();
  const dnc = useDncConfengeAccount();
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

  const stats = useMemo(() => {
    const s = summary.data;
    if (!s) return [];
    return [
      { label: "Ready", value: s.ready_to_generate },
      { label: "Review", value: s.needs_review },
      { label: "Sent", value: s.sent },
      { label: "Replied", value: s.replied },
      { label: "DNC", value: s.do_not_contact },
    ];
  }, [summary.data]);

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

  const company = current?.account?.nome_fantasia || current?.account?.razao_social || current?.account_id?.slice(0, 8) || "—";

  return (
    <Page>
      <PageTopbar
        eyebrow="CONFENGE"
        subtitle="Per-message human approval. Every touch needs Approve & Queue. AI never approves or sends. No whole-sequence approve."
      />
      <div className="flex flex-col gap-4 p-4 md:p-6 max-w-6xl mx-auto w-full">
        <div className="grid grid-cols-2 sm:grid-cols-5 gap-2">
          {stats.map((s) => (
            <div key={s.label} className="rounded-md border border-slate-200 bg-white px-2.5 py-2">
              <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">{s.label}</div>
              <div className="text-lg font-semibold text-slate-900 tabular-nums">{s.value}</div>
            </div>
          ))}
        </div>

        <section className="rounded-md border border-slate-200 bg-white">
          <div className="px-3 h-10 flex items-center border-b border-slate-200 text-[12.5px] font-medium">
            Ready to plan ({ready.data?.length ?? 0})
          </div>
          <ul className="divide-y divide-slate-100 max-h-48 overflow-auto">
            {(ready.data ?? []).map((a) => (
              <li key={a.id} className="px-3 py-2 flex items-center justify-between gap-2 text-[12.5px]">
                <div className="min-w-0">
                  <div className="font-medium truncate">{a.nome_fantasia || a.razao_social}</div>
                  <div className="text-slate-500 truncate">{a.cnpj14} · {a.service_code}</div>
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
            {!ready.data?.length && <li className="px-3 py-6 text-center text-slate-400 text-[12.5px]">None ready</li>}
          </ul>
        </section>

        <section className="rounded-md border border-slate-200 bg-white">
          <div className="px-3 h-10 flex items-center justify-between border-b border-slate-200">
            <span className="text-[12.5px] font-medium">
              Review queue ({queue.length}){current ? ` · ${idx + 1}/${queue.length}` : ""}
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
                  <div className="font-medium text-slate-900">{company}</div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Exact recipient</div>
                  <div className="font-medium break-all">
                    {current.channel === "WHATSAPP" ? "Phone" : "Email"}: {current.recipient || "—"}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Channel / service</div>
                  <div>{current.channel} · {current.service_code || "—"}</div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Fact / evidence</div>
                  <div>{current.fact_used || "—"}</div>
                </div>
                <div>
                  <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">History</div>
                  <ul className="max-h-24 overflow-auto text-slate-600">
                    {(timeline.data ?? []).map((t) => (
                      <li key={t.id}>#{t.ordinal} {t.purpose} · {t.channel} · {t.state}</li>
                    ))}
                  </ul>
                </div>
              </div>
              <div className="space-y-2">
                <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                  Exact recipient
                  <input className="mt-1 w-full h-7 rounded-md border border-slate-200 px-2 text-[12.5px]" value={recipient} onChange={(e) => setRecipient(e.target.value)} />
                </label>
                {current.channel !== "WHATSAPP" && (
                  <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                    Subject
                    <input className="mt-1 w-full h-7 rounded-md border border-slate-200 px-2 text-[12.5px]" value={subject} onChange={(e) => setSubject(e.target.value)} />
                  </label>
                )}
                <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                  Exact send preview
                  <textarea className="mt-1 w-full min-h-[160px] rounded-md border border-slate-200 px-2 py-1.5 text-[12.5px]" value={body} onChange={(e) => setBody(e.target.value)} />
                </label>
                <div className="flex flex-wrap gap-1.5 pt-1">
                  {(current.state === "DUE" || !current.body_text) && (
                    <button type="button" className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px]" disabled={genTouch.isPending} onClick={() => genTouch.mutate(current.id)}>
                      <RefreshCw className="inline h-3.5 w-3.5 mr-1" />Generate
                    </button>
                  )}
                  <button
                    type="button"
                    className="h-7 px-2.5 rounded-md bg-sky-600 text-white text-[12.5px] disabled:opacity-50"
                    disabled={approveQueue.isPending || !body.trim() || !recipient.trim()}
                    onClick={() => approveQueue.mutate({ id: current.id, subject, body_text: body, recipient })}
                  >
                    <Check className="inline h-3.5 w-3.5 mr-1" />Approve & Queue
                  </button>
                  <button type="button" className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px]" disabled={skip.isPending} onClick={() => skip.mutate(current.id, { onSuccess: () => setIdx((i) => Math.min(i, Math.max(0, queue.length - 2))) })}>
                    <SkipForward className="inline h-3.5 w-3.5 mr-1" />Skip
                  </button>
                  <button type="button" className="h-7 px-2.5 rounded-md border border-rose-200 text-rose-700 text-[12.5px]" disabled={dnc.isPending} onClick={() => dnc.mutate(current.account_id)}>
                    <Ban className="inline h-3.5 w-3.5 mr-1" />DNC
                  </button>
                </div>
                <p className="text-[11px] text-slate-400">Editing clears approval. No approve whole sequence.</p>
              </div>
            </div>
          )}
        </section>
      </div>
    </Page>
  );
}
