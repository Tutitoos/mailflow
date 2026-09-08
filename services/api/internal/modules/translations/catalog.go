package translations

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultLocale     = "en"
	MaxCatalogKeys    = 512
	MaxValueBytes     = 4096
	MaxChanges        = 256
	RetainedRevisions = 100
)

var (
	ErrConflict          = errors.New("translation revision conflict")
	ErrInvalidCatalog    = errors.New("invalid translation catalog")
	ErrUnavailable       = errors.New("translation catalog unavailable")
	keyPattern           = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
	privateValuePattern  = regexp.MustCompile(`(?i)([a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}|bearer\s+[a-z0-9._-]{12,}|eyJ[a-zA-Z0-9_-]{10,}\.|https?://[^\s]*(?:token|signature|sig)=)`)
	builtinCatalogs      map[string]map[string]string
	builtinCatalogsError error
)

//go:embed catalogs.json
var catalogFiles embed.FS

func init() {
	contents, err := catalogFiles.ReadFile("catalogs.json")
	if err == nil {
		err = json.Unmarshal(contents, &builtinCatalogs)
	}
	if err == nil {
		err = validateBuiltinCatalogs(builtinCatalogs)
	}
	builtinCatalogsError = err
}

type Publisher interface {
	Publish(context.Context, string, string, json.RawMessage) (events.Envelope, error)
}

type Catalog struct {
	pool      *pgxpool.Pool
	publisher Publisher
	mutex     sync.RWMutex
	revision  int64
	values    map[string]map[string]Message
}

type Message struct {
	Value      string `json:"value"`
	SourceHash string `json:"sourceHash"`
}

type LocaleCatalog struct {
	Locale        string            `json:"locale"`
	DefaultLocale string            `json:"defaultLocale"`
	Revision      int64             `json:"revision"`
	Messages      map[string]string `json:"messages"`
	MissingKeys   []string          `json:"missingKeys"`
}

type Export struct {
	DefaultLocale string                        `json:"defaultLocale"`
	Revision      int64                         `json:"revision"`
	Catalogs      map[string]map[string]Message `json:"catalogs"`
	Diagnostics   Diagnostics                   `json:"diagnostics"`
}

type Change struct {
	Locale     string  `json:"locale"`
	Key        string  `json:"key"`
	Value      *string `json:"value"`
	SourceHash string  `json:"sourceHash,omitempty"`
}

type UpdateRequest struct {
	ExpectedRevision int64    `json:"expectedRevision"`
	Changes          []Change `json:"changes"`
}

type UpdateResult struct {
	Export
	EventPublished bool `json:"eventPublished"`
}

type Diagnostics struct {
	MissingEnglish []string `json:"missingEnglish"`
	MissingSpanish []string `json:"missingSpanish"`
	StaleSpanish   []string `json:"staleSpanish"`
	InvalidICU     []string `json:"invalidIcu"`
	UnknownKeys    []string `json:"unknownKeys"`
	PrivateValues  []string `json:"privateValues"`
}

func NewCatalog() *Catalog {
	if builtinCatalogsError != nil {
		panic(builtinCatalogsError)
	}
	return &Catalog{values: builtinMessages()}
}

func NewPersistentCatalog(ctx context.Context, pool *pgxpool.Pool, publisher Publisher) (*Catalog, error) {
	if pool == nil || builtinCatalogsError != nil {
		return nil, ErrUnavailable
	}
	catalog := &Catalog{pool: pool, publisher: publisher}
	if err := catalog.bootstrap(ctx); err != nil {
		return nil, err
	}
	return catalog, nil
}

func (catalog *Catalog) Locale(locale string) map[string]string {
	result, _ := catalog.snapshot(locale)
	return result.Messages
}

func (catalog *Catalog) Get(ctx context.Context, locale string) (LocaleCatalog, error) {
	if catalog == nil {
		return LocaleCatalog{}, ErrUnavailable
	}
	if catalog.pool != nil {
		if err := catalog.refresh(ctx); err != nil {
			return LocaleCatalog{}, err
		}
	}
	result, ok := catalog.snapshot(locale)
	if !ok {
		return LocaleCatalog{}, ErrUnavailable
	}
	return result, nil
}

func (catalog *Catalog) Export(ctx context.Context) (Export, error) {
	if catalog == nil {
		return Export{}, ErrUnavailable
	}
	if catalog.pool != nil {
		if err := catalog.refresh(ctx); err != nil {
			return Export{}, err
		}
	}
	catalog.mutex.RLock()
	defer catalog.mutex.RUnlock()
	return exportCatalog(catalog.revision, catalog.values), nil
}

