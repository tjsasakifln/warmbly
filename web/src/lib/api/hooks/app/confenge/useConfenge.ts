import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import toast from "react-hot-toast";
import {
    approveConfengeTouchpoint,
    bootstrapConfengeCampaign,
    changeConfengeReferral,
    decideConfengeTouchpoint,
    dncConfengeAccount,
    editConfengeTouchpoint,
    enrollConfengeDraft,
    generateConfengeDraft,
    generateConfengeReplyDraft,
    generateConfengeTouchpoint,
    getConfengeAccount,
    getConfengeAttention,
    getConfengeDispatchStatus,
    getConfengeStatus,
    getConfengeSummary,
    listConfengeAccountTouchpoints,
    listConfengeAccounts,
    listConfengeAttention,
    listConfengeDrafts,
    listConfengeReviewTouchpoints,
    pauseConfengeDispatch,
    planConfengeCadence,
    queueConfengeTouchpoint,
    resumeConfengeAccount,
    resumeConfengeDispatch,
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

export function useConfengeDispatchStatus(enabled = true) {
    return useQuery({
        queryKey: [...KEY, "dispatch-status"],
        queryFn: getConfengeDispatchStatus,
        enabled,
        refetchInterval: 30_000,
    });
}

export function usePauseConfengeDispatch() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (reason?: string) => pauseConfengeDispatch(reason),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Dispatch paused");
        },
        onError: (e: Error) => toast.error(e.message || "Pause failed"),
    });
}

export function useResumeConfengeDispatch() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: () => resumeConfengeDispatch(),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Dispatch resumed");
        },
        onError: (e: Error) => toast.error(e.message || "Resume failed"),
    });
}

export function useConfengeReviewTouchpoints(enabled = true) {
    return useQuery({
        queryKey: [...KEY, "touchpoints", "review"],
        queryFn: listConfengeReviewTouchpoints,
        enabled,
        staleTime: 5000,
    });
}

export function useConfengeAccountTimeline(accountId: string | null) {
    return useQuery({
        queryKey: [...KEY, "timeline", accountId],
        queryFn: () => listConfengeAccountTouchpoints(accountId!),
        enabled: !!accountId,
    });
}

export function usePlanConfengeCadence() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ accountId, channel }: { accountId: string; channel?: string }) =>
            planConfengeCadence(accountId, channel),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Cadence planned");
        },
        onError: (e: Error) => toast.error(e.message || "Plan failed"),
    });
}

export function useGenerateConfengeTouchpoint() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => generateConfengeTouchpoint(id),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Generated");
        },
        onError: (e: Error) => toast.error(e.message || "Generate failed"),
    });
}

export function useApproveAndQueueTouchpoint() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: async (args: {
            id: string;
            subject?: string;
            body_text?: string;
            recipient?: string;
        }) => {
            await editConfengeTouchpoint(args.id, {
                subject: args.subject,
                body_text: args.body_text,
                recipient: args.recipient,
            });
            await approveConfengeTouchpoint(args.id);
            return queueConfengeTouchpoint(args.id);
        },
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Approved and queued");
        },
        onError: (e: Error) => toast.error(e.message || "Approve & queue failed"),
    });
}

export function useSkipConfengeTouchpoint() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => decideConfengeTouchpoint(id, "skip"),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Skipped");
        },
        onError: (e: Error) => toast.error(e.message || "Skip failed"),
    });
}

export function useDncConfengeAccount() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (accountId: string) => dncConfengeAccount(accountId),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("DNC");
        },
        onError: (e: Error) => toast.error(e.message || "DNC failed"),
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
