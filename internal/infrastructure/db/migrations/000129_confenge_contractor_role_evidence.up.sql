INSERT INTO outreach_evidence (
    id,
    organization_id,
    account_id,
    source_evidence_id,
    evidence_type,
    title,
    document,
    evidence_date,
    excerpt,
    synthesis,
    epistemic_class,
    reliability,
    consulted_at,
    last_import_run_id,
    created_at,
    updated_at
)
SELECT
    gen_random_uuid(),
    account.organization_id,
    account.id,
    trim(evidence.id),
    'CONTRACTOR_ROLE_ATTESTATION',
    'Papel de contratada confirmado pelo extra-cli',
    left(account.contractor_role_evidence_reference, 500),
    account.contractor_role_observed_at::date,
    left(
        'extra-cli confirmou o CNPJ ' || account.supplier_cnpj14 ||
        ' como CONTRATADA/FORNECEDORA, distinto do CNPJ contratante ' || account.buyer_cnpj14 || '.',
        4000
    ),
    left(
        'extra-cli confirmou o CNPJ ' || account.supplier_cnpj14 ||
        ' como CONTRATADA/FORNECEDORA, distinto do CNPJ contratante ' || account.buyer_cnpj14 || '.',
        2000
    ),
    'CONFIRMED_FACT',
    'HIGH',
    account.contractor_role_observed_at::date,
    account.last_import_run_id,
    now(),
    now()
FROM outreach_accounts account
CROSS JOIN LATERAL jsonb_array_elements_text(
    CASE
        WHEN jsonb_typeof(account.contractor_role_evidence_ids) = 'array'
            THEN account.contractor_role_evidence_ids
        ELSE '[]'::jsonb
    END
) AS evidence(id)
WHERE account.contractor_role_status = 'CONTRACTOR_ROLE_CONFIRMED'
  AND account.target_party_role = 'SUPPLIER'
  AND account.contractor_role_policy_version = 'contract-party-role.v1'
  AND account.contractor_role_source = 'extra-cli:v_contracts_canonical_v2'
  AND account.contractor_role_source_run_id = account.source_run_id
  AND account.contractor_role_observed_at IS NOT NULL
  AND account.contractor_role_evidence_hash ~ '^[0-9a-f]{64}$'
  AND account.contractor_role_evidence_reference =
      'extra-cli:v_contracts_canonical_v2:sha256:' || account.contractor_role_evidence_hash
  AND account.supplier_cnpj14 = account.cnpj14
  AND account.supplier_identity_ref = 'cnpj:' || account.supplier_cnpj14
  AND account.buyer_identity_ref = 'cnpj:' || account.buyer_cnpj14
  AND account.supplier_cnpj14 ~ '^[0-9]{14}$'
  AND account.buyer_cnpj14 ~ '^[0-9]{14}$'
  AND left(account.supplier_cnpj14, 8) <> left(account.buyer_cnpj14, 8)
  AND account.contractor_role_match_method = 'SUPPLIER_EXACT_CNPJ14'
  AND account.contractor_role_confidence = 'HIGH'
  AND account.contractor_role_reason_codes @> '["lead_matches_supplier","lead_differs_from_buyer"]'::jsonb
  AND account.last_import_run_id IS NOT NULL
  AND length(trim(evidence.id)) BETWEEN 1 AND 200
ON CONFLICT (organization_id, account_id, source_evidence_id) DO NOTHING;
