package provider

// Upstream model-catalog layer.
//
// The compiled-in tables (models_catalog.go) stay hand-typed: they are the
// authority on the models whose facts were verified here. This layer adds a
// DOWNLOADED copy of the catalogue, merged over the compiled-in facts from a
// tracked JSON file in this repo (internal/provider/data/catalog.json), so a
// model the catalogue already knows gets refreshed facts and a model it does
// not know yet appears in the first-party pickers without a binary release.
//
// MERGE SEMANTICS (each pinned in upstream_test.go):
//   - An upstream id that matches a compiled-in entry replaces that entry's
//     facts — prices, window, image support, efforts, quirks — for that id.
//     The compiled-in row keeps its identity: its ID string and its
//     ActiveByDefault never change, and its aliases are a UNION with the
//     upstream ones (aliases another compiled-in entry claims are dropped, so
//     the index's first-claim-wins cannot break). The compiled-in slices are
//     never mutated: the merged layer is built fresh and installed whole.
//   - An upstream id no compiled-in entry claims is ADDED, placed by its
//     Collection field ("anthropic" extends the Anthropic picker, "openai" the
//     OpenAI one, "google" the Gemini lookups, "open" the open-weight
//     catalogue, "legacy" the retired list; anything else is lookup-only). Its
//     ActiveByDefault is the file's own.
//   - ResponsesOnly rows stay filtered out of the OpenAI picker, whatever list
//     they arrive in.
//
// A failed, disabled, or unparseable fetch leaves the previously merged state
// untouched — the same rule RefreshCatalog follows on a transient failure.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// upstreamCatalogVersion is the wire format version of the upstream file.
	upstreamCatalogVersion = 1
	// upstreamCatalogURLDefault is where SyncUpstreamCatalog fetches from by
	// default: the tracked data file in the repo, served raw.
	upstreamCatalogURLDefault = "https://raw.githubusercontent.com/prasenjeet-symon/ogcode/main/internal/provider/data/catalog.json"
	// upstreamCatalogURLEnv overrides the fetch URL (tests, mirrors).
	upstreamCatalogURLEnv = "OGCODE_CATALOG_URL"
	// upstreamSyncEnabledEnv is the kill switch: exactly "false" turns the
	// sync fetch off. Loading a previously persisted file stays available —
	// that is local state, not a fetch.
	upstreamSyncEnabledEnv = "OGCODE_CATALOG_SYNC"
	// upstreamSyncTimeout bounds one fetch, generous for a raw file fetch.
	upstreamSyncTimeout = 15 * time.Second
	// upstreamBodyCap is the response-body cap, the same bound the OGX
	// gateway puts on a completion body: no catalogue file needs more.
	upstreamBodyCap = 4 << 20
)

// upstreamCatalogFile is the persisted/upstream file:
//
//	{"version":1,"generatedAt":"2026-10-30T…","models":[…]}.
//
// Source and SyncedAt are stamped by SyncUpstreamCatalog when it persists the
// fetched file; a file straight from the repo carries neither.
type upstreamCatalogFile struct {
	Version     int            `json:"version"`
	GeneratedAt time.Time      `json:"generatedAt,omitempty"`
	Source      string         `json:"source,omitempty"`
	SyncedAt    time.Time      `json:"syncedAt,omitempty"`
	Models      []CatalogModel `json:"models"`
}

// UpstreamSyncResult reports what one sync or apply did: Installed is the
// count of models added to the catalogue, Updated the count of compiled-in
// entries whose facts were replaced, and Skipped the entries refused at the
// plausibility guard or as duplicates within the file. Source is where the
// file came from — the fetch URL for a sync, or the file's own stamped source
// for an applied persisted file — and FetchedAt is when the file landed here
// (the persisted sync time, or the file's generatedAt for a raw file).
type UpstreamSyncResult struct {
	Installed int
	Updated   int
	Skipped   int
	Source    string
	FetchedAt time.Time
}

// ErrUpstreamSyncDisabled is returned by SyncUpstreamCatalog when the
// OGCODE_CATALOG_SYNC=false kill switch is set.
var ErrUpstreamSyncDisabled = errors.New("model-catalog sync is disabled (OGCODE_CATALOG_SYNC=false)")

