package confenge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// confenge-dossier/1.0 vocabulary. Only manifest.json crosses into Warmbly.
const (
	DossierSchemaV1       = "confenge-dossier/1.0"
	DossierPublicSchemaV1 = "public-read-confenge-dossier/1.0"

	DossierDataReady  = "DATA_READY"
	DossierDataHold   = "DATA_HOLD"
	DossierDataReject = "DATA_REJECT"

	DossierCatalogFixture      = "fixture"
	DossierCatalogOfficialLive = "official_live"
)

var (
	ErrDossierStoreUnavailable    = errors.New("dossier reference store unavailable")
	ErrDossierPrivateBody         = errors.New("dossier body or prospect identity must never be stored in warmbly")
	ErrDossierManifestInvalid     = errors.New("dossier manifest is invalid")
	ErrDossierNotDeliverable      = errors.New("dossier reference is not deliverable")
	ErrDossierHumanActor          = errors.New("dossier delivery requires a human actor")
	ErrDossierReferenceMissing    = errors.New("dossier reference not found")
	ErrDossierDeliveryNotInferred = errors.New("delivery is a human act and cannot be attached pre-delivered")
)

// dossierForbiddenKeys are the confenge-dossier/1.0 body sections and the
// identity fields the contract keeps out of every non-private destination.
// Seeing any of them means the caller handed over dossier.json, not manifest.json.
var dossierForbiddenKeys = []string{
	"identity", "buyer_map", "competitors", "price_panel",
	"expiring_contracts", "open_opportunities", "findings",
	"declared_limitations", "sections", "body", "markdown", "document",
	"cnpj14", "cnpj_raiz", "razao_social", "nome_fantasia",
	"supplier_cnpj", "supplier_nome", "fornecedor_cnpj", "fornecedor_nome",
	"municipio",
}

// Every persisted scalar is format- and length-pinned. Without this the columns
// are free-form text and a caller can put the whole dossier, and the prospect
// identity, inside dossier_id or as_of: the forbidden-key scan only inspects
// key names, never values.
var (
	dossierIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	dossierHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	dossierAsOfPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	dossierSHAPattern  = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	dossierURIPattern  = regexp.MustCompile(`^[A-Za-z0-9_./:-]{1,512}$`)
	dossierCNPJPattern = regexp.MustCompile(`\d{2}[.\s]?\d{3}[.\s]?\d{3}[/.\s]?\d{4}[-.\s]?\d{2}`)
)

// DossierDeliveryNoteMax bounds the one field a human types by hand.
const DossierDeliveryNoteMax = 2000

// DossierManifest is the manifest.json projection. It carries hashes and state,
// never a dossier body and never the prospect identity.
type DossierManifest struct {
	DossierID         string `json:"dossier_id"`
	Schema            string `json:"schema"`
	CatalogMode       string `json:"catalog_mode"`
	DataState         string `json:"data_state"`
	AsOf              string `json:"as_of"`
	ContentHash       string `json:"content_hash"`
	PublicContentHash string `json:"public_content_hash"`
	ProducerSHA       string `json:"producer_sha"`
}

