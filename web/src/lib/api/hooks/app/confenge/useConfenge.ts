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
    applyConfengeManualAction,
    getConfengeCockpit,
    getConfengeExecutiveIntel,
    getConfengeTruthScoreboard,
    listConfengeHumanEnvelopes,
    recordConfengeHumanOutcome,
    getConfengeIntelException,
    listConfengeIntelExceptions,
    resolveConfengeIntelException,
    getConfengeToday,
    recordConfengeActionOutcome,
    recordConfengeInboundOutcome,
    acknowledgeConfengeInboundAlert,
    resolveConfengeInboundNoAction,
    startConfengeAction,
    getConfengeWorkingOverview,
    listConfengeAccountTouchpoints,
    listConfengeAccounts,
    listConfengeAttention,
    listConfengeWorkingQueue,
    listConfengeDrafts,
    listConfengeReviewTouchpoints,
    pauseConfengeDispatch,
    planConfengeCadence,
    prepareConfengePilotCohort,
    resumeConfengeAccount,
    resumeConfengeDispatch,
    reviewConfengeDraft,
    syncConfengeFeed,
} from "@/lib/api/client/app/confenge/confenge";
import type { ConfengeAttentionFilter, ConfengeIntelExceptionFilter } from "@/lib/api/models/app/confenge/Confenge";
import type { AppError } from "@/lib/api/client/normalizeError";

const KEY = ["confenge"] as const;

function confengeError(error: unknown, fallback: string): string {
    const appError = error as AppError;
    const detail = appError?.message && appError.message !== "Unexpected error occurred." ? appError.message : fallback;
    return appError?.request_id ? `${detail} Código de atendimento: ${appError.request_id}` : detail;
}

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

export function useConfengeWorkingOverview(enabled = true) {
    return useQuery({
        queryKey: [...KEY, "working-overview"],
        queryFn: getConfengeWorkingOverview,
        enabled,
        staleTime: 15_000,
    });
}

export function useConfengeExecutiveIntel(enabled = true) {
    return useQuery({
        queryKey: [...KEY, "intel", "executive"],
        queryFn: () => getConfengeExecutiveIntel({ includeSynthetic: false }),
        enabled,
        staleTime: 15_000,
    });
}

export function useConfengeTruthScoreboard(enabled = true) {
    return useQuery({
        queryKey: [...KEY, "intel", "scoreboard"],
        queryFn: () => getConfengeTruthScoreboard({ includeSynthetic: false }),
        enabled,
        staleTime: 15_000,
    });
}

export function useConfengeHumanEnvelopes(enabled = true) {
    return useQuery({
        queryKey: [...KEY, "intel", "human-envelopes"],
        queryFn: listConfengeHumanEnvelopes,
        enabled,
        staleTime: 60_000,
    });
}

export function useRecordConfengeHumanOutcome() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: recordConfengeHumanOutcome,
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: [...KEY, "intel"] });
            toast.success("Registro humano gravado");
        },
        onError: (error) => toast.error(confengeError(error, "Nao foi possivel gravar o registro humano")),
    });
}

export function useConfengeIntelExceptions(filter: ConfengeIntelExceptionFilter, enabled = true) {
    return useQuery({
        queryKey: [...KEY, "intel", "exceptions", filter],
        queryFn: () => listConfengeIntelExceptions(filter),
        enabled,
        staleTime: 10_000,
    });
}

export function useConfengeIntelException(id: string | null) {
    return useQuery({
        queryKey: [...KEY, "intel", "exceptions", "detail", id],
        queryFn: () => getConfengeIntelException(id!),
        enabled: !!id,
    });
}

export function useResolveConfengeIntelException() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (args: {
            id: string;
            action: "link" | "defer" | "reject" | "mark_external_evidence_required";
            reason: string;
            link_identity?: string;
        }) => resolveConfengeIntelException(args.id, args),
        onSuccess: (res) => {
            void qc.invalidateQueries({ queryKey: KEY });
            if (res.replay) {
                toast.success("Ação já registrada. Replay sem nova mudança.");
                return;
            }
            toast.success("Exceção atualizada. Sem inventar WON, LOST, receita ou identidade.");
        },
        onError: (e) => toast.error(confengeError(e, "A resolução foi recusada. A exceção permanece aberta.")),
    });
}