// UpstreamCatalogURL is the URL SyncUpstreamCatalog fetches the catalogue
// from: OGCODE_CATALOG_URL when set, else the tracked data file's raw URL.
func UpstreamCatalogURL() string {
	if raw := strings.TrimSpace(os.Getenv(upstreamCatalogURLEnv)); raw != "" {
		return raw
	}
	return upstreamCatalogURLDefault
}

func upstreamSyncEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv(upstreamSyncEnabledEnv)), "false")
}

// upstreamCatalogPath is the persisted copy under the machine's ogcode config
// dir (the same home + .ogcode convention every other local state file uses),
// or "" when no home is known — callers then skip persistence entirely.
func upstreamCatalogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ogcode", "catalog-upstream.json")
}

// SyncUpstreamCatalog fetches the upstream catalogue and merges it over the
// compiled-in tables, persisting a stamped copy for the next boot. A disabled
// kill switch returns ErrUpstreamSyncDisabled without touching anything; a
// fetch, parse, or persist failure is also a no-op for the merged state —
// what was merged before stays merged.
func SyncUpstreamCatalog(ctx context.Context) (UpstreamSyncResult, error) {
	if !upstreamSyncEnabled() {
		return UpstreamSyncResult{}, ErrUpstreamSyncDisabled
	}
	url := UpstreamCatalogURL()
	data, err := fetchUpstreamCatalog(ctx, url)
	if err != nil {
		return UpstreamSyncResult{}, err
	}
	res, err := ApplyUpstreamCatalog(data)
	if err != nil {
		return UpstreamSyncResult{}, err
	}
	res.Source = url
	res.FetchedAt = time.Now()
	if err := persistUpstreamCatalog(data, url, res.FetchedAt); err != nil {
		// The merge itself landed; report the persistence problem so the caller
		// can decide, without undoing state other goroutines now read.
		return res, fmt.Errorf("upstream catalog fetched and merged, but not persisted: %w", err)
	}
	return res, nil
}

// fetchUpstreamCatalog GETs url with a generous 15s budget and a hard cap on
// the response body, returning the raw catalogue file.
func fetchUpstreamCatalog(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, upstreamSyncTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("upstream catalog request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream catalog fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream catalog fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamBodyCap+1))
	if err != nil {
		return nil, fmt.Errorf("upstream catalog fetch %s: %w", url, err)
	}
	if len(body) > upstreamBodyCap {
		return nil, fmt.Errorf("upstream catalog fetch %s: response exceeds %d bytes", url, upstreamBodyCap)
	}
	return body, nil
}

// persistUpstreamCatalog writes the fetched data with its source and sync
// time stamped in, replacing atomically (same directory, rename over). A
// missing home dir is an error — there is nowhere for the next boot to read.
func persistUpstreamCatalog(data []byte, source string, syncedAt time.Time) error {
	var f upstreamCatalogFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("upstream catalog persist: %w", err)
	}
	f.Source = source
	f.SyncedAt = syncedAt
	path := upstreamCatalogPath()
	if path == "" {
		return errors.New("upstream catalog persist: no home directory to write it to")
	}
	out, err := json.MarshalIndent(&f, "", "  ")
	if err != nil {
		return fmt.Errorf("upstream catalog persist: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("upstream catalog persist: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("upstream catalog persist: %w", err)
	}
	return os.Rename(tmp, path)
}

// ApplyUpstreamCatalog parses, validates, and merges an upstream catalogue
// file without network or persistence — the test seam, and the path both
// SyncUpstreamCatalog and LoadPersistedUpstreamCatalog run through.
func ApplyUpstreamCatalog(data []byte) (UpstreamSyncResult, error) {
	parsed, err := parseUpstreamCatalog(data)
	if err != nil {
		return UpstreamSyncResult{}, err
	}
	return applyUpstream(parsed), nil
}

// upstreamParsed is one file's validated entries plus what the plausibility
// guard and the in-file dedupe refused.
type upstreamParsed struct {
	file    upstreamCatalogFile
	models  []CatalogModel
	skipped int
}

// parseUpstreamCatalog decodes and validates the file whole: bad JSON or an
// unsupported version refuses the FILE; a model that fails the plausibility
// guard or claims a key another entry in the same file already claimed is
// refused on its own and counted Skipped. Aliases colliding with an earlier
// sibling are dropped — they would be redundant lookups, not errors.
func parseUpstreamCatalog(data []byte) (upstreamParsed, error) {
	var f upstreamCatalogFile
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&f); err != nil {
		return upstreamParsed{}, fmt.Errorf("upstream catalog: malformed JSON: %w", err)
	}
	if f.Version != upstreamCatalogVersion {
		return upstreamParsed{}, fmt.Errorf(
			"upstream catalog: unsupported version %d (want %d)", f.Version, upstreamCatalogVersion)
	}
	parsed := upstreamParsed{file: f}
	// seen holds every key the file has claimed so far: entries' ids and kept
	// aliases, both raw and date-stripped, so a dated duplicate of an earlier
	// sibling cannot slip past the dedupe.
	seen := make(map[string]bool, len(f.Models))
	for _, m := range f.Models {
		if reason := checkUpstreamEntry(m); reason != "" {
			parsed.skipped++
			continue
		}
		key := normalizeModelID(m.ID)
		if key == "" || seen[key] {
			parsed.skipped++
			continue
		}
		// A dated spelling of a key an earlier entry already claimed —
		// "dupe-20260101" after "dupe" — is the same duplicate.
		if base := dateSuffix.ReplaceAllString(key, ""); base != key && seen[base] {
			parsed.skipped++
			continue
		}
		claimKey(seen, key)
		aliases := make([]string, 0, len(m.Aliases))
		for _, a := range m.Aliases {
			ka := normalizeModelID(a)
			if ka == "" || ka == key || seen[ka] {
				continue
			}
			claimKey(seen, ka)
			aliases = append(aliases, a)
		}
		m.Aliases = aliases
		parsed.models = append(parsed.models, m)
	}
	return parsed, nil
}

