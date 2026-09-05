# NET_NEW_INBOUND_HANDRAISER / CONFENGE_WEB intake (REV-03)

Test-only coordination contract for REV-03. Not a production fallback.
Runtime without a matching REV-02 `contract_id + version + content hash`
fails closed. `RuntimeRev02Pin` stays empty until a final REV-02 SHA is
recorded.

See `docs/confenge/net-new-inbound-handraiser.md` and the published
fixture `internal/app/confenge/testdata/net_new_inbound_handraiser/conformance.json`.

Producers send this envelope on `POST /api/v1/webhooks/confenge/inbound`.
Downstream Meetcfg may consume sales-context items only after
`outcome=ACCEPTED`.
