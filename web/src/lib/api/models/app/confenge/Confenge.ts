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
    inbound?: string;
    inbound_secret_configured?: boolean;
    inbound_org_configured?: boolean;
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

export type ConfengeContactFunnel = {
    imported: number;
    tier_a: number;
    tier_b: number;
    tier_c: number;
    tier_d: number;
    blocked_exhausted: number;
    messageability_ready: number;
    needs_review: number;
    manual_outreach_ready: number;
    approved: number;
    contacted: number;
    replied: number;
    meeting: number;
    actionable?: number;
    action_planned?: number;
    touched?: number;
    target_reached?: number;
    conversation?: number;
    interested?: number;
};

export type ConfengeManualItem = {
    company: string;
    person?: string;
    role?: string;
    contact_tier: string;
    lane: string;
    channel?: string;
    channel_value?: string;
    mailbox_label?: string;
    source?: string;
    service?: string;
    why_now?: string;
    factual_hook?: string;
    recommended_action?: string;
    suggested_text?: string;
    confidence?: string;
    blocking_warning?: string;
    actions: string[];
    canonical_target_id?: string;
};

export type ConfengeActionCopy = {
    kind?: string;
    subject?: string;
    body?: string;
    cta?: string;
    opening?: string;
    reason_for_call?: string;
    value_proposition?: string;
    ask?: string;
    objection_notes?: string;
    do_not_claim?: string[];
    person_id?: string;
    mailbox_label?: string;
};

export type ConfengeActionCard = {
    action_id: string;
    account_id?: string;
    company: string;
    person?: string;
    person_id?: string;
    role?: string;
    target_role?: string;
    why_now?: string;
    offer?: string;
    recommended_action?: string;
    channel?: string;
    channel_value?: string;
    next_action_at?: string;
    route_type?: string;
    route_relation?: string;
    reachability_class?: string;
    confidence?: string;
    factual_hook?: string;
    evidence?: string[];
    warnings?: string[];
    copy: ConfengeActionCopy;
    last_outcome?: string;
    next_action?: string;
    route_epistemology?: string;
    mailbox_label?: string;
    action_type: string;
    lane: string;
    state: string;
    actionable: boolean;
    email_sendable: boolean;
    dispatchable: boolean;
    parent_action_id?: string;
    followup_action_id?: string;
    stale_warning?: string;
};

export type ConfengeTodaySummary = {
    calls: number;
    routed_calls: number;
    emails_to_review: number;
    inferred_emails: number;
    role_emails: number;
    whatsapp: number;
    professional_social: number;
    contact_forms: number;
    low_confidence: number;
    total: number;
};

export type ConfengeToday = {
    summary: ConfengeTodaySummary;
    actions: ConfengeActionCard[];
};

export type ConfengeActionMetrics = {
    actions_planned: number;
    actions_executed: number;
    by_action_type?: Record<string, number>;
    by_reachability_class?: Record<string, number>;
    target_reached_rate?: number | null;
    conversation_rate?: number | null;
    interest_rate?: number | null;
    meeting_rate?: number | null;
    wrong_person: number;
    wrong_channel: number;
    referrals: number;
    dnc: number;
    bounce: number;
};

export type ConfengeInboundLatency = {
    lead_created_at: string;
    warmbly_ingested_at: string;
    enrichment_completed_at?: string;
    owner_assigned_at?: string;
    first_action_at?: string;
    conversation_at?: string;
    proposal_at?: string;
    close_at?: string;
};

export type ConfengeInboundNowItem = {
    lead_id: string;
    receipt_id: string;
    company: string;
    person?: string;
    origin: string;
    asset?: string;
    query?: string;
    cta?: string;
    trigger?: string;
    offer?: string;
    entity_id?: string;
    person_id?: string;
    correlation_id?: string;
    contract_context?: string;
    why_now: string;
    recommended_action: string;
    channel?: string;
    reachability?: string;
    freshness?: string;
    confidence?: string;
    evidence?: string[];
    owner: string;
    lead_age_seconds: number;
    lead_age: string;
    status: string;
    next_action: string;
    action_id?: string;
    account_id?: string;
    email_sendable: boolean;
    dispatchable: boolean;
    enrichment_status: string;
    warnings?: string[];
    suggested_copy?: string;
    suggested_copy_route?: string;
    suggested_copy_review?: string;
    latency: ConfengeInboundLatency;
    alert_id?: string;
    alert_state?: string;
    alert_band?: string;
    synthetic?: boolean;
    acknowledged_at?: string;
    acknowledged_by?: string;
    received_ago?: string;
    alert_failure_code?: string;
    first_action_type?: string;
    resolution_reason?: string;
};

export type ConfengeExecutiveFamily = {
    route_family: string;
    inbound_qualified_pipeline: number;
    qco: number;
    conversations: number;
    meetings: number;
    proposals: number;
    pipeline: number;
    won: number;
    lost: number;
    unknown: number;
};

export type ConfengeIntelEvidence = {
    kind: string;
    key: string;
    value: string;
};

