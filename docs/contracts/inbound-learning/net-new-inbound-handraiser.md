# NET_NEW_INBOUND_HANDRAISER / CONFENGE_WEB intake (draft pin)

Test-only coordination contract for campaign 07. Not a production
fallback. Runtime without this version and hash fails closed.

See `docs/confenge/net-new-inbound-handraiser.md` and the published
fixture `internal/app/confenge/testdata/net_new_inbound_handraiser/conformance.json`.

Producers (web-cfg campaign 08) send this envelope on
`POST /api/v1/webhooks/confenge/inbound`. Downstream Meetcfg (campaign 14)
may consume sales-context items only after `outcome=ACCEPTED`.