func (catalog *Catalog) Validate(ctx context.Context, request UpdateRequest) (Export, error) {
	current, err := catalog.Export(ctx)
	if err != nil {
		return Export{}, err
	}
	if request.ExpectedRevision != current.Revision {
		return current, ErrConflict
	}
	values, diagnostics := applyChanges(current.Catalogs, request.Changes)
	return Export{DefaultLocale: DefaultLocale, Revision: current.Revision, Catalogs: values, Diagnostics: diagnostics}, nil
}

func (catalog *Catalog) Update(ctx context.Context, actorUserID string, request UpdateRequest) (UpdateResult, error) {
	if catalog == nil || catalog.pool == nil || strings.TrimSpace(actorUserID) == "" {
		return UpdateResult{}, ErrUnavailable
	}
	if len(request.Changes) == 0 || len(request.Changes) > MaxChanges {
		return UpdateResult{}, ErrInvalidCatalog
	}
	tx, err := catalog.pool.Begin(ctx)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("begin translation update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('mailflow:translations'))`); err != nil {
		return UpdateResult{}, fmt.Errorf("lock translations: %w", err)
	}
	revision, values, err := loadActive(ctx, tx)
	if err != nil {
		return UpdateResult{}, err
	}
	if request.ExpectedRevision != revision {
		return UpdateResult{Export: exportCatalog(revision, values)}, ErrConflict
	}
	updated, diagnostics := applyChanges(values, request.Changes)
	exported := Export{DefaultLocale: DefaultLocale, Revision: revision, Catalogs: updated, Diagnostics: diagnostics}
	if diagnostics.blocking() {
		return UpdateResult{Export: exported}, ErrInvalidCatalog
	}
	var nextRevision int64
	if err := tx.QueryRow(ctx, `insert into translation_revisions (actor_user_id, message_count) values ($1::uuid, $2) returning id`, actorUserID, messageCount(updated)).Scan(&nextRevision); err != nil {
		return UpdateResult{}, fmt.Errorf("create translation revision: %w", err)
	}
	if err := insertMessages(ctx, tx, nextRevision, updated); err != nil {
		return UpdateResult{}, err
	}
	if _, err := tx.Exec(ctx, `update translation_state set active_revision=$1, updated_at=now() where singleton=true`, nextRevision); err != nil {
		return UpdateResult{}, fmt.Errorf("activate translation revision: %w", err)
	}
	if _, err := tx.Exec(ctx, `delete from translation_revisions where id <> $1 and id not in (select id from translation_revisions order by id desc limit $2)`, nextRevision, RetainedRevisions); err != nil {
		return UpdateResult{}, fmt.Errorf("retain translation revisions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UpdateResult{}, fmt.Errorf("commit translation revision: %w", err)
	}
	catalog.store(nextRevision, updated)
	exported = exportCatalog(nextRevision, updated)
	published := false
	if catalog.publisher != nil {
		payload, _ := json.Marshal(map[string]any{"revision": nextRevision})
		_, publishErr := catalog.publisher.Publish(ctx, actorUserID, "translations.changed", payload)
		published = publishErr == nil
	}
	return UpdateResult{Export: exported, EventPublished: published}, nil
}

func (diagnostics Diagnostics) blocking() bool {
	return len(diagnostics.MissingEnglish) > 0 || len(diagnostics.StaleSpanish) > 0 || len(diagnostics.InvalidICU) > 0 || len(diagnostics.UnknownKeys) > 0 || len(diagnostics.PrivateValues) > 0
}

func (catalog *Catalog) snapshot(locale string) (LocaleCatalog, bool) {
	catalog.mutex.RLock()
	defer catalog.mutex.RUnlock()
	if len(catalog.values[DefaultLocale]) == 0 {
		return LocaleCatalog{}, false
	}
	selectedLocale := locale
	if selectedLocale != "es" {
		selectedLocale = DefaultLocale
	}
	messages := make(map[string]string, len(catalog.values[DefaultLocale]))
	missing := make([]string, 0)
	for key, message := range catalog.values[DefaultLocale] {
		messages[key] = message.Value
		if selectedLocale == "es" {
			if translated, ok := catalog.values["es"][key]; ok {
				messages[key] = translated.Value
			} else {
				missing = append(missing, key)
			}
		}
	}
	sort.Strings(missing)
	return LocaleCatalog{Locale: selectedLocale, DefaultLocale: DefaultLocale, Revision: catalog.revision, Messages: messages, MissingKeys: missing}, true
}

func (catalog *Catalog) bootstrap(ctx context.Context) error {
	tx, err := catalog.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin translation bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('mailflow:translations'))`); err != nil {
		return fmt.Errorf("lock translation bootstrap: %w", err)
	}
	var revision int64
	err = tx.QueryRow(ctx, `select active_revision from translation_state where singleton=true`).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		values := builtinMessages()
		if err := tx.QueryRow(ctx, `insert into translation_revisions (message_count) values ($1) returning id`, messageCount(values)).Scan(&revision); err != nil {
			return fmt.Errorf("create initial translation revision: %w", err)
		}
		if err := insertMessages(ctx, tx, revision, values); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into translation_state (active_revision) values ($1)`, revision); err != nil {
			return fmt.Errorf("activate initial translation revision: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("read translation state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit translation bootstrap: %w", err)
	}
	return catalog.refresh(ctx)
}