export type ConfengeIntelQueueEvent = {
    at: string;
    kind: string;
    actor?: string;
    action?: string;
    reason?: string;
    detail?: string;
};

export type ConfengeIntelResolution = {
    action: string;
    actor: string;
    reason: string;
    at: string;
    before_status: string;
    after_status: string;
    link_identity?: string;
};

export type ConfengeIntelException = {
    id: string;
    organization_id?: string;
    code: string;
    reason: string;
    next_action: string;
    identity?: string;
    action_id?: string;
    outcome_id?: string;
    account_id?: string;
    lead_id?: string;
    held: boolean;
    synthetic?: boolean;
    at: string;
    lane?: string;
    source?: string;
    severity?: string;
    status?: string;
    age_seconds: number;
    evidence?: ConfengeIntelEvidence[];
    history?: ConfengeIntelQueueEvent[];
    allowed_actions?: string[];
    resolution?: ConfengeIntelResolution;
    linked_identity?: string;
};

export type ConfengeIntelResolveResult = {
    exception: ConfengeIntelException;
    replay: boolean;
    refused: boolean;
    reason?: string;
    before: { id: string; status: string; code: string; held: boolean; next_action: string };
    after: { id: string; status: string; code: string; held: boolean; next_action: string };
    actor?: string;
    action?: string;
};

export type ConfengeIntelExceptionFilter = {
    type?: string;
    lane?: string;
    source?: string;
    severity?: string;
    status?: string;
    ageMinSeconds?: number;
};

export type ConfengeExecutiveView = {
    month: string;
    include_synthetic: boolean;
    inbound_qualified_pipeline: number;
    qco: number;
    conversations: number;
    meetings: number;
    proposals: number;
    pipeline: number;
    won: number;
    lost: number;
    unknown: number;
    families: ConfengeExecutiveFamily[];
    denominators: {
        leads: number;
        actions: number;
        outcomes: number;
        qualified: number;
        conversations: number;
        meetings: number;
        proposals: number;
        closed: number;
    };
    latency: {
        sampled_chains: number;
        baseline: string;
        lead_to_ingest_ms?: number;
        ingest_to_enrichment_ms?: number;
        enrichment_to_action_ms?: number;
    };
    freshness?: {
        stale_chains?: number;
        missing_version_chains?: number;
    };
    attribution_kind: string;
    causal_proof: boolean;
    real_empty: boolean;
    chain_count: number;
    commercial?: {
        offer_viewed?: number;
        checkout_created?: number;
        payment_pending?: number;
        payment_received?: number;
        onboarding?: number;
        service_active?: number;
        payment_overdue?: number;
        payment_refunded?: number;
        canceled?: number;
        qualified_pipeline?: number;
        received_revenue_cents?: number;
        contracted_revenue_cents?: number;
        mrr_cents?: number;
        exception_count?: number;
    };
    by_offer_version?: Array<{
        offer_id: string;
        offer_version: string;
        selected: number;
        checkout_created: number;
        payment_pending: number;
        payment_received: number;
        onboarding_started: number;
        service_active: number;
        overdue: number;
        refund: number;
        cancel: number;
        qualified_pipeline: number;
        received_revenue_cents: number;
        contracted_revenue_cents: number;
        mrr_cents: number;
        exceptions: number;
        unknown: number;
        onboarding_blocked: number;
        onboarding_eligible: number;
        denominator_chains: number;
        stage_timestamps?: {
            eligibility_submitted_at?: string;
            capacity_decision_at?: string;
            terms_accepted_at?: string;
            checkout_created_at?: string;
            payment_received_at?: string;
            onboarding_started_at?: string;
            service_activated_at?: string;
        };
    }>;
};

export type ConfengeScoreboardStage = {
    id: string;
    label: string;
    order: number;
    status: "TRUE" | "FALSE" | "UNKNOWN" | "BLOCKED";
    source_of_truth: string;
    snapshot_at: string;
    freshness: string;
    numerator?: number | null;
    denominator?: number | null;
    synthetic_included: boolean;
    owner: string;
    next_action: string;
    latency: string;
    observation: string;
};

export type ConfengeScoreboardMetric = {
    id: string;
    label: string;
    status: string;
    value_cents: number;
    count?: number;
    source_of_truth: string;
    observation: string;
};

export type ConfengeScoreboard = {
    schema_version: string;
    generated_at: string;
    include_synthetic: boolean;
    production_path: string;
    human_blocker?: string;
    next_real_event: string;
    causal_proof: boolean;
    auto_send_enabled: boolean;
    dispatch_attempted: boolean;
    stages: ConfengeScoreboardStage[];
    separate_metrics: ConfengeScoreboardMetric[];
};

export type ConfengeHumanEnvelope = {
    slot: string;
    lead_id: string;
    account_id: string;
    idempotency_key: string;
    status: string;
    invented_ids: boolean;
    next_action: string;
};

