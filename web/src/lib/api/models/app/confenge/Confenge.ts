export type ConfengeReadiness = {
    email: string;
    whatsapp: string;
    feed_configured: boolean;
    feed_age_seconds?: number | null;
    feed_age: string;
    outcome_loop: string;
    ai: string;
    governor_cap: number;
    queue_count: number;
    kill_switch: boolean;
    sending_allowed: boolean;
    outreach_enabled: boolean;
    require_human_approval: boolean;
    auto_send_enabled: boolean;
    whatsapp_enabled: boolean;
    whatsapp_provider?: string;
};

export type ConfengeStatus = {
    enabled: boolean;
    auto_send_enabled: boolean;
    require_human_approval: boolean;
    default_daily_limit: number;
    max_initial_email_words: number;
    feed_configured: boolean;
    kill_switch?: boolean;
    sending_allowed?: boolean;
    readiness?: ConfengeReadiness;
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
};
