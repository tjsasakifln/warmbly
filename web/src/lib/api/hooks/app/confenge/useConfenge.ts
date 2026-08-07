import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import toast from "react-hot-toast";
import {
    bootstrapConfengeCampaign,
    changeConfengeReferral,
    enrollConfengeDraft,
    generateConfengeDraft,
    generateConfengeReplyDraft,
    getConfengeAccount,
    getConfengeAttention,
    getConfengeStatus,
    getConfengeSummary,
    listConfengeAccounts,
    listConfengeAttention,
    listConfengeDrafts,
    resumeConfengeAccount,
    reviewConfengeDraft,
} from "@/lib/api/client/app/confenge/confenge";
import type { ConfengeAttentionFilter } from "@/lib/api/models/app/confenge/Confenge";

const KEY = ["confenge"] as const;

export function useConfengeStatus() {
    return useQuery({
        queryKey: [...KEY, "status"],
        queryFn: getConfengeStatus,
        staleTime: 60_000,
    });
}

export function useConfengeSummary(enabled = true) {
    return useQuery({
        queryKey: [...KEY, "summary"],
        queryFn: getConfengeSummary,
        enabled,
        staleTime: 15_000,
    });
}

export function useConfengeAccounts(queueState?: string, enabled = true) {
    return useQuery({
        queryKey: [...KEY, "accounts", queueState ?? ""],
        queryFn: () => listConfengeAccounts({ queue_state: queueState, limit: 100 }),
        enabled,
    });
}

export function useConfengeAccount(id: string | null) {
    return useQuery({
        queryKey: [...KEY, "account", id],
        queryFn: () => getConfengeAccount(id!),
        enabled: !!id,
    });
}

export function useConfengeDrafts(status?: string, enabled = true) {
    return useQuery({
        queryKey: [...KEY, "drafts", status ?? ""],
        queryFn: () => listConfengeDrafts(status),
        enabled,
    });
}

export function useGenerateConfengeDraft() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ accountId, contactId }: { accountId: string; contactId?: string }) =>
            generateConfengeDraft(accountId, contactId),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Draft generated");
        },
        onError: (e: Error) => toast.error(e.message || "Generate failed"),
    });
}

export function useReviewConfengeDraft() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (args: {
            id: string;
            action: "approve" | "reject" | "skip" | "edit" | "block";
            subject?: string;
            body_text?: string;
            reason?: string;
            do_not_contact?: boolean;
        }) => reviewConfengeDraft(args.id, args),
        onSuccess: (_d, vars) => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success(`Draft ${vars.action}d`);
        },
        onError: (e: Error) => toast.error(e.message || "Review failed"),
    });
}

export function useEnrollConfengeDraft() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => enrollConfengeDraft(id),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Enrolled in CONFENGE campaign");
        },
        onError: (e: Error) => toast.error(e.message || "Enroll failed"),
    });
}

export function useBootstrapConfengeCampaign() {
    return useMutation({
        mutationFn: () => bootstrapConfengeCampaign(),
        onSuccess: (c) => toast.success(`Campaign ready: ${c.name}`),
        onError: (e: Error) => toast.error(e.message || "Bootstrap failed"),
    });
}

export function useConfengeAttention(filter: ConfengeAttentionFilter | string, enabled = true) {
    return useQuery({
        queryKey: [...KEY, "attention", filter],
        queryFn: () => listConfengeAttention(filter, 100),
        enabled,
    });
}

export function useConfengeAttentionDetail(id: string | null) {
    return useQuery({
        queryKey: [...KEY, "attention-detail", id],
        queryFn: () => getConfengeAttention(id!),
        enabled: !!id,
    });
}

export function useGenerateConfengeReplyDraft() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ accountId, contactId }: { accountId: string; contactId?: string }) =>
            generateConfengeReplyDraft(accountId, contactId),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Reply draft ready for human review (never auto-send)");
        },
        onError: (e: Error) => toast.error(e.message || "Generate reply failed"),
    });
}

export function useResumeConfengeAccount() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ accountId, resumeAt, note }: { accountId: string; resumeAt: string; note?: string }) =>
            resumeConfengeAccount(accountId, resumeAt, note),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Resume date recorded (no auto-reopen)");
        },
        onError: (e: Error) => toast.error(e.message || "Resume failed"),
    });
}

export function useChangeConfengeReferral() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (args: {
            accountId: string;
            name?: string;
            email?: string;
            role?: string;
            phone?: string;
        }) => changeConfengeReferral(args.accountId, args),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Referral recipient updated");
        },
        onError: (e: Error) => toast.error(e.message || "Referral update failed"),
    });
}