export type ConfengeHumanOutcomeEntry = {
    envelope_id?: string;
    idempotency_key?: string;
    lead_id?: string;
    account_id?: string;
    action: string;
    reached?: boolean;
    route_valid?: boolean;
    reply?: boolean;
    meeting_scheduled?: boolean;
    meeting_held?: boolean;
    follow_up_at?: string;
    disqualified?: boolean;
    proposal_emitted?: boolean;
    outcome_state?: string;
    human_confirmed?: boolean;
    evidence_ref?: string;
    revenue_document_id?: string;
    revenue_cents?: number;
    notes?: string;
};

export type ConfengeCockpit = {
    funnel: ConfengeContactFunnel;
    manual: ConfengeManualItem[];
    needs_review: ConfengeManualItem[];
    today?: ConfengeToday;
    metrics?: ConfengeActionMetrics;
    inbound_now?: ConfengeInboundNowItem[];
    unacknowledged_real?: number;
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

export type ConfengeDelegatedFirstTouchItem = {
    batch_id: string;
    account_id?: string;
    cnpj14: string;
    supplier_cnpj14: string;
    buyer_cnpj14?: string;
    recipient?: string;
    route_class: string;
    decision: "DELEGATED_POLICY_APPROVE" | "HOLD";
    approval_source: "DELEGATED_POLICY_APPROVE" | "POLICY_EVALUATION_HOLD";
    state: string;
    evidence_reference?: string;
    evidence_hash?: string;
    source_run_id: string;
    source_snapshot_hash: string;
    reason_codes?: string[];
    blocker_codes?: string[];
    content_hash?: string;
    runtime_release_sha?: string;
    due_at?: string | null;
    readback_at?: string | null;
    decided_at: string;
};

export type ConfengeDelegatedFirstTouchStatus = {
    batch_id?: string;
    policy_id: string;
    policy_version: string;
    policy_hash: string;
    policy_active: boolean;
    counts: Record<string, number>;
    human_approved: number;
    queued_readback: number;
    duplicate_live_account: number;
    duplicate_live_root: number;
    items: ConfengeDelegatedFirstTouchItem[];
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
  messageability?: string;
  messageability_reason?: string;
};

export type ConfengeConsultantSendability = {
  company?: string;
  person?: string;
  why_this_person?: string;
  email?: string;
  email_evidence?: string;
  derivation?: string;
  verification_status?: string;
  epistemic_class?: string;
  freshness?: string;
  suppression?: string;
  service_code?: string;
  supporting_fact?: string;
  subject?: string;
  body?: string;
  warnings?: string[];
  hard_gates?: Record<string, boolean>;
  send_without_editing?: string;
  recipient_state?: string;
  messageability?: string;
  lane?: string;
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
  recipient_state?: string;
  recipient_reason?: string;
  consultant_sendability?: ConfengeConsultantSendability;
  generation_error?: string;
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
    email_account_id?: string;
    task_id?: string;
    channel: string;
    message_key: string;
    draft_id?: string;
    error_code?: string;
    error_class: string;
    error_text: string;
    occurred_at: string;
};

export type ConfengeMailboxCapacity = {
    email_account_id: string;
    email: string;
    enabled: boolean;
    status: string;
    provider: string;
    credentials_ready: boolean;
    worker_assigned: boolean;
    auth_state: string;
    auth_spf: boolean;
    auth_dkim: boolean;
    auth_dmarc: boolean;
    auth_dmarc_policy?: string;
    auth_checked_at?: string;
    mailbox_age_days: number;
    warmup_started_at?: string;
    warmup_age_days?: number;
    warmup_days_observed: number;
    cold_ramp_started_at?: string;
    configured_daily_cap: number;
    configured_min_wait_seconds: number;
    derived_hourly_cap: number;
    effective_daily_cap: number;
    effective_hourly_cap: number;
    provider_daily_cap?: number;
    provider_hourly_cap?: number;
    provider_cap_source: string;
    business_window: {
        timezone: string;
        start: string;
        end: string;
        business_days_only: boolean;
    };
    observed_throughput: {
        accepted_last_hour: number;
        accepted_today: number;
        accepted_last_7d: number;
    };
    latest: {
        attempt_at?: string;
        accepted_at?: string;
        bounce_at?: string;
        complaint_at?: string;
        reply_at?: string;
        provider_rejection_at?: string;
        provider_error_class?: string;
    };
    pause_source?: string;
    health: string;
    health_reason: string;
    health_signals?: string[];
    unknown?: string[];
    used_today: number;
    next_eligible_slot?: string;
};

export type ConfengeCapacityAlert = {
    code: string;
    severity: string;
    email_account_id?: string;
    count?: number;
    occurred_at?: string;
    reason: string;
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
    pause_source?: string;
    capacity_source: string;
    mailboxes: ConfengeMailboxCapacity[];
    forecast: {
        slots_next_24h: number;
        slots_next_7d: number;
        potential_slots_next_24h: number;
        potential_slots_next_7d: number;
        estimated_days_to_drain?: number;
        delivery_promised: boolean;
    };
    alerts?: ConfengeCapacityAlert[];
};
