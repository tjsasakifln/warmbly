const STATE_LABELS: Record<string, string> = {
  NEEDS_CONTACT: "Precisa de contato",
  READY_TO_GENERATE: "Pronta para gerar",
  NEEDS_REVIEW: "Precisa de revisão",
  APPROVED: "Aprovada",
  ENROLLED: "Incluída na campanha",
  SENT: "Enviada",
  REPLIED: "Respondida",
  MEETING: "Reunião",
  PROPOSAL: "Proposta",
  WON: "Ganha",
  LOST: "Perdida",
  BLOCKED: "Bloqueada",
  BOUNCED: "E-mail devolvido",
  DO_NOT_CONTACT: "Não contatar",
  SKIPPED: "Ignorada",
  TARGET_FIT_SUPPRESSED: "Fora do perfil",
  NOT_GENERATED: "Não gerada",
  GENERATING: "Gerando",
  REJECTED: "Rejeitada",
  PLANNED: "Planejada",
  DUE: "Pronta para executar",
  DRAFTED: "Rascunho criado",
  QUEUED: "Na fila de envio",
  DNC: "Não contatar",
  CANCELLED: "Cancelada",
  FAILED: "Falhou",
  READY: "Pronto",
  NEEDS_ENRICHMENT: "Precisa de enriquecimento",
  NOT_READY: "Não está pronto",
  BLOCKED_BY_POLICY: "Bloqueado pela política",
  NOT_CONFIGURED: "Não configurado",
  FALLBACK_TEMPLATE: "Modelo alternativo",
};

const PURPOSE_LABELS: Record<string, string> = {
  INITIAL: "Contato inicial",
  FOLLOW_UP: "Acompanhamento",
  CLOSE: "Encerramento",
};

const CHANNEL_LABELS: Record<string, string> = {
  EMAIL: "E-mail",
  WHATSAPP: "WhatsApp",
};

const INTENT_LABELS: Record<string, string> = {
  POSITIVE_INTEREST: "Interesse positivo",
  REFERRAL_TO_OTHER_PERSON: "Encaminhamento para outra pessoa",
  QUESTION: "Pergunta",
  OBJECTION: "Objeção",
  NOT_NOW: "Retomar mais tarde",
  NEGATIVE: "Sem interesse",
  DO_NOT_CONTACT: "Não contatar",
  OUT_OF_OFFICE: "Ausência temporária",
  UNKNOWN: "Intenção ainda não classificada",
  NEW: "Nova resposta",
};

const REASON_LABELS: Record<string, string> = {
  factual_error: "Erro factual",
  too_generic: "Muito genérica",
  too_salesy: "Comercial demais",
  wrong_offer: "Oferta incorreta",
  unsupported_claim: "Afirmação sem evidência",
  creepy: "Abordagem invasiva",
  too_long: "Muito longa",
  tone: "Tom inadequado",
  other: "Outro motivo",
  manual_pause: "Pausa manual",
  empty_followup: "Acompanhamento sem conteúdo útil",
  banned_phrase: "Expressão não permitida",
  meeting_default_cta: "Pedido de reunião cedo demais",
  multiple_ctas: "Mais de uma chamada para ação",
  fake_re_fwd: "Assunto simula resposta ou encaminhamento",
  missing_why_now: "Falta explicar por que agir agora",
  offer_cannot_be_fulfilled: "Oferta não pode ser cumprida",
  evidence_weak: "Evidência insuficiente",
  missing_contract_event: "Existe contrato público, mas nenhum evento contratual específico sustenta ainda uma primeira abordagem",
  metadata_dump: "O fato público chegou como metadado, não como gancho comercial",
  no_safe_hook: "Não há fato público específico o bastante para um primeiro contato",
  missing_value_unit: "Não há unidade de valor concreta para oferecer",
  unfulfillable_cta: "O convite promete um conteúdo que o dossiê ainda não sustenta",
  reasoning_leak: "Raciocínio interno apareceu na mensagem",
  vocab_mismatch: "Vocabulário incompatível com o serviço",
  messageability_needs_enrichment: "Ainda não dá para um primeiro contato digno",
  messageability_blocked: "Abordagem bloqueada",
  composer_version_stale: "Rascunho anterior à correção de messageability",
  hypothesis_as_fact: "Hipótese apresentada como fato",
  short_email: "Mensagem curta demais",
  long_email: "Mensagem longa demais",
  generic_opening: "Abertura genérica",
  WINDOW_OPEN: "Janela de ação aberta",
  NEW_AMENDMENT_OR_TERM: "Novo aditivo ou encerramento",
  NEW_RELEVANT_CONTRACT: "Novo contrato relevante",
  generic_mailbox_allowed_by_policy: "Caixa genérica: confirme destinatário e saudação antes de aprovar",
  recipient_conflict: "Mais de um destinatário válido no snapshot atual",
  recipient_removed_current_snapshot: "Contato removido do snapshot atual",
  recipient_changed_requires_review: "A cadência existente aponta para outro destinatário",
  recipient_snapshot_missing: "Snapshot autoritativo do contato ausente",
  account_not_in_current_snapshot: "Conta ausente do snapshot atual",
  cohort_membership_conflict: "Conta já preparada com outra mensagem ou destinatário",
  cohort_membership_failed: "Dependências da preparação ficaram incoerentes",
};

export function stateLabel(value?: string | null): string {
  if (!value) return "Não informado";
  return STATE_LABELS[value.toUpperCase()] ?? "Estado não reconhecido";
}

export function purposeLabel(value?: string | null): string {
  if (!value) return "Etapa";
  return PURPOSE_LABELS[value.toUpperCase()] ?? "Etapa não reconhecida";
}

export function channelLabel(value?: string | null): string {
  if (!value) return "Canal não informado";
  return CHANNEL_LABELS[value.toUpperCase()] ?? "Canal não reconhecido";
}

export function intentLabel(value?: string | null): string {
  if (!value) return "Intenção não informada";
  return INTENT_LABELS[value.toUpperCase()] ?? "Intenção não reconhecida";
}

export function reasonLabel(value?: string | null): string {
  if (!value) return "Não informado";
  return REASON_LABELS[value] ?? `Bloqueio operacional (${value})`;
}

export function formatPtBrDate(value: string | Date | null | undefined): string | null {
  if (!value) return null;
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return new Intl.DateTimeFormat("pt-BR", { dateStyle: "short" }).format(date);
}

export function formatFeedAge(seconds?: number | null): string {
  if (seconds === null || seconds === undefined) return "horário desconhecido";
  if (seconds < 60) return "agora";
  if (seconds < 3600) return `há ${Math.floor(seconds / 60)} min`;
  if (seconds < 86400) return `há ${Math.floor(seconds / 3600)} h`;
  const days = Math.floor(seconds / 86400);
  return `há ${days} ${days === 1 ? "dia" : "dias"}`;
}
