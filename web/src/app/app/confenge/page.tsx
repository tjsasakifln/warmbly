import { useEffect, useMemo, useState } from "react";
import { Navigate } from "react-router-dom";
import { Check, Loader2, RefreshCw, SkipForward, X } from "lucide-react";
import { Page, PageTopbar } from "@/components/layout/Page";
import {
    useConfengeAccounts,
    useConfengeDrafts,
    useConfengeStatus,
    useConfengeSummary,
    useBootstrapConfengeCampaign,
    useEnrollConfengeDraft,
    useGenerateConfengeDraft,
    useReviewConfengeDraft,
} from "@/lib/api/hooks/app/confenge/useConfenge";
import type { ConfengeDraft } from "@/lib/api/models/app/confenge/Confenge";


function readinessTone(v: string | undefined): string {
    if (!v) return "text-slate-500";
    if (v === "ready" || v === "just now") return "text-emerald-700";
    if (v === "blocked_by_policy" || v === "paused") return "text-rose-700";
    if (v === "fallback_template" || v === "not_configured") return "text-amber-700";
    return "text-slate-600";
}

function ReadinessCard({ status }: { status: NonNullable<ReturnType<typeof useConfengeStatus>["data"]> }) {
    const r = status.readiness;
    if (!r) return null;
    const rows: { label: string; value: string }[] = [
        { label: "EMAIL", value: r.email },
        { label: "WHATSAPP", value: r.whatsapp },
        { label: "FEED AGE", value: r.feed_age || "unknown" },
        { label: "OUTCOME", value: r.outcome_loop },
        { label: "AI", value: r.ai },
        { label: "GOVERNOR", value: `${r.governor_cap}/day` },
        { label: "QUEUE", value: String(r.queue_count) },
    ];
    return (
        <section className="rounded-md border border-slate-200 bg-white px-3 py-2.5">
            <div className="flex items-center justify-between gap-2 mb-2">
                <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Readiness</div>
                {r.kill_switch || status.kill_switch ? (
                    <span className="text-[10px] uppercase tracking-[0.14em] px-1.5 py-0.5 rounded bg-rose-50 text-rose-700">
                        sending paused
                    </span>
                ) : (
                    <span className="text-[10px] uppercase tracking-[0.14em] px-1.5 py-0.5 rounded bg-emerald-50 text-emerald-700">
                        sending allowed
                    </span>
                )}
            </div>
            <div className="flex flex-wrap gap-x-4 gap-y-1.5">
                {rows.map((row) => (
                    <div key={row.label} className="min-w-[88px]">
                        <div className="text-[10px] uppercase tracking-[0.14em] text-slate-400">{row.label}</div>
                        <div className={`text-[12.5px] font-medium tabular-nums ${readinessTone(row.value)}`}>
                            {row.value}
                        </div>
                    </div>
                ))}
            </div>
        </section>
    );
}

