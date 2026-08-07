import type {
    ConfengeAccount,
    ConfengeAttentionFilter,
    ConfengeAttentionItem,
    ConfengeDispatchStatus,
    ConfengeDraft,
    ConfengeStatus,
    ConfengeSummary,
    ConfengeTouchpoint,
} from "@/lib/api/models/app/confenge/Confenge";
import Request from "../../Request";

export async function getConfengeStatus(): Promise<ConfengeStatus> {
    return await Request<ConfengeStatus>({
        method: "GET",
        url: "/confenge/status",
        authorization: true,
    });
}

export async function getConfengeSummary(): Promise<ConfengeSummary> {
    const res = await Request<{ data: ConfengeSummary }>({
        method: "GET",
        url: "/confenge/summary",
        authorization: true,
    });
    return res.data;
}

export async function listConfengeAccounts(params?: {
    queue_state?: string;
    q?: string;
    limit?: number;
}): Promise<ConfengeAccount[]> {
    const sp = new URLSearchParams();
    if (params?.queue_state) sp.set("queue_state", params.queue_state);
    if (params?.q) sp.set("q", params.q);
    if (params?.limit) sp.set("limit", String(params.limit));
    const qs = sp.toString();
    const res = await Request<{ data: ConfengeAccount[] }>({
        method: "GET",
        url: `/confenge/accounts${qs ? `?${qs}` : ""}`,
        authorization: true,
    });
    return res.data ?? [];
}

export async function getConfengeAccount(id: string): Promise<ConfengeAccount> {
    const res = await Request<{ data: ConfengeAccount }>({
        method: "GET",
        url: `/confenge/accounts/${id}`,
        authorization: true,
    });
    return res.data;
}

export async function generateConfengeDraft(accountId: string, contactCandidateId?: string): Promise<ConfengeDraft> {
    const res = await Request<{ data: ConfengeDraft }>({
        method: "POST",
        url: `/confenge/accounts/${accountId}/generate`,
        authorization: true,
        data: contactCandidateId ? { contact_candidate_id: contactCandidateId } : {},
    });
    return res.data;
}

export async function listConfengeDrafts(status?: string): Promise<ConfengeDraft[]> {
    const qs = status ? `?status=${encodeURIComponent(status)}` : "";
    const res = await Request<{ data: ConfengeDraft[] }>({
        method: "GET",
        url: `/confenge/drafts${qs}`,
        authorization: true,
    });
    return res.data ?? [];
}

export async function reviewConfengeDraft(
    id: string,
    body: {
        action: "approve" | "reject" | "skip" | "edit" | "block";
        subject?: string;
        body_text?: string;
        reason?: string;
        do_not_contact?: boolean;
    },
): Promise<ConfengeDraft> {
    const res = await Request<{ data: ConfengeDraft }>({
        method: "POST",
        url: `/confenge/drafts/${id}/review`,
        authorization: true,
        data: body,
    });
    return res.data;
}

export async function enrollConfengeDraft(id: string): Promise<ConfengeDraft> {
    const res = await Request<{ data: ConfengeDraft }>({
        method: "POST",
        url: `/confenge/drafts/${id}/enroll`,
        authorization: true,
        data: {},
    });
    return res.data;
}

export async function bootstrapConfengeCampaign(): Promise<{ id: string; name: string }> {
    const res = await Request<{ data: { id: string; name: string } }>({
        method: "POST",
        url: "/confenge/campaign/bootstrap",
        authorization: true,
        data: {},
    });
    return res.data;
}

export async function listConfengeReviewTouchpoints(): Promise<ConfengeTouchpoint[]> {
  const res = await Request<{ data: ConfengeTouchpoint[] }>({ method: "GET", url: "/confenge/touchpoints/review?limit=100", authorization: true });
  return res.data ?? [];
}

export async function planConfengeCadence(accountId: string, channel?: string): Promise<ConfengeTouchpoint[]> {
  const res = await Request<{ data: ConfengeTouchpoint[] }>({ method: "POST", url: `/confenge/accounts/${accountId}/plan`, authorization: true, data: { channel } });
  return res.data ?? [];
}

export async function generateConfengeTouchpoint(id: string): Promise<ConfengeTouchpoint> {
  const res = await Request<{ data: ConfengeTouchpoint }>({ method: "POST", url: `/confenge/touchpoints/${id}/generate`, authorization: true, data: {} });
  return res.data;
}

export async function editConfengeTouchpoint(id: string, body: { subject?: string; body_text?: string; recipient?: string }): Promise<ConfengeTouchpoint> {
  const res = await Request<{ data: ConfengeTouchpoint }>({ method: "POST", url: `/confenge/touchpoints/${id}/edit`, authorization: true, data: body });
  return res.data;
}

