import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import toast from "react-hot-toast";
import {
    bootstrapConfengeCampaign,
    enrollConfengeDraft,
    generateConfengeDraft,
    getConfengeAccount,
    getConfengeDispatchStatus,
    getConfengeStatus,
    getConfengeSummary,
    listConfengeAccounts,
    listConfengeDrafts,
    pauseConfengeDispatch,
    resumeConfengeDispatch,
    reviewConfengeDraft,
} from "@/lib/api/client/app/confenge/confenge";

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
            qc.invalidateQueries({ queryKey: KEY });
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
            qc.invalidateQueries({ queryKey: KEY });
            toast.success("Dispatch resumed");
        },
        onError: (e: Error) => toast.error(e.message || "Resume failed"),
    });
}
