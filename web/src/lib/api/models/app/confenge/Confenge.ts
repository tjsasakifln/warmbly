export type ConfengeReadiness = {
    email: string;
    whatsapp: string;
    feed_configured: boolean;
    feed_age_seconds?: number | null;
    feed_age: string;
    feed_state?: "fresh" | "stale" | "missing";
    feed_snapshot_hash?: string;
    feed_last_success_at?: string | Date | null;
    feed_source_generated_at?: string | Date | null;
    feed_synced_at?: string | Date | null;
    feed_max_age_seconds?: number;
    outcome_loop: string;
    ai: string;
    governor_cap: number;
    campaign_daily_limit: number;
    effective_daily_cap: number;
    queue_count: number;
    kill_switch: boolean;
    sending_allowed: boolean;
    outreach_enabled: boolean;
    require_human_approval: boolean;
    auto_send_enabled: boolean;
    whatsapp_enabled: boolean;
    whatsapp_provider?: string;
    pilot_cohort_state?: "ready" | "unavailable";
    pilot_cohort_prepared?: number;
    pilot_cohort_needs_review?: number;
    pilot_cohort_approved?: number;
    pilot_cohort_sent?: number;
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
    next_best_action_at?: string | Date | null;
    activation_expires_at?: string | Date | null;
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
    next_best_action_at?: string | Date | null;
    activation_expires_at?: string | Date | null;
    contact_ready: boolean;
    context_stale: boolean;
    channel_readiness?: string;
};

export type ConfengeFeedSyncResult = {
    status: "completed" | "noop" | "failed" | "partial";
    snapshot_hash?: string;
    run_id?: string;
    chunks_total: number;
    chunks_imported: number;
    deactivations_applied: number;
    skipped_same_snapshot: boolean;
    errors?: string[];
    counts?: Record<string, number>;
};

export type ConfengePilotAccountResult = {
    account_id: string;
    cnpj14?: string;
    company?: string;
    status: "PREPARED" | "BLOCKED";
    reason_code?: string;
    human_readable_reason?: string;
    remediation?: string;
    previous_state?: string;
    intended_state: string;
    contact_state?: string;
    recipient?: string;
    recipient_name?: string;
    recipient_role?: string;
    contact_candidate_id?: string;
    touchpoint_id?: string;
    draft_id?: string;
    draft_state?: string;
    warnings?: string[];
    upstream_snapshot_hash?: string;
    message_context_hash?: string;
    prepared_at?: string | Date;
    idempotent: boolean;
};

export type ConfengePilotCohortResult = {
    cohort_id: string;
    target: number;
    selected: number;
    prepared: number;
    blocked: number;
    contact_needed: number;
    cohort_prepared: number;
    remaining: number;
    upstream_snapshot_hash?: string;
    feed_timestamp?: string | Date;
    results: ConfengePilotAccountResult[];
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
    evidence_date?: string | Date | null;
    consulted_at?: string | Date | null;
    reliability?: string;
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
  why_this_account?: string;
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
  draft_id?: string;
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
  recipient_mailbox_purpose?: string;
  recipient_generic?: boolean;
  draft?: ConfengeDraft;
  approved_by?: string;
  approved_at?: string | Date;
  generated_context_hash?: string;
  created_at?: string | Date;
  updated_at?: string | Date;
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