func (catalog *Catalog) refresh(ctx context.Context) error {
	var activeRevision int64
	if err := catalog.pool.QueryRow(ctx, `select active_revision from translation_state where singleton=true`).Scan(&activeRevision); err != nil {
		return fmt.Errorf("read active translation revision: %w", err)
	}
	catalog.mutex.RLock()
	current := catalog.revision
	catalog.mutex.RUnlock()
	if current == activeRevision {
		return nil
	}
	revision, values, err := loadActive(ctx, catalog.pool)
	if err != nil {
		return err
	}
	catalog.store(revision, values)
	return nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadActive(ctx context.Context, database queryer) (int64, map[string]map[string]Message, error) {
	var revision int64
	if err := database.QueryRow(ctx, `select active_revision from translation_state where singleton=true`).Scan(&revision); err != nil {
		return 0, nil, fmt.Errorf("read active translation revision: %w", err)
	}
	rows, err := database.Query(ctx, `select locale, key, value, source_hash from translation_messages where revision_id=$1 order by locale, key`, revision)
	if err != nil {
		return 0, nil, fmt.Errorf("read translation messages: %w", err)
	}
	defer rows.Close()
	values := map[string]map[string]Message{"en": {}, "es": {}}
	for rows.Next() {
		var locale, key string
		var message Message
		if err := rows.Scan(&locale, &key, &message.Value, &message.SourceHash); err != nil {
			return 0, nil, fmt.Errorf("scan translation message: %w", err)
		}
		values[locale][key] = message
	}
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("iterate translation messages: %w", err)
	}
	return revision, values, nil
}

func insertMessages(ctx context.Context, tx pgx.Tx, revision int64, values map[string]map[string]Message) error {
	for _, locale := range []string{"en", "es"} {
		for _, key := range sortedKeys(values[locale]) {
			message := values[locale][key]
			if _, err := tx.Exec(ctx, `insert into translation_messages (revision_id, locale, key, value, source_hash) values ($1,$2,$3,$4,$5)`, revision, locale, key, message.Value, message.SourceHash); err != nil {
				return fmt.Errorf("persist translation %s.%s: %w", locale, key, err)
			}
		}
	}
	return nil
}

func (catalog *Catalog) store(revision int64, values map[string]map[string]Message) {
	catalog.mutex.Lock()
	catalog.revision = revision
	catalog.values = cloneMessages(values)
	catalog.mutex.Unlock()
}

func builtinMessages() map[string]map[string]Message {
	values := map[string]map[string]Message{"en": {}, "es": {}}
	for key, value := range builtinCatalogs["en"] {
		hash := sourceHash(value)
		values["en"][key] = Message{Value: value, SourceHash: hash}
		if spanish, ok := builtinCatalogs["es"][key]; ok {
			values["es"][key] = Message{Value: spanish, SourceHash: hash}
		}
	}
	return values
}

func applyChanges(current map[string]map[string]Message, changes []Change) (map[string]map[string]Message, Diagnostics) {
	values := cloneMessages(current)
	diagnostics := emptyDiagnostics()
	if len(changes) == 0 || len(changes) > MaxChanges {
		diagnostics.UnknownKeys = []string{"changes"}
		return values, diagnostics
	}
	seen := make(map[string]bool)
	for _, change := range changes {
		identity := change.Locale + "\x00" + change.Key
		if seen[identity] || (change.Locale != "en" && change.Locale != "es") || !keyPattern.MatchString(change.Key) {
			diagnostics.UnknownKeys = append(diagnostics.UnknownKeys, change.Locale+"."+change.Key)
			continue
		}
		seen[identity] = true
		if _, known := builtinCatalogs["en"][change.Key]; !known {
			diagnostics.UnknownKeys = append(diagnostics.UnknownKeys, change.Locale+"."+change.Key)
			continue
		}
		if change.Value == nil {
			if change.Locale == "en" {
				diagnostics.MissingEnglish = append(diagnostics.MissingEnglish, change.Key)
			} else {
				delete(values["es"], change.Key)
			}
			continue
		}
		value := strings.TrimSpace(*change.Value)
		if !safeValue(value) {
			diagnostics.PrivateValues = append(diagnostics.PrivateValues, change.Locale+"."+change.Key)
			continue
		}
		if change.Locale == "en" {
			values["en"][change.Key] = Message{Value: value, SourceHash: sourceHash(value)}
		} else {
			values["es"][change.Key] = Message{Value: value, SourceHash: change.SourceHash}
		}
	}
	validated := diagnose(values)
	diagnostics.MissingEnglish = append(diagnostics.MissingEnglish, validated.MissingEnglish...)
	diagnostics.MissingSpanish = validated.MissingSpanish
	diagnostics.StaleSpanish = validated.StaleSpanish
	diagnostics.InvalidICU = validated.InvalidICU
	uniqueSort(&diagnostics.MissingEnglish)
	uniqueSort(&diagnostics.UnknownKeys)
	uniqueSort(&diagnostics.PrivateValues)
	return values, diagnostics
}

