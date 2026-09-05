# Fragment: net-new inbound readback endpoint

target_path: docs/content/docs/api/endpoints.mdx
operation: add under CONFENGE inbound routes
stable_key: GET /confenge/inbound/handraisers/:logicalId
depends_on: REV-03 consumer
test: go test ./internal/api/handler -run TestConfengeInboundWebhookNetNewHandraiserRoutesAndReadback
rollback: remove the GET route in internal/api/routes.go and this handler

Authenticated `GET /confenge/inbound/handraisers/:logicalId` returns the
persisted receipt for one logical ID: `acknowledged_by`, `acknowledged_at`,
`policy_version`, `hash`, `receipt`, `reason`, `outcome`. Permission:
`view_contacts` / `APIPermReadContacts`.