// claimKey records key in seen plus its date-stripped form, so "m-4-6" and
// "m-4-6-20260115" in one file count as the duplicate they both resolve to.
func claimKey(seen map[string]bool, key string) {
	seen[key] = true
	if base := dateSuffix.ReplaceAllString(key, ""); base != "" && base != key {
		seen[base] = true
	}
}

// checkUpstreamEntry refuses a single entry the data file must not install:
// empty id, a negative price, window, or output ceiling (0 = unknown is
// fine). It returns "" for an acceptable entry.
func checkUpstreamEntry(m CatalogModel) string {
	if m.ID == "" {
		return "empty id"
	}
	if m.ContextWindow < 0 {
		return fmt.Sprintf("negative context window %d", m.ContextWindow)
	}
	if m.MaxOutputTokens < 0 {
		return fmt.Sprintf("negative max output tokens %d", m.MaxOutputTokens)
	}
	for name, price := range map[string]float64{
		"output":      m.OutputPricePerM,
		"input":       m.InputPricePerM,
		"cache read":  m.CacheReadPricePerM,
		"cache write": m.CacheWritePricePerM,
	} {
		if price < 0 {
			return fmt.Sprintf("negative %s price", name)
		}
	}
	return ""
}

// upstreamMu guards the merged layer as a whole: the installed lists and the
// swap on apply. The compiled-in package slices are read-only and shared by
// every provider instance — never mutated anywhere.
var (
	upstreamMu    sync.RWMutex
	upstreamLists *mergedCatalog // nil = compiled-in tables only
)

// mergedCatalog is the catalogue as it stands after an upstream file landed:
// fresh copies of the five compiled-in lists with upstream facts replacing
// matched entries, plus the rows no compiled-in entry claimed, appended by
// collection (extras being the lookup-only pool every other collection lands
// in).
type mergedCatalog struct {
	anthropic, openai, google, open, legacy []CatalogModel
	extras                                  []CatalogModel
}

func cloneCatalogList(list []CatalogModel) []CatalogModel {
	out := make([]CatalogModel, len(list))
	copy(out, list)
	return out
}

