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
    /** Ordering priority from extra-cli (not purchase probability). */
    activation_state?: string;
    activation_score?: number;
    activation_reason_codes?: string[];
    activation_policy_version?: string;
    next_best_action_at?: string | null;
    activation_expires_at?: string | null;
    message_context_hash?: string;
    context_stale?: boolean;
    contacts?: ConfengeContact[];
    evidence?: ConfengeEvidence[];
};

export type ConfengeWorkingQueueSummary = {
    reservoir_monitored: number;
    actionable_now: number;
    needs_contact: number;
    needs_review: number;
    approved_scheduled: number;
    watch_awaiting: number;
    suppressed: number;
    stale_context: number;
    due_next_24h: number;
    theoretical_slots_24h: number;
    capacity_load: number;
    dynamic_priority_enabled: boolean;
    last_feed_sync_at?: string | null;
    feed_age_seconds?: number | null;
};

export type ConfengeWorkingQueueItem = {
    account: ConfengeAccount;
    lane: string;
    why_now?: string;
    reason_codes?: string[];
    activation_score?: number;
    next_best_action_at?: string | null;
    activation_expires_at?: string | null;
    contact_ready: boolean;
    context_stale: boolean;
    channel_readiness?: string;
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


export type ConfengeStrategyExplain = {
  why_now?: string;
  fact_used?: string;
  hypothesis?: string;
  service?: string;
  offer?: string;
  recipient?: string;
  sources?: string[];
  touch?: string;
  experiment?: string;
  doctrine_version?: string;
};

export type ConfengeTouchpoint = {
  id: string;
  organization_id: string;
  account_id: string;
  ordinal: number;
  channel: string;
  purpose: string;
  due_at: string;
  state: string;
  recipient: string;
  subject: string;
  body_text: string;
  content_hash: string;
  approved_content_hash: string;
  service_code: string;
  fact_used: string;
  evidence_ids?: string[];
  account?: ConfengeAccount;
  strategy_explain?: ConfengeStrategyExplain;
  doctrine_alerts?: string[];
  draft?: ConfengeDraft;
};

export type ConfengeDispatchFailure = {
    id: string;
    organization_id?: string;
    channel: string;
    message_key: string;
    draft_id?: string;
    error_text: string;
    occurred_at: string;
};

export type ConfengeDispatchStatus = {
    sent_last_hour: number;
    cap: number;
    min_gap_seconds: number;
    next_slot_at?: string;
    queued_approved: number;
    paused: boolean;
    pause_reason?: string;
    in_send_window: boolean;
    timezone: string;
    window_start: string;
    window_end: string;
    active_leases: number;
    recent_failures?: ConfengeDispatchFailure[];
};
