import { useEffect, useMemo, useState } from "react";
import { Navigate } from "react-router-dom";
import { Check, Loader2, MessageSquareText, RefreshCw, SkipForward, X } from "lucide-react";
import { Page, PageTopbar } from "@/components/layout/Page";
import {
    useConfengeAccounts,
    useConfengeAttention,
    useConfengeAttentionDetail,
    useConfengeDrafts,
    useConfengeStatus,
    useConfengeSummary,
    useBootstrapConfengeCampaign,
    useChangeConfengeReferral,
    useEnrollConfengeDraft,
    useGenerateConfengeDraft,
    useGenerateConfengeReplyDraft,
    useResumeConfengeAccount,
    useReviewConfengeDraft,
} from "@/lib/api/hooks/app/confenge/useConfenge";
import type {
    ConfengeAttentionFilter,
    ConfengeDraft,
} from "@/lib/api/models/app/confenge/Confenge";

const FILTERS: { id: ConfengeAttentionFilter; label: string }[] = [
    { id: "needs_attention", label: "Needs attention" },
    { id: "awaiting_approval", label: "Awaiting approval" },
    { id: "scheduled", label: "Scheduled" },
    { id: "sent", label: "Sent" },
    { id: "replied", label: "Replied" },
    { id: "dnc", label: "DNC" },
];