// applyUpstream merges parsed into the compiled-in tables and installs the
// result as the live catalogue. See the file header for the merge semantics.
func applyUpstream(parsed upstreamParsed) UpstreamSyncResult {
	res := UpstreamSyncResult{
		Skipped:   parsed.skipped,
		Source:    parsed.file.Source,
		FetchedAt: parsed.file.SyncedAt,
	}
	if res.FetchedAt.IsZero() {
		res.FetchedAt = parsed.file.GeneratedAt
	}

	upstreamMu.Lock()

	// claims maps every key the compiled-in index would answer — each entry's
	// id and aliases, date-stripped variants included — to the slot that
	// claims it, first claim winning exactly as newCatalogIndex does. Matching
	// an upstream entry consults only these, so an upstream file can never
	// displace a compiled-in claim, only replace the entry behind it.
	type rowRef struct{ list, row int }
	claims := make(map[string]rowRef)
	claim := func(li, ri int, m CatalogModel) {
		add := func(key string) {
			if _, taken := claims[key]; !taken {
				claims[key] = rowRef{li, ri}
			}
		}
		if key := normalizeModelID(m.ID); key != "" {
			add(key)
			if base := dateSuffix.ReplaceAllString(key, ""); base != "" && base != key {
				add(base)
			}
		}
		for _, a := range m.Aliases {
			if ka := normalizeModelID(a); ka != "" {
				add(ka)
			}
		}
	}

	//
	for li, list := range [][]CatalogModel{AnthropicModels, OpenAIModels, GoogleModels, OpenModels, LegacyModels} {
		for ri := range list {
			claim(li, ri, list[ri])
		}
	}

	// merged carries fresh copies; the compiled-in slices stay untouched.
	merged := &mergedCatalog{
		anthropic: cloneCatalogList(AnthropicModels),
		openai:    cloneCatalogList(OpenAIModels),
		google:    cloneCatalogList(GoogleModels),
		open:      cloneCatalogList(OpenModels),
		legacy:    cloneCatalogList(LegacyModels),
	}

	slot := func(r rowRef) *CatalogModel {
		switch r.list {
		case 0:
			return &merged.anthropic[r.row]
		case 1:
			return &merged.openai[r.row]
		case 2:
			return &merged.google[r.row]
		case 3:
			return &merged.open[r.row]
		case 4:
			return &merged.legacy[r.row]
		}
		return nil
	}

	for _, m := range parsed.models {
		ref, found := (func() (rowRef, bool) {
			// Match by id first — exact key, then its date-stripped form, so an
			// undated upstream id meets a dated compiled-in one — then, for an
			// upstream spelling the compiled tables know only as an alias, by
			// the aliases. A candidate whose lookup already hit nothing at
			// exact form falls back to the stripped form, mirroring idx.lookup.
			for _, candidate := range append([]string{m.ID}, m.Aliases...) {
				k := normalizeModelID(candidate)
				if k == "" {
					continue
				}
				if at, ok := claims[k]; ok {
					return at, true
				}
				if base := dateSuffix.ReplaceAllString(k, ""); base != "" && base != k {
					if at, ok := claims[base]; ok {
						return at, true
					}
				}
			}
			return rowRef{}, false
		})()

		if !found {
			// A model the compiled-in tables do not know: added whole, placed
			// by its collection, ActiveByDefault exactly as the file says.
			switch m.Collection {
			case "anthropic":
				merged.anthropic = append(merged.anthropic, m)
			case "openai":
				merged.openai = append(merged.openai, m)
			case "google":
				merged.google = append(merged.google, m)
			case "open":
				merged.open = append(merged.open, m)
			case "legacy":
				merged.legacy = append(merged.legacy, m)
			default:
				merged.extras = append(merged.extras, m)
			}
			res.Installed++
			continue
		}

		// Facts replace the compiled-in row — but the row keeps its identity:
		// its ID string (the id first parties send), its ActiveByDefault (an
		// upstream file never flips an existing row's default-active state),
		// and its collection. Aliases are a union: the compiled-in ones stand,
		// upstream ones join unless another compiled-in entry claims the key,
		// which would break first-claim-wins in the rebuilt index.
		row := *slot(ref)
		replaced := m
		replaced.ID = row.ID
		replaced.ActiveByDefault = row.ActiveByDefault
		replaced.Collection = row.Collection
		aliases := make([]string, 0, len(row.Aliases)+len(m.Aliases))
		aliases = append(aliases, row.Aliases...)
		for _, a := range m.Aliases {
			ka := normalizeModelID(a)
			if ka == "" {
				continue
			}
			if owner, claimed := claims[ka]; claimed && owner != ref {
				continue // another compiled-in entry owns this spelling
			}
			if catalogHasAliasKey(aliases, ka) {
				continue
			}
			aliases = append(aliases, a)
		}
		replaced.Aliases = aliases
		*slot(ref) = replaced
		res.Updated++
	}

	// The swap goes in under the lock, but the memos must be invalidated
	// AFTER it is released: a lookup rebuilding its index holds its memo lock
	// while reading these lists (catalogLists takes upstreamMu.RLock), so
	// taking each memo's lock here, under upstreamMu, would deadlock the two
	// orderings against each other. No path above returns early, so the
	// explicit unlock stands in for the defer.
	upstreamLists = merged
	upstreamMu.Unlock()
	invalidateCatalogIndexes()
	return res
}

