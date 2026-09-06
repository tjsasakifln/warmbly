# MV-03 Warmbly integration fragment

`CONFENGE_MV_CAMPAIGN=03`

Dependency: Governance PR
[`#172`](https://github.com/tjsasakifln/Governance/pull/172), commit
`0074722ce66f16af06dd4799ee88064ea8a12fc1`, policy hash
`sha256:405ac86064a90641b843352d21cd21703744115de9592558e100671d92276df7`.

The web producer may send the official Governance request field names plus
`protected_contact` inside the authenticated HMAC body. Warmbly keeps legacy
B2G aliases compatible, admits email-only or WhatsApp/phone-only contact,
scrubs protected PII from the raw receipt payload/readback and creates only an
inbound manual action. The official source is `{"system":"web-cfg"}` and the
distinct producer origin is `CONFENGE_WEB`; legacy string source remains
compatible.

Integration assertions:

- `technical_triage_review` and `technical_triage_v1` are admitted alongside
  the donor private-readiness values;
- `other_technical_need` returns `NEEDS_CONTEXT`;
- known nuclei with `UNKNOWN`/`NOT_SCREENED` return
  `CONFLICT_CHECK_REQUIRED`;
- `HIT`/`DECLINE` are safely rejected;
- retry is exactly-once and conflicting reuse is rejected;
- accepted POST/readback keep the same logical ID and receipt;
- producer readback is HMAC-authenticated at
  `GET /api/v1/webhooks/confenge/inbound/handraisers/:logicalId`; sign the
  bytes `GET\n/api/v1/webhooks/confenge/inbound/handraisers/<logicalId>`;
- `outbound_eligible=false`, `auto_send=false`,
  `dispatch_attempted=false`; no SMTP is invoked;
- analytics and readback contain no protected contact PII.
- optional protected organization is retained operationally but excluded from
  raw payload/readback; `PHONE` never creates WhatsApp consent.

Rollback: stop submissions or revert this consumer commit; retain receipts and
do not promote inbound identities to outbound.