export default function ConfengePage() {
    const status = useConfengeStatus();
    const enabled = !!status.data?.enabled;
    const summary = useConfengeSummary(enabled);
    const ready = useConfengeAccounts("READY_TO_GENERATE", enabled);
    const needsContact = useConfengeAccounts("NEEDS_CONTACT", enabled);
    const drafts = useConfengeDrafts("NEEDS_REVIEW", enabled);
    const approved = useConfengeDrafts("APPROVED", enabled);
    const generate = useGenerateConfengeDraft();
    const generateReply = useGenerateConfengeReplyDraft();
    const review = useReviewConfengeDraft();
    const enroll = useEnrollConfengeDraft();
    const bootstrap = useBootstrapConfengeCampaign();
    const resume = useResumeConfengeAccount();
    const referral = useChangeConfengeReferral();

    const [filter, setFilter] = useState<ConfengeAttentionFilter>("needs_attention");
    const attention = useConfengeAttention(filter, enabled);
    const [selectedId, setSelectedId] = useState<string | null>(null);
    const detail = useConfengeAttentionDetail(selectedId);
    const [resumeAt, setResumeAt] = useState("");
    const [resumeNote, setResumeNote] = useState("");
    const [refName, setRefName] = useState("");
    const [refEmail, setRefEmail] = useState("");
    const [refRole, setRefRole] = useState("");

    useEffect(() => {
        const list = attention.data ?? [];
        if (!list.length) {
            setSelectedId(null);
            return;
        }
        if (!selectedId || !list.some((a) => a.account_id === selectedId)) {
            setSelectedId(list[0].account_id);
        }
    }, [attention.data, selectedId]);

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

    const item = detail.data;

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
                subtitle="Reply cockpit + review queue. AI never sends; human approves exact content."
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
                            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                                {s.label}
                            </div>
                            <div className="text-lg font-semibold text-slate-900 tabular-nums">
                                {s.value}
                            </div>
                        </div>
                    ))}
                </div>

                {/* Needs attention cockpit */}
                <section className="rounded-md border border-slate-200 bg-white">
                    <div className="shrink-0 px-3 flex items-center gap-1 border-b border-slate-200 overflow-x-auto">
                        {FILTERS.map((f) => {
                            const active = filter === f.id;
                            return (
                                <button
                                    key={f.id}
                                    type="button"
                                    onClick={() => setFilter(f.id)}
                                    className={
                                        "relative h-10 px-2.5 inline-flex items-center gap-1.5 text-[12.5px] " +
                                        (active
                                            ? "text-slate-900 font-medium"
                                            : "text-slate-500 hover:text-slate-800")
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

                    <div className="grid md:grid-cols-5 min-h-[280px]">
                        <ul className="md:col-span-2 divide-y divide-slate-100 max-h-80 overflow-auto border-r border-slate-100">
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

                        <div className="md:col-span-3 p-3 space-y-3 text-[12.5px]">
                            {!item ? (
                                <div className="py-10 text-center text-slate-400">
                                    Select an account to inspect the reply handoff
                                </div>
                            ) : (
                                <>
                                    <div className="grid sm:grid-cols-2 gap-3">
                                        <Field label="Company" value={item.company_name} />
                                        <Field label="CNPJ" value={item.cnpj14} />
                                        <Field
                                            label="Contact"
                                            value={
                                                [item.contact_name, item.contact_email, item.contact_phone]
                                                    .filter(Boolean)
                                                    .join(" · ") || "—"
                                            }
                                        />
                                        <Field label="Channel" value={item.channel || "EMAIL"} />
                                        <Field
                                            label="Service"
                                            value={
                                                [item.service_code, item.service_name]
                                                    .filter(Boolean)
                                                    .join(" · ") || "—"
                                            }
                                        />
                                        <Field label="Intent" value={item.intent || item.commercial_state || "—"} />
                                        <Field
                                            label="Confidence"
                                            value={
                                                item.confidence != null && item.confidence > 0
                                                    ? String(Math.round(item.confidence * 100) / 100)
                                                    : "—"
                                            }
                                        />
                                        <Field label="Queue" value={item.queue_state} />
                                    </div>
                                    <Field label="Suggested action" value={item.suggested_action || "—"} />
                                    <Field label="Fact / evidence anchor" value={item.fact_to_mention || "—"} />
                                    <Field
                                        label="Thread"
                                        value={
                                            [item.thread_subject, item.thread || item.last_snippet]
                                                .filter(Boolean)
                                                .join(" — ") || "—"
                                        }
                                    />
                                    {item.resume_at ? <Field label="Resume at" value={item.resume_at} /> : null}
                                    {!!item.evidence?.length && (
                                        <div>
                                            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500 mb-1">
                                                Evidence
                                            </div>
                                            <ul className="space-y-1 max-h-28 overflow-auto">
                                                {item.evidence.map((e) => (
                                                    <li
                                                        key={e.id}
                                                        className="rounded border border-slate-100 px-2 py-1 text-slate-700"
                                                    >
                                                        <span className="font-medium">{e.title || e.id}</span>
                                                        {e.excerpt ? (
                                                            <span className="text-slate-500"> · {e.excerpt}</span>
                                                        ) : null}
                                                    </li>
                                                ))}
                                            </ul>
                                        </div>
                                    )}
                                    <div className="flex flex-wrap gap-1.5 pt-1">
                                        <button
                                            type="button"
                                            disabled={
                                                generateReply.isPending ||
                                                item.do_not_contact ||
                                                item.blocked
                                            }
                                            onClick={() =>
                                                generateReply.mutate({ accountId: item.account_id })
                                            }
                                            className="h-7 px-2.5 inline-flex items-center gap-1 rounded-md border border-sky-200 bg-sky-50 text-sky-700 text-[12.5px] hover:bg-sky-100 disabled:opacity-50"
                                        >
                                            <MessageSquareText className="h-3.5 w-3.5" />
                                            Generate reply draft
                                        </button>
                                        {item.do_not_contact && (
                                            <span className="h-7 px-2 inline-flex items-center rounded-md bg-slate-900 text-white text-[11px]">
                                                Sticky DNC
                                            </span>
                                        )}
                                    </div>

                                    {/* Resume on date X — explicit future touch, still human-approved */}
                                    <div className="rounded-md border border-slate-100 p-2 space-y-1.5">
                                        <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                                            Resume on date (no auto-reopen)
                                        </div>
                                        <div className="flex flex-wrap gap-1.5 items-center">
                                            <input
                                                type="date"
                                                value={resumeAt}
                                                onChange={(e) => setResumeAt(e.target.value)}
                                                className="h-7 rounded-md border border-slate-200 px-2 text-[12.5px] focus:border-sky-400 focus:ring-1 focus:ring-sky-100 outline-none"
                                            />
                                            <input
                                                type="text"
                                                placeholder="Note (optional)"
                                                value={resumeNote}
                                                onChange={(e) => setResumeNote(e.target.value)}
                                                className="h-7 flex-1 min-w-[120px] rounded-md border border-slate-200 px-2 text-[12.5px] focus:border-sky-400 focus:ring-1 focus:ring-sky-100 outline-none"
                                            />
                                            <button
                                                type="button"
                                                disabled={
                                                    resume.isPending ||
                                                    !resumeAt ||
                                                    item.do_not_contact
                                                }
                                                onClick={() =>
                                                    resume.mutate({
                                                        accountId: item.account_id,
                                                        resumeAt,
                                                        note: resumeNote,
                                                    })
                                                }
                                                className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px] text-slate-700 hover:bg-slate-50 disabled:opacity-50"
                                            >
                                                Schedule resume draft
                                            </button>
                                        </div>
                                    </div>

                                    {/* Referral recipient swap — timeline retained */}
                                    <div className="rounded-md border border-slate-100 p-2 space-y-1.5">
                                        <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                                            Referral recipient (keep timeline)
                                        </div>
                                        <div className="grid sm:grid-cols-3 gap-1.5">
                                            <input
                                                type="text"
                                                placeholder="Name"
                                                value={refName}
                                                onChange={(e) => setRefName(e.target.value)}
                                                className="h-7 rounded-md border border-slate-200 px-2 text-[12.5px] focus:border-sky-400 focus:ring-1 focus:ring-sky-100 outline-none"
                                            />
                                            <input
                                                type="email"
                                                placeholder="Email"
                                                value={refEmail}
                                                onChange={(e) => setRefEmail(e.target.value)}
                                                className="h-7 rounded-md border border-slate-200 px-2 text-[12.5px] focus:border-sky-400 focus:ring-1 focus:ring-sky-100 outline-none"
                                            />
                                            <input
                                                type="text"
                                                placeholder="Role"
                                                value={refRole}
                                                onChange={(e) => setRefRole(e.target.value)}
                                                className="h-7 rounded-md border border-slate-200 px-2 text-[12.5px] focus:border-sky-400 focus:ring-1 focus:ring-sky-100 outline-none"
                                            />
                                        </div>
                                        <button
                                            type="button"
                                            disabled={
                                                referral.isPending ||
                                                item.do_not_contact ||
                                                (!refEmail.trim() && !refName.trim())
                                            }
                                            onClick={() =>
                                                referral.mutate({
                                                    accountId: item.account_id,
                                                    name: refName,
                                                    email: refEmail,
                                                    role: refRole,
                                                })
                                            }
                                            className="h-7 px-2.5 rounded-md border border-slate-200 text-[12.5px] text-slate-700 hover:bg-slate-50 disabled:opacity-50"
                                        >
                                            Update recipient
                                        </button>
                                    </div>

                                    <p className="text-[11px] text-slate-400">
                                        Reply/resume drafts land in Awaiting approval. AI never auto-sends or
                                        marks Ganho.
                                    </p>
                                </>
                            )}
                        </div>
                    </div>
                </section>

                <div className="grid md:grid-cols-2 gap-4">
                    <section className="rounded-md border border-slate-200 bg-white">
                        <div className="px-3 h-10 flex items-center border-b border-slate-200 text-[12.5px] font-medium text-slate-900">
                            Ready to generate ({ready.data?.length ?? 0})
                        </div>
                        <ul className="divide-y divide-slate-100 max-h-64 overflow-auto">
                            {(ready.data ?? []).map((a) => (
                                <li
                                    key={a.id}
                                    className="px-3 py-2 flex items-center justify-between gap-2 text-[12.5px]"
                                >
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
                                <li className="px-3 py-6 text-center text-slate-400 text-[12.5px]">
                                    No accounts ready
                                </li>
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
                                <li className="px-3 py-6 text-center text-slate-400 text-[12.5px]">
                                    None waiting
                                </li>
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
                                <li
                                    key={d.id}
                                    className="px-3 py-2 flex items-center justify-between gap-2 text-[12.5px]"
                                >
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

                {/* Human approval review (shared for initial + reply drafts) */}
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
                            No drafts waiting for review. Generate from ready accounts or reply cockpit first.
                        </div>
                    ) : (
                        <div className="p-3 grid md:grid-cols-2 gap-4">
                            <div className="space-y-2 text-[12.5px]">
                                <div>
                                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                                        Recipient
                                    </div>
                                    <div className="text-slate-900 font-medium">
                                        {current.recipient_name || "—"} · {current.recipient_role || "—"}
                                    </div>
                                    <div className="text-slate-600">{current.recipient_email}</div>
                                    <div className="text-slate-500">{current.verification_status}</div>
                                </div>
                                <div>
                                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                                        Fact
                                    </div>
                                    <div className="text-slate-800">{current.fact_used || "—"}</div>
                                </div>
                                <div>
                                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                                        Service
                                    </div>
                                    <div className="text-slate-800">{current.service_code}</div>
                                </div>
                                <div>
                                    <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                                        Provider
                                    </div>
                                    <div className="text-slate-800">
                                        {current.provider}/{current.model}
                                        {current.provider === "template" ? " (fallback, not AI)" : ""}
                                    </div>
                                </div>
                                {!!current.risk_flags?.length && (
                                    <div>
                                        <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">
                                            Flags
                                        </div>
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
                                    Human approves the exact content shown. Auto-send stays off.
                                </p>
                            </div>
                        </div>
                    )}
                </section>
            </div>
        </Page>
    );
}

function Field({ label, value }: { label: string; value: string }) {
    return (
        <div>
            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-500">{label}</div>
            <div className="text-slate-800 whitespace-pre-wrap">{value}</div>
        </div>
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