export async function approveConfengeTouchpoint(id: string): Promise<ConfengeTouchpoint> {
  const res = await Request<{ data: ConfengeTouchpoint }>({ method: "POST", url: `/confenge/touchpoints/${id}/approve`, authorization: true, data: {} });
  return res.data;
}

export async function queueConfengeTouchpoint(id: string): Promise<ConfengeTouchpoint> {
  const res = await Request<{ data: ConfengeTouchpoint }>({ method: "POST", url: `/confenge/touchpoints/${id}/queue`, authorization: true, data: {} });
  return res.data;
}

export async function decideConfengeTouchpoint(id: string, action: "skip" | "reject"): Promise<ConfengeTouchpoint> {
  const res = await Request<{ data: ConfengeTouchpoint }>({ method: "POST", url: `/confenge/touchpoints/${id}/decision`, authorization: true, data: { action } });
  return res.data;
}

export async function dncConfengeAccount(accountId: string): Promise<{ cancelled: number }> {
  const res = await Request<{ data: { cancelled: number } }>({ method: "POST", url: `/confenge/accounts/${accountId}/dnc`, authorization: true, data: {} });
  return res.data;
}

export async function listConfengeAccountTouchpoints(accountId: string): Promise<ConfengeTouchpoint[]> {
  const res = await Request<{ data: ConfengeTouchpoint[] }>({ method: "GET", url: `/confenge/accounts/${accountId}/touchpoints`, authorization: true });
  return res.data ?? [];
}

export async function getConfengeDispatchStatus(): Promise<ConfengeDispatchStatus> {
    const res = await Request<{ data: ConfengeDispatchStatus }>({
        method: "GET",
        url: "/confenge/dispatch/status",
        authorization: true,
    });
    return res.data;
}

export async function pauseConfengeDispatch(reason?: string): Promise<ConfengeDispatchStatus> {
    const res = await Request<{ data: ConfengeDispatchStatus }>({
        method: "POST",
        url: "/confenge/dispatch/pause",
        authorization: true,
        data: { reason: reason ?? "manual_pause" },
    });
    return res.data;
}

export async function resumeConfengeDispatch(): Promise<ConfengeDispatchStatus> {
    const res = await Request<{ data: ConfengeDispatchStatus }>({
        method: "POST",
        url: "/confenge/dispatch/resume",
        authorization: true,
        data: {},
    });
    return res.data;
}

export async function listConfengeAttention(
    filter: ConfengeAttentionFilter | string = "needs_attention",
    limit = 50,
): Promise<ConfengeAttentionItem[]> {
    const sp = new URLSearchParams();
    if (filter) sp.set("filter", filter);
    if (limit) sp.set("limit", String(limit));
    const qs = sp.toString();
    const res = await Request<{ data: ConfengeAttentionItem[] }>({
        method: "GET",
        url: `/confenge/attention${qs ? `?${qs}` : ""}`,
        authorization: true,
    });
    return res.data ?? [];
}

export async function getConfengeAttention(id: string): Promise<ConfengeAttentionItem> {
    const res = await Request<{ data: ConfengeAttentionItem }>({
        method: "GET",
        url: `/confenge/attention/${id}`,
        authorization: true,
    });
    return res.data;
}

export async function generateConfengeReplyDraft(
    accountId: string,
    contactCandidateId?: string,
): Promise<ConfengeDraft> {
    const res = await Request<{ data: ConfengeDraft }>({
        method: "POST",
        url: `/confenge/accounts/${accountId}/generate-reply`,
        authorization: true,
        data: contactCandidateId ? { contact_candidate_id: contactCandidateId } : {},
    });
    return res.data;
}

export async function resumeConfengeAccount(
    accountId: string,
    resumeAt: string,
    note?: string,
): Promise<ConfengeAccount> {
    const res = await Request<{ data: ConfengeAccount }>({
        method: "POST",
        url: `/confenge/accounts/${accountId}/resume`,
        authorization: true,
        data: { resume_at: resumeAt, note: note ?? "" },
    });
    return res.data;
}

export async function changeConfengeReferral(
    accountId: string,
    body: { name?: string; email?: string; role?: string; phone?: string },
): Promise<ConfengeAccount> {
    const res = await Request<{ data: ConfengeAccount }>({
        method: "POST",
        url: `/confenge/accounts/${accountId}/referral`,
        authorization: true,
        data: body,
    });
    return res.data;
}