// DossierReference is the only dossier state Warmbly holds: a manifest pointer
// bound to an account, plus the human delivery outcome an operator records.
type DossierReference struct {
	ID                   uuid.UUID  `json:"id"`
	OrganizationID       uuid.UUID  `json:"organization_id"`
	AccountID            uuid.UUID  `json:"account_id"`
	CommercialActionID   *uuid.UUID `json:"commercial_action_id,omitempty"`
	TouchpointID         *uuid.UUID `json:"touchpoint_id,omitempty"`
	DossierID            string     `json:"dossier_id"`
	Schema               string     `json:"schema"`
	CatalogMode          string     `json:"catalog_mode"`
	DataState            string     `json:"data_state"`
	AsOf                 string     `json:"as_of,omitempty"`
	ContentHash          string     `json:"content_hash"`
	PublicContentHash    string     `json:"public_content_hash,omitempty"`
	ProducerSHA          string     `json:"producer_sha,omitempty"`
	ArtifactURI          string     `json:"artifact_uri,omitempty"`
	Deliverable          bool       `json:"deliverable"`
	NotDeliverableReason string     `json:"not_deliverable_reason,omitempty"`
	AttachedBy           uuid.UUID  `json:"attached_by"`
	AttachedAt           time.Time  `json:"attached_at"`
	DeliveredAt          *time.Time `json:"delivered_at,omitempty"`
	DeliveredBy          *uuid.UUID `json:"delivered_by,omitempty"`
	DeliveryNote         string     `json:"delivery_note,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// Delivered reports the recorded human handoff. Nothing else implies delivery.
func (r *DossierReference) Delivered() bool {
	return r != nil && r.DeliveredAt != nil && !r.DeliveredAt.IsZero()
}

// DossierReferenceStore persists manifest references. It has no send surface.
type DossierReferenceStore interface {
	PutDossierReference(ctx context.Context, ref *DossierReference) error
	GetDossierReference(ctx context.Context, orgID, id uuid.UUID) (*DossierReference, error)
	ListDossierReferences(ctx context.Context, orgID, accountID uuid.UUID, limit int) ([]DossierReference, error)
	ListDossierReferencesForAccounts(ctx context.Context, orgID uuid.UUID, accountIDs []uuid.UUID) ([]DossierReference, error)
	SetDossierReferenceDelivered(ctx context.Context, ref *DossierReference) error
}

// ParseDossierManifest accepts manifest.json only. A dossier.json, or any
// payload carrying a body section or an identity field, is refused outright.
func ParseDossierManifest(raw []byte) (DossierManifest, error) {
	var m DossierManifest
	if len(raw) == 0 {
		return m, fmt.Errorf("%w: empty payload", ErrDossierManifestInvalid)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return m, fmt.Errorf("%w: %v", ErrDossierManifestInvalid, err)
	}
	if key := findForbiddenDossierKey(doc, 0); key != "" {
		return m, fmt.Errorf("%w: %s", ErrDossierPrivateBody, key)
	}
	m = DossierManifest{
		DossierID:         dossierString(doc, "dossier_id"),
		Schema:            dossierString(doc, "schema"),
		CatalogMode:       strings.ToLower(dossierString(doc, "catalog_mode")),
		DataState:         strings.ToUpper(dossierString(doc, "data_state")),
		AsOf:              dossierString(doc, "as_of"),
		ContentHash:       dossierString(doc, "content_hash"),
		PublicContentHash: dossierString(doc, "public_content_hash"),
		ProducerSHA:       dossierString(doc, "producer_sha"),
	}
	if err := ValidateDossierManifest(m); err != nil {
		return DossierManifest{}, err
	}
	return m, nil
}

// ValidateDossierManifest pins the schema and the closed vocabularies.
func ValidateDossierManifest(m DossierManifest) error {
	if !dossierIDPattern.MatchString(strings.TrimSpace(m.DossierID)) {
		return fmt.Errorf("%w: dossier_id must match %s", ErrDossierManifestInvalid, dossierIDPattern)
	}
	if !dossierHashPattern.MatchString(strings.TrimSpace(m.ContentHash)) {
		return fmt.Errorf("%w: content_hash must be sha256:<64 hex>", ErrDossierManifestInvalid)
	}
	if v := strings.TrimSpace(m.PublicContentHash); v != "" && !dossierHashPattern.MatchString(v) {
		return fmt.Errorf("%w: public_content_hash must be sha256:<64 hex>", ErrDossierManifestInvalid)
	}
	if v := strings.TrimSpace(m.AsOf); v != "" && !dossierAsOfPattern.MatchString(v) {
		return fmt.Errorf("%w: as_of must be YYYY-MM-DD", ErrDossierManifestInvalid)
	}
	if v := strings.TrimSpace(m.ProducerSHA); v != "" && !dossierSHAPattern.MatchString(v) {
		return fmt.Errorf("%w: producer_sha must be lowercase hex", ErrDossierManifestInvalid)
	}
	if s := strings.TrimSpace(m.Schema); s != "" && s != DossierSchemaV1 {
		return fmt.Errorf("%w: unsupported schema %q", ErrDossierManifestInvalid, s)
	}
	switch m.CatalogMode {
	case DossierCatalogFixture, DossierCatalogOfficialLive:
	default:
		return fmt.Errorf("%w: unknown catalog_mode %q", ErrDossierManifestInvalid, m.CatalogMode)
	}
	switch m.DataState {
	case DossierDataReady, DossierDataHold, DossierDataReject:
	default:
		return fmt.Errorf("%w: unknown data_state %q", ErrDossierManifestInvalid, m.DataState)
	}
	return nil
}

// findForbiddenDossierValue looks for prospect identity inside free prose, where
// a key-name scan cannot reach. A CNPJ or a razao_social pasted into the note is
// the same leak as one stored in a column.
func findForbiddenDossierValue(text string) string {
	if text == "" {
		return ""
	}
	if dossierCNPJPattern.MatchString(text) {
		return "cnpj"
	}
	lowered := strings.ToLower(text)
	for _, key := range dossierForbiddenKeys {
		if strings.Contains(lowered, key+":") || strings.Contains(lowered, key+"=") {
			return key
		}
	}
	return ""
}

// DossierDeliverability is derived from the manifest, never asserted by a caller.
// A fixture run can never be deliverable, per the producer's honesty rules.
func DossierDeliverability(catalogMode, dataState string) (bool, string) {
	mode := strings.ToLower(strings.TrimSpace(catalogMode))
	state := strings.ToUpper(strings.TrimSpace(dataState))
	if mode != DossierCatalogOfficialLive {
		return false, "catalog_mode=" + mode + ": execucao fixture nao e entregavel a cliente"
	}
	if state != DossierDataReady {
		return false, "data_state=" + state + ": somente DATA_READY pode ser entregue"
	}
	return true, ""
}

// NormalizeDossierReference validates the reference and derives deliverability.
// It refuses a pre-stamped delivery: delivery is recorded, never inferred.
func NormalizeDossierReference(ref *DossierReference, now time.Time) error {
	if ref == nil {
		return ErrDossierReferenceMissing
	}
	if ref.DeliveredAt != nil || ref.DeliveredBy != nil {
		return ErrDossierDeliveryNotInferred
	}
	if ref.OrganizationID == uuid.Nil || ref.AccountID == uuid.Nil {
		return fmt.Errorf("%w: organization and account required", ErrDossierManifestInvalid)
	}
	if ref.AttachedBy == uuid.Nil {
		return ErrDossierHumanActor
	}
	ref.Schema = firstNonEmpty(strings.TrimSpace(ref.Schema), DossierSchemaV1)
	ref.CatalogMode = strings.ToLower(strings.TrimSpace(ref.CatalogMode))
	ref.DataState = strings.ToUpper(strings.TrimSpace(ref.DataState))
	ref.DossierID = strings.TrimSpace(ref.DossierID)
	ref.ContentHash = strings.TrimSpace(ref.ContentHash)
	ref.PublicContentHash = strings.TrimSpace(ref.PublicContentHash)
	ref.ProducerSHA = strings.TrimSpace(ref.ProducerSHA)
	ref.AsOf = strings.TrimSpace(ref.AsOf)
	ref.ArtifactURI = strings.TrimSpace(ref.ArtifactURI)
	if ref.ArtifactURI != "" && !dossierURIPattern.MatchString(ref.ArtifactURI) {
		return fmt.Errorf("%w: artifact_uri must match %s", ErrDossierManifestInvalid, dossierURIPattern)
	}
	if err := ValidateDossierManifest(DossierManifest{
		DossierID: ref.DossierID, Schema: ref.Schema, CatalogMode: ref.CatalogMode,
		DataState: ref.DataState, ContentHash: ref.ContentHash,
		PublicContentHash: ref.PublicContentHash, AsOf: ref.AsOf, ProducerSHA: ref.ProducerSHA,
	}); err != nil {
		return err
	}
	if ref.ID == uuid.Nil {
		ref.ID = uuid.New()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if ref.AttachedAt.IsZero() {
		ref.AttachedAt = now
	}
	if ref.CreatedAt.IsZero() {
		ref.CreatedAt = now
	}
	ref.UpdatedAt = now
	ref.Deliverable, ref.NotDeliverableReason = DossierDeliverability(ref.CatalogMode, ref.DataState)
	return nil
}

// MarkDossierDelivered records the human handoff. It is the only way a
// reference becomes delivered, and a non-deliverable reference never can.
func MarkDossierDelivered(ref *DossierReference, actor uuid.UUID, at time.Time, note string) error {
	if ref == nil {
		return ErrDossierReferenceMissing
	}
	if actor == uuid.Nil {
		return ErrDossierHumanActor
	}
	if !ref.Deliverable {
		return fmt.Errorf("%w: %s", ErrDossierNotDeliverable, ref.NotDeliverableReason)
	}
	if ref.Delivered() {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	note = strings.TrimSpace(note)
	if len(note) > DossierDeliveryNoteMax {
		return fmt.Errorf("%w: delivery_note over %d bytes", ErrDossierManifestInvalid, DossierDeliveryNoteMax)
	}
	// The note is free prose, so it cannot be format-pinned. It can still be
	// kept from becoming a place to paste the dossier back in.
	if key := findForbiddenDossierValue(note); key != "" {
		return fmt.Errorf("%w: delivery_note contains %s", ErrDossierPrivateBody, key)
	}
	at = at.UTC()
	ref.DeliveredAt = &at
	ref.DeliveredBy = &actor
	ref.DeliveryNote = note
	ref.UpdatedAt = at
	return nil
}

// dossierScanMaxDepth bounds recursion against a hostile payload. Reaching it is
// itself a refusal: returning "" here would silently declare a deeply nested
// payload clean, which is the opposite of what the guarantee says.
const dossierScanMaxDepth = 32

// dossierScanTooDeep is reported as the offending key when the cap is reached.
const dossierScanTooDeep = "payload_nested_beyond_scan_depth"

func findForbiddenDossierKey(node any, depth int) string {
	if depth > dossierScanMaxDepth {
		return dossierScanTooDeep
	}
	switch v := node.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if isForbiddenDossierKey(k) {
				return k
			}
			if found := findForbiddenDossierKey(v[k], depth+1); found != "" {
				return found
			}
		}
	case []any:
		for _, item := range v {
			if found := findForbiddenDossierKey(item, depth+1); found != "" {
				return found
			}
		}
	}
	return ""
}

func isForbiddenDossierKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, bad := range dossierForbiddenKeys {
		if k == bad {
			return true
		}
	}
	return false
}

func dossierString(doc map[string]any, key string) string {
	if v, ok := doc[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// memoryDossierStore is the unit-test authority; production wires Postgres.
type memoryDossierStore struct {
	mu    sync.Mutex
	byID  map[uuid.UUID]DossierReference
	byKey map[string]uuid.UUID
}

// NewMemoryDossierStore returns an in-process reference store.
func NewMemoryDossierStore() DossierReferenceStore {
	return &memoryDossierStore{byID: map[uuid.UUID]DossierReference{}, byKey: map[string]uuid.UUID{}}
}

func (m *memoryDossierStore) PutDossierReference(_ context.Context, ref *DossierReference) error {
	if m == nil {
		return ErrDossierStoreUnavailable
	}
	if ref == nil || ref.ID == uuid.Nil {
		return ErrDossierReferenceMissing
	}
	if ref.DeliveredAt != nil || ref.DeliveredBy != nil {
		return ErrDossierDeliveryNotInferred
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Model the unique index the migration declares. Without it three identical
	// attaches give three rows here and one in Postgres, and the suite stops
	// being able to see an upsert defect at all.
	key := dossierUniqueKey(*ref)
	if existing, ok := m.byKey[key]; ok {
		prior := m.byID[existing]
		ref.ID = prior.ID
		ref.AttachedAt = prior.AttachedAt
		ref.CreatedAt = prior.CreatedAt
		ref.DeliveredAt = prior.DeliveredAt
		ref.DeliveredBy = prior.DeliveredBy
		ref.DeliveryNote = prior.DeliveryNote
	}
	m.byID[ref.ID] = *ref
	m.byKey[key] = ref.ID
	return nil
}

// dossierUniqueKey mirrors the migration's unique index.
func dossierUniqueKey(ref DossierReference) string {
	return ref.OrganizationID.String() + "|" + ref.AccountID.String() + "|" + ref.DossierID + "|" + ref.ContentHash
}

func (m *memoryDossierStore) GetDossierReference(_ context.Context, orgID, id uuid.UUID) (*DossierReference, error) {
	if m == nil {
		return nil, ErrDossierStoreUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ref, ok := m.byID[id]
	if !ok || ref.OrganizationID != orgID {
		return nil, nil
	}
	return &ref, nil
}

func (m *memoryDossierStore) ListDossierReferencesForAccounts(_ context.Context, orgID uuid.UUID, accountIDs []uuid.UUID) ([]DossierReference, error) {
	if m == nil {
		return nil, ErrDossierStoreUnavailable
	}
	if orgID == uuid.Nil || len(accountIDs) == 0 {
		return nil, nil
	}
	wanted := make(map[uuid.UUID]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		wanted[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	newest := map[uuid.UUID]DossierReference{}
	for _, ref := range m.byID {
		if ref.OrganizationID != orgID {
			continue
		}
		if _, ok := wanted[ref.AccountID]; !ok {
			continue
		}
		if cur, ok := newest[ref.AccountID]; !ok || ref.AttachedAt.After(cur.AttachedAt) {
			newest[ref.AccountID] = ref
		}
	}
	out := make([]DossierReference, 0, len(newest))
	for _, ref := range newest {
		out = append(out, ref)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AccountID.String() < out[j].AccountID.String() })
	return out, nil
}

func (m *memoryDossierStore) ListDossierReferences(_ context.Context, orgID, accountID uuid.UUID, limit int) ([]DossierReference, error) {
	if m == nil {
		return nil, ErrDossierStoreUnavailable
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DossierReference
	for _, ref := range m.byID {
		if ref.OrganizationID != orgID {
			continue
		}
		if accountID != uuid.Nil && ref.AccountID != accountID {
			continue
		}
		out = append(out, ref)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AttachedAt.Equal(out[j].AttachedAt) {
			return out[i].ID.String() < out[j].ID.String()
		}
		return out[i].AttachedAt.After(out[j].AttachedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memoryDossierStore) SetDossierReferenceDelivered(_ context.Context, ref *DossierReference) error {
	if m == nil {
		return ErrDossierStoreUnavailable
	}
	if ref == nil || ref.ID == uuid.Nil {
		return ErrDossierReferenceMissing
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[ref.ID]; !ok {
		return ErrDossierReferenceMissing
	}
	m.byID[ref.ID] = *ref
	return nil
}
