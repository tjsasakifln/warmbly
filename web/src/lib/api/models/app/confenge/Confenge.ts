export type ConfengeStatus = {
    enabled: boolean;
    auto_send_enabled: boolean;
    require_human_approval: boolean;
    default_daily_limit: number;
    max_initial_email_words: number;
    feed_configured: boolean;
};

export type ConfengeSummary = {
    needs_contact: number;
    ready_to_generate: number;
    needs_review: number;
    approved: number;
    enrolled: number;
    sent: number;
    replied: number;
    meeting: number;
    proposal: number;
    won: number;
    blocked: number;
    bounced: number;
    do_not_contact: number;
    total: number;
};

export type ConfengeAccount = {
    id: string;
    organization_id: string;
    source_lead_id: string;
    cnpj14: string;
    cnpj_root: string;
    razao_social: string;
    nome_fantasia: string;
    municipio: string;
    uf: string;
    website: string;
    priority_rank: number;
    priority_score: number;
    priority_tier: string;
    moment_code: string;
    moment_summary: string;
    service_code: string;
    service_name: string;
    entry_offer: string;
    fact_to_mention: string;
    question_to_ask: string;
    cta: string;
    commercial_state: string;
    queue_state: string;
    blocked: boolean;
    do_not_contact: boolean;
    contacts?: ConfengeContact[];
    evidence?: ConfengeEvidence[];
};

export type ConfengeContact = {
    id: string;
    name: string;
    role: string;
    email: string;
    verification_status: string;
    recommended: boolean;
    do_not_contact: boolean;
    bounced: boolean;
};

export type ConfengeEvidence = {
    id: string;
    source_evidence_id: string;
    title: string;
    url: string;
    excerpt: string;
    synthesis: string;
    epistemic_class: string;
};

export type ConfengeDraft = {
    id: string;
    account_id: string;
    recipient_name: string;
    recipient_role: string;
    recipient_email: string;
    verification_status: string;
    subject: string;
    body_text: string;
    service_code: string;
    fact_used: string;
    evidence_ids?: string[];
    question: string;
    cta: string;
    provider: string;
    model: string;
    risk_class: string;
    risk_flags?: string[];
    status: string;
    human_edited: boolean;
    validation_ok?: boolean;
    channel?: string;
    strategy_code?: string;
};

export type ConfengeAttentionFilter =
    | "needs_attention"
    | "awaiting_approval"
    | "scheduled"
    | "sent"
    | "replied"
    | "dnc";

export type ConfengeEvidenceBrief = {
    id: string;
    title?: string;
    excerpt?: string;
    epistemic_class?: string;
    url?: string;
};

export type ConfengeAttentionItem = {
    account_id: string;
    company_name: string;
    cnpj14: string;
    contact_name?: string;
    contact_email?: string;
    contact_phone?: string;
    channel?: string;
    service_code?: string;
    service_name?: string;
    fact_to_mention?: string;
    queue_state: string;
    commercial_state: string;
    do_not_contact: boolean;
    blocked: boolean;
    intent?: string;
    confidence?: number;
    suggested_action?: string;
    evidence?: ConfengeEvidenceBrief[];
    last_snippet?: string;
    thread_subject?: string;
    thread?: string;
    updated_at?: string;
    reply_draft_id?: string;
    resume_at?: string;
};