export function useConfengeCockpit(enabled = true) {
    return useQuery({
        queryKey: [...KEY, "cockpit"],
        queryFn: getConfengeCockpit,
        enabled,
        staleTime: 10_000,
    });
}

export function useConfengeToday(enabled = true) {
    return useQuery({
        queryKey: [...KEY, "today"],
        queryFn: getConfengeToday,
        enabled,
        staleTime: 10_000,
    });
}

export function useStartConfengeAction() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (actionId: string) => startConfengeAction(actionId),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: [...KEY, "cockpit"] });
            qc.invalidateQueries({ queryKey: [...KEY, "today"] });
        },
    });
}

export function useRecordConfengeActionOutcome() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({
            actionId,
            outcome_code,
            notes,
            referral_name,
            referral_role,
            next_action_type,
            next_action_at,
        }: {
            actionId: string;
            outcome_code: string;
            notes?: string;
            referral_name?: string;
            referral_role?: string;
            next_action_type?: string;
            next_action_at?: string;
        }) => recordConfengeActionOutcome(actionId, { outcome_code, notes, referral_name, referral_role, next_action_type, next_action_at }),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: [...KEY, "cockpit"] });
            qc.invalidateQueries({ queryKey: [...KEY, "today"] });
            toast.success("Outcome registrado");
        },
        onError: (error) => toast.error(confengeError(error, "Nao foi possivel registrar o outcome")),
    });
}

export function useRecordConfengeInboundOutcome() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({
            leadId,
            outcome_code,
            notes,
            next_action_type,
            next_action_at,
        }: {
            leadId: string;
            outcome_code: string;
            notes?: string;
            next_action_type?: string;
            next_action_at?: string;
        }) => recordConfengeInboundOutcome(leadId, { outcome_code, notes, next_action_type, next_action_at }),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: [...KEY, "cockpit"] });
            qc.invalidateQueries({ queryKey: [...KEY, "today"] });
            toast.success("Outcome inbound registrado");
        },
        onError: (error) => toast.error(confengeError(error, "Nao foi possivel registrar o outcome inbound")),
    });
}

export function useAcknowledgeConfengeInboundAlert() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (leadId: string) => acknowledgeConfengeInboundAlert(leadId),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: [...KEY, "cockpit"] });
            toast.success("Lead reconhecido. Sem contato automático.");
        },
        onError: (error) => toast.error(confengeError(error, "Nao foi possivel reconhecer o lead")),
    });
}

export function useResolveConfengeInboundNoAction() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ leadId, reason }: { leadId: string; reason: string }) =>
            resolveConfengeInboundNoAction(leadId, reason),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: [...KEY, "cockpit"] });
            toast.success("Resolvido sem ação. Motivo registrado.");
        },
        onError: (error) => toast.error(confengeError(error, "Nao foi possivel resolver sem ação")),
    });
}

export function useApplyConfengeManualAction() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ accountId, action, reason }: { accountId: string; action: string; reason?: string }) =>
            applyConfengeManualAction(accountId, action, reason),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: [...KEY, "cockpit"] });
        },
    });
}

export function useConfengeWorkingQueue(lane?: string, enabled = true) {
    return useQuery({
        queryKey: [...KEY, "working-queue", lane ?? ""],
        queryFn: () => listConfengeWorkingQueue({ lane, limit: 50 }),
        enabled,
        staleTime: 10_000,
    });
}

export function useSyncConfengeFeed() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: async () => {
            const result = await syncConfengeFeed();
            const [summary, overview] = await Promise.all([
                getConfengeSummary(),
                getConfengeWorkingOverview(),
            ]);
            return { result, summary, overview };
        },
        onSuccess: ({ result, summary, overview }) => {
            void qc.invalidateQueries({ queryKey: KEY });
            const eligible = summary.ready_to_generate + overview.actionable_now;
            if (eligible === 0) {
                toast.error("Base sincronizada, mas nenhuma conta está elegível para o cohort. O feed precisa incluir target-fit válido e prontidão de envio.", { duration: 8_000 });
                return;
            }
            if (result.skipped_same_snapshot) {
                toast.success(`Base atualizada: ${eligible} contas elegíveis para o cohort`);
                return;
            }
            toast.success(`Base atualizada: ${eligible} contas elegíveis; ${result.chunks_imported}/${result.chunks_total} lotes importados`);
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível atualizar a base comercial.")),
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
            toast.success("Rascunho gerado");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível gerar o rascunho.")),
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
            const actions = { approve: "aprovado", reject: "rejeitado", skip: "ignorado", edit: "editado", block: "bloqueado" };
            toast.success(`Rascunho ${actions[vars.action]}`);
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível concluir a revisão.")),
    });
}