func catalogHasAliasKey(aliases []string, key string) bool {
	for _, a := range aliases {
		if normalizeModelID(a) == key {
			return true
		}
	}
	return false
}

// catalogLists returns the five lists the global catalogue is built from:
// the merged layer when one is installed, the compiled-in tables otherwise.
func catalogLists() [][]CatalogModel {
	upstreamMu.RLock()
	defer upstreamMu.RUnlock()
	if l := upstreamLists; l != nil {
		return [][]CatalogModel{
			l.anthropic, l.openai, l.google, l.open, l.legacy, l.extras,
		}
	}
	return [][]CatalogModel{
		AnthropicModels, OpenAIModels, GoogleModels, OpenModels, LegacyModels,
	}
}

// anthropicCatalogList is the list the Anthropic provider's picker is built
// from: the merged layer's Anthropic rows when one is installed (compiled-in
// rows with replaced facts, plus upstream additions placed here), else the
// compiled-in table.
func anthropicCatalogList() []CatalogModel {
	upstreamMu.RLock()
	defer upstreamMu.RUnlock()
	if l := upstreamLists; l != nil {
		return l.anthropic
	}
	return AnthropicModels
}

// openAICatalogList is the list the OpenAI provider's picker is built from:
// the merged layer's OpenAI rows when one is installed, else the compiled-in
// table. Rows here are still filtered by ResponsesOnly at the picker.
func openAICatalogList() []CatalogModel {
	upstreamMu.RLock()
	defer upstreamMu.RUnlock()
	if l := upstreamLists; l != nil {
		return l.openai
	}
	return OpenAIModels
}

// googleCatalogList is the list the Gemini lookups are built from.
func googleCatalogList() []CatalogModel {
	upstreamMu.RLock()
	defer upstreamMu.RUnlock()
	if l := upstreamLists; l != nil {
		return l.google
	}
	return GoogleModels
}

// legacyCatalogList is the list of retired models the Anthropic lookups
// extend themselves with.
func legacyCatalogList() []CatalogModel {
	upstreamMu.RLock()
	defer upstreamMu.RUnlock()
	if l := upstreamLists; l != nil {
		return l.legacy
	}
	return LegacyModels
}

// invalidateCatalogIndexes drops every memoised package-level index, so the
// next lookup rebuilds from the lists now installed. Called on every install,
// after upstreamMu has been released — never under it (catalog_lookup holds a
// memo lock across its rebuild, which reads these lists through
// upstreamMu.RLock).
func invalidateCatalogIndexes() {
	lookupCatalogMemo.invalidate()
	claudeCatalogMemo.invalidate()
	openAICatalogMemo.invalidate()
	googleCatalogMemo.invalidate()
}

// LoadPersistedUpstreamCatalog re-installs the last synced upstream catalogue
// from the persisted copy under the ogcode config dir — the boot path. A
// missing file is a clean no-op (nothing was ever synced); a corrupt one is
// an error and installs nothing, so garbage never reaches the catalogue.
func LoadPersistedUpstreamCatalog() error {
	path := upstreamCatalogPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("upstream catalog load %s: %w", path, err)
	}
	if _, err := ApplyUpstreamCatalog(data); err != nil {
		return fmt.Errorf("upstream catalog load %s: %w", path, err)
	}
	return nil
}

// UpstreamCatalogInfo reads the persisted copy's meta without touching the
// live merged layer: when the last sync landed (the stamped sync time, else
// the file's generatedAt), where it came from, and how many models it
// carries. ok is false when nothing is persisted, no home is known, or the
// file does not parse.
func UpstreamCatalogInfo() (syncedAt time.Time, source string, modelCount int, ok bool) {
	path := upstreamCatalogPath()
	if path == "" {
		return time.Time{}, "", 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, "", 0, false
	}
	var f upstreamCatalogFile
	if err := json.Unmarshal(data, &f); err != nil {
		return time.Time{}, "", 0, false
	}
	when := f.SyncedAt
	if when.IsZero() {
		when = f.GeneratedAt
	}
	return when, f.Source, len(f.Models), true
}