export default function ConfengePage() {
    const status = useConfengeStatus();
    const enabled = !!status.data?.enabled;
    const summary = useConfengeSummary(enabled);
    const ready = useConfengeAccounts("READY_TO_GENERATE", enabled);
    const needsContact = useConfengeAccounts("NEEDS_CONTACT", enabled);
    const drafts = useConfengeDrafts("NEEDS_REVIEW", enabled);
    const approved = useConfengeDrafts("APPROVED", enabled);
    const generate = useGenerateConfengeDraft();
    const review = useReviewConfengeDraft();
    const enroll = useEnrollConfengeDraft();
    const bootstrap = useBootstrapConfengeCampaign();

    const [idx, setIdx] = useState(0);
    const queue = drafts.data ?? [];
    const current: ConfengeDraft | undefined = queue[idx];

    const [subject, setSubject] = useState("");
    const [body, setBody] = useState("");

    useEffect(() => {
        if (current) {
            setSubject(current.subject);
            setBody(current.body_text);
        }
    }, [current?.id]);

    const stats = useMemo(() => {
        const s = summary.data;
        if (!s) return [];
        return [
            { label: "Needs contact", value: s.needs_contact },
            { label: "Ready", value: s.ready_to_generate },
            { label: "Review", value: s.needs_review },
            { label: "Approved", value: s.approved },
            { label: "Enrolled", value: s.enrolled },
            { label: "Sent", value: s.sent },
            { label: "Replied", value: s.replied },
            { label: "Blocked", value: s.blocked + s.do_not_contact },
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

    if (!enabled) {
        return <Navigate to="/app" replace />;
    }

    return (
        <Page>
            <PageTopbar
                eyebrow="CONFENGE"
                subtitle="Review queue for intelligence-plane leads. Metric: commercial outcomes per human minute."
            >
                <button
                    type="button"
                    className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px] text-slate-700 hover:bg-slate-50"
                    disabled={bootstrap.isPending}
                    onClick={() => bootstrap.mutate()}
                >
                    Bootstrap campaign
                </button>
            </PageTopbar>
            <div className="flex flex-col gap-4 p-4 md:p-6 max-w-6xl mx-auto w-full">

                <div className="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-8 gap-2">
                    {stats.map((s) => (
                        <div
                            key={s.label}
                            className="rounded-md border border-slate-200 bg-white px-2.5 py-2"
                        >
                            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">{s.label}</div>
                            <div className="text-lg font-semibold text-slate-900 tabular-nums">{s.value}</div>
                        </div>
                    ))}
                </div>

                {status.data && <ReadinessCard status={status.data} />}

                <div className="grid md:grid-cols-2 gap-4">
                    <section className="rounded-md border border-slate-200 bg-white">
                        <div className="px-3 h-10 flex items-center border-b border-slate-200 text-[12.5px] font-medium text-slate-900">
                            Ready to generate ({ready.data?.length ?? 0})
                        </div>
                        <ul className="divide-y divide-slate-100 max-h-64 overflow-auto">
                            {(ready.data ?? []).map((a) => (
                                <li key={a.id} className="px-3 py-2 flex items-center justify-between gap-2 text-[12.5px]">
                                    <div className="min-w-0">
                                        <div className="font-medium text-slate-900 truncate">
                                            {a.nome_fantasia || a.razao_social}
                                        </div>
                                        <div className="text-slate-500 truncate">
                                            {a.cnpj14} · {a.municipio}/{a.uf} · {a.service_code}
                                        </div>
                                    </div>
                                    <button
                                        type="button"
                                        className="shrink-0 h-7 px-2 rounded-md bg-sky-50 text-sky-700 border border-sky-200 hover:bg-sky-100"
                                        disabled={generate.isPending}
                                        onClick={() => generate.mutate({ accountId: a.id })}
                                    >
                                        Generate
                                    </button>
                                </li>
                            ))}
                            {!ready.data?.length && (
                                <li className="px-3 py-6 text-center text-slate-400 text-[12.5px]">No accounts ready</li>
                            )}
                        </ul>
                    </section>

                    <section className="rounded-md border border-slate-200 bg-white">
                        <div className="px-3 h-10 flex items-center border-b border-slate-200 text-[12.5px] font-medium text-slate-900">
                            Needs contact ({needsContact.data?.length ?? 0})
                        </div>
                        <ul className="divide-y divide-slate-100 max-h-64 overflow-auto">
                            {(needsContact.data ?? []).map((a) => (
                                <li key={a.id} className="px-3 py-2 text-[12.5px]">
                                    <div className="font-medium text-slate-900 truncate">
                                        {a.nome_fantasia || a.razao_social}
                                    </div>
                                    <div className="text-slate-500 truncate">
                                        {a.cnpj14} · {a.moment_code || "—"} · {a.fact_to_mention || "no fact"}
                                    </div>
                                </li>
                            ))}
                            {!needsContact.data?.length && (
                                <li className="px-3 py-6 text-center text-slate-400 text-[12.5px]">None waiting</li>
                            )}
                        </ul>
                    </section>
                </div>

                {(approved.data?.length ?? 0) > 0 && (
                    <section className="rounded-md border border-slate-200 bg-white">
                        <div className="px-3 h-10 flex items-center border-b border-slate-200 text-[12.5px] font-medium text-slate-900">
                            Approved — ready to enroll ({approved.data?.length ?? 0})
                        </div>
                        <ul className="divide-y divide-slate-100">
                            {(approved.data ?? []).map((d) => (
                                <li key={d.id} className="px-3 py-2 flex items-center justify-between gap-2 text-[12.5px]">
                                    <div className="min-w-0">
                                        <div className="font-medium truncate">{d.recipient_email}</div>
                                        <div className="text-slate-500 truncate">{d.subject}</div>
                                    </div>
                                    <button
                                        type="button"
                                        className="shrink-0 h-7 px-2 rounded-md bg-sky-600 text-white hover:bg-sky-700"
                                        disabled={enroll.isPending}
                                        onClick={() => enroll.mutate(d.id)}
                                    >
                                        Enroll
                                    </button>
                                </li>
                            ))}
                        </ul>
                    </section>
                )}

                <section className="rounded-md border border-slate-200 bg-white">
                    <div className="px-3 h-10 flex items-center justify-between border-b border-slate-200">
                        <span className="text-[12.5px] font-medium text-slate-900">
                            Review queue ({queue.length})
                            {current ? ` · ${idx + 1}/${queue.length}` : ""}
                        </span>
                        {current && (
                            <span
                                className={
                                    "text-[10px] uppercase tracking-[0.14em] px-1.5 py-0.5 rounded " +
                                    (current.risk_class === "GREEN"
                                        ? "bg-emerald-50 text-emerald-700"
                                        : current.risk_class === "RED"
                                          ? "bg-rose-50 text-rose-700"
                                          : "bg-amber-50 text-amber-700")
                                }
                            >
                                {current.risk_class}
                            </span>
                        )}
                    </div>

                    {!current ? (
                        <div className="px-3 py-10 text-center text-slate-400 text-[12.5px]">
                            No drafts waiting for review. Generate from ready accounts first.
                        </div>
                    ) : (
                        <div className="p-3 grid md:grid-cols-2 gap-4">
                            <div className="space-y-2 text-[12.5px]">
                                <div>
                                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Recipient</div>
                                    <div className="text-slate-900 font-medium">
                                        {current.recipient_name || "—"} · {current.recipient_role || "—"}
                                    </div>
                                    <div className="text-slate-600">{current.recipient_email}</div>
                                    <div className="text-slate-500">{current.verification_status}</div>
                                </div>
                                <div>
                                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Fact</div>
                                    <div className="text-slate-800">{current.fact_used || "—"}</div>
                                </div>
                                <div>
                                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Service</div>
                                    <div className="text-slate-800">{current.service_code}</div>
                                </div>
                                <div>
                                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Provider</div>
                                    <div className="text-slate-800">
                                        {current.provider}/{current.model}
                                        {current.provider === "template" ? " (fallback, not AI)" : ""}
                                    </div>
                                </div>
                                {!!current.risk_flags?.length && (
                                    <div>
                                        <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">Flags</div>
                                        <div className="text-slate-600">{current.risk_flags.join(", ")}</div>
                                    </div>
                                )}
                            </div>

                            <div className="space-y-2">
                                <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                                    Subject
                                    <input
                                        className="mt-1 w-full h-7 rounded-md border border-slate-200 px-2 text-[12.5px] text-slate-900 focus:border-sky-400 focus:ring-1 focus:ring-sky-100 outline-none"
                                        value={subject}
                                        onChange={(e) => setSubject(e.target.value)}
                                    />
                                </label>
                                <label className="block text-[10px] uppercase tracking-[0.14em] text-slate-500">
                                    Message
                                    <textarea
                                        className="mt-1 w-full min-h-[160px] rounded-md border border-slate-200 px-2 py-1.5 text-[12.5px] text-slate-900 focus:border-sky-400 focus:ring-1 focus:ring-sky-100 outline-none font-sans"
                                        value={body}
                                        onChange={(e) => setBody(e.target.value)}
                                    />
                                </label>

                                <div className="flex flex-wrap gap-1.5 pt-1">
                                    <ActionBtn
                                        icon={<Check className="h-3.5 w-3.5" />}
                                        label="Approve"
                                        accent
                                        disabled={review.isPending}
                                        onClick={() =>
                                            review.mutate(
                                                {
                                                    id: current.id,
                                                    action: "edit",
                                                    subject,
                                                    body_text: body,
                                                },
                                                {
                                                    onSuccess: () =>
                                                        review.mutate({ id: current.id, action: "approve" }),
                                                },
                                            )
                                        }
                                    />
                                    <ActionBtn
                                        icon={<RefreshCw className="h-3.5 w-3.5" />}
                                        label="Save edit"
                                        disabled={review.isPending}
                                        onClick={() =>
                                            review.mutate({
                                                id: current.id,
                                                action: "edit",
                                                subject,
                                                body_text: body,
                                            })
                                        }
                                    />
                                    <ActionBtn
                                        icon={<SkipForward className="h-3.5 w-3.5" />}
                                        label="Skip"
                                        disabled={review.isPending}
                                        onClick={() => {
                                            review.mutate({ id: current.id, action: "skip" });
                                            setIdx((i) => Math.min(i, Math.max(0, queue.length - 2)));
                                        }}
                                    />
                                    <ActionBtn
                                        icon={<X className="h-3.5 w-3.5" />}
                                        label="Block"
                                        danger
                                        disabled={review.isPending}
                                        onClick={() =>
                                            review.mutate({
                                                id: current.id,
                                                action: "block",
                                                do_not_contact: false,
                                            })
                                        }
                                    />
                                </div>
                                <p className="text-[11px] text-slate-400">
                                    Shortcuts later: A approve · S skip · E edit focus. Auto-send stays off.
                                </p>
                            </div>
                        </div>
                    )}
                </section>
            </div>
        </Page>
    );
}

function ActionBtn({
    icon,
    label,
    onClick,
    disabled,
    accent,
    danger,
}: {
    icon: React.ReactNode;
    label: string;
    onClick: () => void;
    disabled?: boolean;
    accent?: boolean;
    danger?: boolean;
}) {
    const cls = accent
        ? "bg-sky-600 text-white border-sky-600 hover:bg-sky-700"
        : danger
          ? "bg-white text-rose-700 border-rose-200 hover:bg-rose-50"
          : "bg-white text-slate-700 border-slate-200 hover:bg-slate-50";
    return (
        <button
            type="button"
            disabled={disabled}
            onClick={onClick}
            className={`h-7 px-2.5 inline-flex items-center gap-1 rounded-md border text-[12.5px] disabled:opacity-50 ${cls}`}
        >
            {icon}
            {label}
        </button>
    );
}