func diagnose(values map[string]map[string]Message) Diagnostics {
	diagnostics := emptyDiagnostics()
	for key := range builtinCatalogs["en"] {
		english, ok := values["en"][key]
		if !ok || !safeValue(english.Value) {
			diagnostics.MissingEnglish = append(diagnostics.MissingEnglish, key)
			continue
		}
		englishArgs, englishValid := icuArguments(english.Value)
		if !englishValid {
			diagnostics.InvalidICU = append(diagnostics.InvalidICU, "en."+key)
		}
		spanish, ok := values["es"][key]
		if !ok {
			diagnostics.MissingSpanish = append(diagnostics.MissingSpanish, key)
			continue
		}
		if spanish.SourceHash != sourceHash(english.Value) {
			diagnostics.StaleSpanish = append(diagnostics.StaleSpanish, key)
		}
		spanishArgs, spanishValid := icuArguments(spanish.Value)
		if !spanishValid || !maps.Equal(englishArgs, spanishArgs) {
			diagnostics.InvalidICU = append(diagnostics.InvalidICU, "es."+key)
		}
	}
	for key := range values["en"] {
		if _, ok := builtinCatalogs["en"][key]; !ok {
			diagnostics.UnknownKeys = append(diagnostics.UnknownKeys, "en."+key)
		}
	}
	for key := range values["es"] {
		if _, ok := builtinCatalogs["en"][key]; !ok {
			diagnostics.UnknownKeys = append(diagnostics.UnknownKeys, "es."+key)
		}
	}
	uniqueSort(&diagnostics.MissingEnglish)
	uniqueSort(&diagnostics.MissingSpanish)
	uniqueSort(&diagnostics.StaleSpanish)
	uniqueSort(&diagnostics.InvalidICU)
	uniqueSort(&diagnostics.UnknownKeys)
	return diagnostics
}

func exportCatalog(revision int64, values map[string]map[string]Message) Export {
	return Export{DefaultLocale: DefaultLocale, Revision: revision, Catalogs: cloneMessages(values), Diagnostics: diagnose(values)}
}

func validateBuiltinCatalogs(values map[string]map[string]string) error {
	if len(values) != 2 || len(values["en"]) == 0 || len(values["en"]) > MaxCatalogKeys {
		return ErrInvalidCatalog
	}
	for locale, messages := range values {
		if locale != "en" && locale != "es" {
			return ErrInvalidCatalog
		}
		for key, value := range messages {
			if !keyPattern.MatchString(key) || !safeValue(value) {
				return ErrInvalidCatalog
			}
			if _, valid := icuArguments(value); !valid {
				return ErrInvalidCatalog
			}
		}
	}
	for key := range values["es"] {
		if _, ok := values["en"][key]; !ok {
			return ErrInvalidCatalog
		}
	}
	return nil
}

func safeValue(value string) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= MaxValueBytes && !privateValuePattern.MatchString(value)
}

func sourceHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func cloneMessages(values map[string]map[string]Message) map[string]map[string]Message {
	result := map[string]map[string]Message{"en": {}, "es": {}}
	for _, locale := range []string{"en", "es"} {
		result[locale] = maps.Clone(values[locale])
	}
	return result
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func uniqueSort(values *[]string) {
	sort.Strings(*values)
	result := (*values)[:0]
	for _, value := range *values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	*values = result
}

func messageCount(values map[string]map[string]Message) int {
	return len(values["en"]) + len(values["es"])
}

func emptyDiagnostics() Diagnostics {
	return Diagnostics{
		MissingEnglish: []string{}, MissingSpanish: []string{}, StaleSpanish: []string{},
		InvalidICU: []string{}, UnknownKeys: []string{}, PrivateValues: []string{},
	}
}
