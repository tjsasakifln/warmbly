import type {
    ConfengeAccount,
    ConfengeDraft,
    ConfengeStatus,
    ConfengeSummary,
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