export function useEnrollConfengeDraft() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => enrollConfengeDraft(id),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Mensagem incluída na campanha CONFENGE");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível incluir a mensagem na campanha.")),
    });
}

export function useBootstrapConfengeCampaign() {
    return useMutation({
        mutationFn: () => bootstrapConfengeCampaign(),
        onSuccess: (c) => toast.success(`Campanha pronta: ${c.name}`),
        onError: (e) => toast.error(confengeError(e, "Não foi possível preparar a campanha.")),
    });
}

export function useConfengeDispatchStatus(enabled = true) {
    return useQuery({
        queryKey: [...KEY, "dispatch-status"],
        queryFn: getConfengeDispatchStatus,
        enabled,
		staleTime: 10_000,
    });
}

export function usePauseConfengeDispatch() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (reason?: string) => pauseConfengeDispatch(reason),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Envios pausados");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível pausar os envios.")),
    });
}

export function useResumeConfengeDispatch() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: () => resumeConfengeDispatch(),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Envios retomados");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível retomar os envios.")),
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
            toast.success("Cadência planejada");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível planejar a cadência.")),
    });
}

export function usePrepareConfengePilotCohort() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (accountIds: string[]) => prepareConfengePilotCohort(accountIds),
        onSuccess: (result) => {
            void qc.invalidateQueries({ queryKey: KEY });
            if (result.blocked > 0) {
                toast.error(`${result.selected} selecionadas: ${result.prepared} preparadas e ${result.blocked} bloqueadas`, { duration: 8_000 });
                return;
            }
            toast.success(`${result.prepared} mensagens da coorte foram preparadas para revisão`);
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível iniciar a preparação da coorte.")),
    });
}

export function useGenerateConfengeTouchpoint() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => generateConfengeTouchpoint(id),
        onSuccess: (tp) => {
            void qc.invalidateQueries({ queryKey: KEY });
            const err = tp.generation_error?.trim();
            if (err || !tp.body_text?.trim()) {
                toast.error(err || "A mensagem não ficou autorizável. Veja o motivo no card.");
                return;
            }
            toast.success("Mensagem gerada");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível gerar a mensagem.")),
    });
}

export function useApproveConfengeTouchpoint() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: async (args: {
            id: string;
            subject?: string;
            body_text?: string;
            recipient?: string;
            generic_recipient_acknowledged?: boolean;
        }) => {
            await editConfengeTouchpoint(args.id, {
                subject: args.subject,
                body_text: args.body_text,
                recipient: args.recipient,
            });
			return approveConfengeTouchpoint(args.id, args.generic_recipient_acknowledged ?? false);
        },
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Mensagem aprovada. O envio continua pausado até a etapa de dispatch.");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível aprovar a mensagem.")),
    });
}

export function useSkipConfengeTouchpoint() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => decideConfengeTouchpoint(id, "skip"),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Etapa ignorada");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível ignorar a etapa.")),
    });
}

export function useRejectConfengeTouchpoint() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ id, reason }: { id: string; reason: string }) =>
            decideConfengeTouchpoint(id, "reject", reason),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Mensagem rejeitada");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível rejeitar a mensagem.")),
    });
}

export function useDncConfengeAccount() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (accountId: string) => dncConfengeAccount(accountId),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Conta marcada como não contatar");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível marcar a conta como não contatar.")),
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
            toast.success("Rascunho de resposta pronto para revisão humana");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível gerar o rascunho de resposta.")),
    });
}

export function useResumeConfengeAccount() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ accountId, resumeAt, note }: { accountId: string; resumeAt: string; note?: string }) =>
            resumeConfengeAccount(accountId, resumeAt, note),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: KEY });
            toast.success("Data de retomada registrada");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível registrar a retomada.")),
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
            toast.success("Destinatário indicado atualizado");
        },
        onError: (e) => toast.error(confengeError(e, "Não foi possível atualizar o destinatário indicado.")),
    });
}
