package registry

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	bolt "go.etcd.io/bbolt"
)

// bbolt bucket names. `users` holds the operator accounts (Phase B). It is
// always created by Open — the corrupt-file policy is bucket-aware at load
// time, not by bucket presence.
var (
	bucketWorkers  = []byte("workers")
	bucketTokens   = []byte("tokens")
	bucketSessions = []byte("sessions")
	bucketUsers    = []byte("users")
)

// ErrCorrupt is returned by Open when the DB file on disk is structurally
// unusable (truncated or garbage). It exists so the caller can apply the A2
// boot policy — move the file aside and start empty — instead of treating it
// like any other open failure. Open returns this BEFORE bbolt opens the file,
// because bbolt itself segfaults on a byte-truncated file (freelist page past
// EOF is read through the mmap with no bounds check).
var ErrCorrupt = errors.New("corrupt control-plane database")

// UserRecord is the persisted form of one operator account. Only a bcrypt hash
// is stored — never the raw password — so a leaked DB yields hashes, not
// credentials. Workspaces is an optional per-user allowlist of workspace
// identifiers (a route label, a workspace name, or a workspace path suffix):
// empty/nil means the account may open ANY workspace, which preserves the
// behaviour of accounts created before the allowlist existed.
//
// Admin marks an administrator: an account that may see every worker and
// session on the console. It is a *bool so a pre-admin-era record (field
// absent) unmarshals to nil — meaning admin — while explicitly created
// non-admin accounts store false.
//
// Repos is the set of repositories a plain user is assigned to; the user gets
// one git worktree off each clone. It supersedes the scalar Repo, which is kept
// only so records written before multi-repo assignment still read correctly —
// AllRepos merges the two and every write persists the merged set into Repos.
// A repo-scoped account with no explicit Workspaces allowlist is auto-scoped at
// login to its own worktree tunnels (one allowlist entry — the user slug —
// already matches its worktree in every assigned repo, because the worker names
// each worktree dir after the user). BaseBranch records the branch the user's
// most recent worktree was cut from (empty = the repo's default branch; display
// only). Email is the contact address captured on the console's add-account
// form (display only — the login identifier stays the account name).
type UserRecord struct {
	Hash       string    `json:"hash"`
	CreatedAt  time.Time `json:"createdAt"`
	Workspaces []string  `json:"workspaces,omitempty"`
	Admin      *bool     `json:"admin,omitempty"`
	Repos      []string  `json:"repos,omitempty"`
	Repo       string    `json:"repo,omitempty"` // legacy single-repo field; read via AllRepos
	Email      string    `json:"email,omitempty"`
	BaseBranch string    `json:"baseBranch,omitempty"`
}

// IsAdmin reports whether the account is an administrator. Records written
// before the admin flag existed are administrators by definition — the
// operator password they were created under predated per-user scoping.
func (r UserRecord) IsAdmin() bool { return r.Admin == nil || *r.Admin }

// AllRepos returns the account's assigned repositories, merging the multi-repo
// Repos field with the legacy scalar Repo (de-duplicated, Repos first). Empty
// when the account is assigned to none.
func (r UserRecord) AllRepos() []string {
	if r.Repo == "" {
		return r.Repos
	}
	for _, u := range r.Repos {
		if u == r.Repo {
			return r.Repos // legacy value already present in the multi-repo set
		}
	}
	return append(append(make([]string, 0, len(r.Repos)+1), r.Repos...), r.Repo)
}

// WithRepoAdded returns a copy of the record with repoURL added to its
// repository set (idempotent) and the legacy scalar folded in, so callers
// persist a single canonical Repos slice. report(added) tells whether repoURL
// was new.
func (r UserRecord) WithRepoAdded(repoURL string) (rec UserRecord, added bool) {
	merged := r.AllRepos()
	for _, u := range merged {
		if u == repoURL {
			r.Repos, r.Repo = merged, ""
			return r, false
		}
	}
	r.Repos = append(append(make([]string, 0, len(merged)+1), merged...), repoURL)
	r.Repo = ""
	return r, true
}

// WithRepoRemoved returns a copy of the record with repoURL dropped from its
// repository set (folding in the legacy scalar). reportRemoved tells whether
// repoURL was present.
func (r UserRecord) WithRepoRemoved(repoURL string) (rec UserRecord, removed bool) {
	merged := r.AllRepos()
	out := make([]string, 0, len(merged))
	for _, u := range merged {
		if u == repoURL {
			removed = true
			continue
		}
		out = append(out, u)
	}
	r.Repos, r.Repo = out, ""
	return r, removed
}

// workerRecord is the persisted form of a worker: its info plus its current
// token. Status/LastSeen/conn are LIVE state and deliberately excluded — they
// are re-derived at restore time (Restore). Persisting the token's ExpiresAt
// keeps a restored token valid within its TTL, which is the whole point of
// master-side persistence (Phase C).
type workerRecord struct {
	Info  WorkerInfo    `json:"info"`
	Token pairing.Token `json:"token"`
}

// Snapshot is the raw persisted registry state, decoded by Store.Load and fed
// to Registry.Restore. This is a copy, not the live maps.
type Snapshot struct {
	Workers  map[string]workerRecord // workerID -> record
	Tokens   map[string]string       // token value -> workerID (the token index)
	Sessions map[string]string       // sessionID -> workerID (the routing table)
}

// Store is a bbolt-backed persistence layer for the registry's three maps. All
// values are plain JSON blobs — no ORM, no schema-migration machinery for v1.
// bbolt is single-writer, and every mutating Registry call already holds the
// registry lock, so transactions here never contend with each other.
type Store struct {
	db   *bolt.DB
	path string
}

// Open opens (creating if needed) the control-plane DB at path and ensures the
// registry buckets exist. The file is created 0600, mirroring ogcode's own
// ~/.ogcode/ogcode.db. The parent directory is created if missing.
//
// A structurally corrupt existing file (truncated or garbage) returns ErrCorrupt
// without bbolt opening it — a truncated file would segfault the process inside
// bbolt's freelist loader, so corruption must be detected here first. An empty
// (or absent) file opens clean and is initialised by this call.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}
	if err := checkDBFile(path); err != nil {
		return nil, err // ErrCorrupt for garbage/truncated, nil for empty
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketWorkers, bucketTokens, bucketSessions, bucketUsers} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialise registry buckets: %w", err)
	}
	return &Store{db: db, path: path}, nil
}

// checkDBFile validates an existing DB file structurally before bbolt maps it.
// It performs the bbolt meta-page sanity checks (magic, version, page bounds,
// freelist bound, meta checksum) on the freshest meta page so a truncated or
// garbage file is refused up front instead of segfaulting the process inside
// bbolt's freelist loader. A missing or empty file passes — bbolt initialises
// those.
func checkDBFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // absent file: bbolt creates it
		}
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() == 0 {
		return nil // empty file: bbolt initialises it
	}
	size := fi.Size()
	data := make([]byte, size)
	if _, err := io.ReadFull(f, data); err != nil {
		return err
	}
	// Validate both meta pages (pageSize is unknown until we read one; bbolt
	// tries the first page, then the second at guessed offsets). Mirror the
	// essential checks bbolt's own meta.Validate does so we never hand bbolt a
	// page it would read garbage from.
	if !validMetaPage(data, 0) && !validMetaPageAtBoltOffsets(data, size) {
		return ErrCorrupt
	}
	return nil
}

// validMetaPage reports whether the byte slice holds a structurally sound bbolt
// meta page at offset off. The page header is 16 bytes, then the Meta struct: it
// checks magic, version, plausible page size, that the high-water mark (pgid)
// and freelist page fit within the file, and that the stored checksum matches.
func validMetaPage(data []byte, off int64) bool {
	const (
		pageHeader = 16                 // Page struct header before the Meta struct
		metaSize   = 64                 // Meta struct size (through the checksum)
		metaMagic  = 0xED0CDAED         // common.Magic
		metaVer    = 2                  // common.Version
		pending    = 0xffffffffffffffff // common.PgidNoFreelist
	)
	if int64(len(data)) < off+pageHeader+metaSize {
		return false
	}
	m := off + pageHeader
	if binary.LittleEndian.Uint32(data[m:]) != metaMagic {
		return false
	}
	if binary.LittleEndian.Uint32(data[m+4:]) != metaVer {
		return false
	}
	pageSize := binary.LittleEndian.Uint32(data[m+8:])
	if pageSize < 1024 || pageSize > 64*1024 || pageSize&(pageSize-1) != 0 {
		return false
	}
	pages := uint64(len(data)) / uint64(pageSize)
	freelist := binary.LittleEndian.Uint64(data[m+32:])
	pgid := binary.LittleEndian.Uint64(data[m+40:])
	// High-water mark must be within the mapped file, else a subsequent read
	// walks off the end of the mmap. The freelist page must likewise be bounded
	// (bbolt reads it with no bounds check).
	if pgid > pages {
		return false
	}
	if freelist != pending && freelist >= pgid {
		return false
	}
	return true
}

// validMetaPageAtBoltOffsets probes the second meta page the way bbolt does: at
// candidate page-size offsets (1KB..16MB), returning true if any is a sound meta
// page. bbolt reads the size from the first page when it can; this is the
// fallback for a file whose first page is unreadable but whose second is intact.
func validMetaPageAtBoltOffsets(data []byte, size int64) bool {
	for i := 0; i <= 14; i++ {
		pos := int64(1024 << uint(i))
		if pos+1024 >= size {
			break
		}
		if validMetaPage(data, pos) {
			return true
		}
	}
	return false
}

// Path returns the absolute DB file path this store opened.
func (s *Store) Path() string { return s.path }

// Load decodes the three buckets into a Snapshot.
func (s *Store) Load() (*Snapshot, error) {
	snap := &Snapshot{
		Workers:  map[string]workerRecord{},
		Tokens:   map[string]string{},
		Sessions: map[string]string{},
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucketWorkers).ForEach(func(k, v []byte) error {
			var rec workerRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				return fmt.Errorf("decode worker %q: %w", k, err)
			}
			snap.Workers[string(k)] = rec
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Bucket(bucketTokens).ForEach(func(k, v []byte) error {
			snap.Tokens[string(k)] = string(v)
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Bucket(bucketSessions).ForEach(func(k, v []byte) error {
			snap.Sessions[string(k)] = string(v)
			return nil
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// ReplaceWorker atomically writes the net effect of a registration (Add): the
// worker record, the new token index entry, and the deletion of the replaced
// token's entry. One transaction so a crash mid-registration never leaves an
// orphaned token index entry whose old token still resolves to the worker.
func (s *Store) ReplaceWorker(id string, rec workerRecord, oldTokenValue string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if oldTokenValue != "" {
			if err := tx.Bucket(bucketTokens).Delete([]byte(oldTokenValue)); err != nil {
				return err
			}
		}
		v, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("encode worker %q: %w", id, err)
		}
		if err := tx.Bucket(bucketWorkers).Put([]byte(id), v); err != nil {
			return err
		}
		return tx.Bucket(bucketTokens).Put([]byte(rec.Token.Value), []byte(id))
	})
}

// UpdateToken rotates a worker's token: the old token value stops resolving to
// the worker and the new one starts. Both the workers bucket record (so the
// restored token stays current) and the token index are updated.
func (s *Store) UpdateToken(id, oldTokenValue string, nt pairing.Token) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if oldTokenValue != "" {
			if err := tx.Bucket(bucketTokens).Delete([]byte(oldTokenValue)); err != nil {
				return err
			}
		}
		if err := tx.Bucket(bucketTokens).Put([]byte(nt.Value), []byte(id)); err != nil {
			return err
		}
		var rec workerRecord
		if v := tx.Bucket(bucketWorkers).Get([]byte(id)); v != nil {
			if err := json.Unmarshal(v, &rec); err != nil {
				return fmt.Errorf("decode worker %q: %w", id, err)
			}
		}
		rec.Token = nt
		v, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("encode worker %q: %w", id, err)
		}
		return tx.Bucket(bucketWorkers).Put([]byte(id), v)
	})
}

// RouteSession records that sessionID is hosted by workerID.
func (s *Store) RouteSession(sessionID, workerID string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSessions).Put([]byte(sessionID), []byte(workerID))
	})
}

// UnrouteSession forgets a session's routing.
func (s *Store) UnrouteSession(sessionID string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSessions).Delete([]byte(sessionID))
	})
}

// BackupTo writes a consistent point-in-time copy of the DB to dst. bbolt's
// copy-on-write means the running DB stays live during the copy. Used on
// graceful shutdown as a single forensic recovery point; the real durability
// story is the running transactions.
func (s *Store) BackupTo(dst string) error {
	return s.db.View(func(tx *bolt.Tx) error {
		return tx.CopyFile(dst, 0o600)
	})
}

// Close releases the DB file.
func (s *Store) Close() error {
	return s.db.Close()
}

// PutUser stores an account record keyed by username. The value must be a
// bcrypt hash (never the raw password). Re-adding an existing name overwrites
// it (a password reset for that account).
func (s *Store) PutUser(username string, rec UserRecord) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		v, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("encode user %q: %w", username, err)
		}
		return tx.Bucket(bucketUsers).Put([]byte(username), v)
	})
}

// GetUser returns the stored account for username, or ok=false if absent.
func (s *Store) GetUser(username string) (UserRecord, bool, error) {
	var rec UserRecord
	found := false
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketUsers).Get([]byte(username))
		if v == nil {
			return nil
		}
		if err := json.Unmarshal(v, &rec); err != nil {
			return fmt.Errorf("decode user %q: %w", username, err)
		}
		found = true
		return nil
	})
	if err != nil {
		return UserRecord{}, false, err
	}
	return rec, found, nil
}

// DeleteUser removes an account by username, reporting whether one existed.
func (s *Store) DeleteUser(username string) (bool, error) {
	removed := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		if v := tx.Bucket(bucketUsers).Get([]byte(username)); v != nil {
			if err := tx.Bucket(bucketUsers).Delete([]byte(username)); err != nil {
				return err
			}
			removed = true
		}
		return nil
	})
	return removed, err
}

// UserExists reports whether an account exists (the A1 validate-time lookup).
func (s *Store) UserExists(username string) (bool, error) {
	exists := false
	err := s.db.View(func(tx *bolt.Tx) error {
		exists = tx.Bucket(bucketUsers).Get([]byte(username)) != nil
		return nil
	})
	return exists, err
}

// HasUsers reports whether the users bucket is non-empty. Used by auth to pick
// ModeAccounts over ModeLegacy at gate construction.
func (s *Store) HasUsers() (bool, error) {
	exists := false
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketUsers).Cursor()
		k, _ := c.First()
		exists = k != nil
		return nil
	})
	return exists, err
}

// GetPasswordHash returns the stored bcrypt hash for username, whether the
// account exists, and any lookup error. This is the narrow seam the auth gate
// needs — it deliberately does not surface the full UserRecord so auth stays
// decoupled from registry's schema.
func (s *Store) GetPasswordHash(username string) (hash string, exists bool, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketUsers).Get([]byte(username))
		if v == nil {
			return nil
		}
		var rec UserRecord
		if err := json.Unmarshal(v, &rec); err != nil {
			return fmt.Errorf("decode user %q: %w", username, err)
		}
		hash = rec.Hash
		exists = true
		return nil
	})
	return hash, exists, err
}

// GetUserWorkspaces returns the account's workspace allowlist — nil/empty when
// the account may open any workspace. A missing account also returns nil with
// no error: absence is "unrestricted" here, and callers that need to
// distinguish missing accounts already ask UserExists first (the auth gate
// does, before this seam is ever consulted — see the A1 note in internal/auth).
func (s *Store) GetUserWorkspaces(username string) ([]string, error) {
	var workspaces []string
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketUsers).Get([]byte(username))
		if v == nil {
			return nil
		}
		var rec UserRecord
		if err := json.Unmarshal(v, &rec); err != nil {
			return fmt.Errorf("decode user %q: %w", username, err)
		}
		workspaces = rec.Workspaces
		return nil
	})
	if err != nil {
		return nil, err
	}
	return workspaces, nil
}

// ListUsers returns all account names (sorted), for the `users list` CLI.
func (s *Store) ListUsers() ([]string, error) {
	var names []string
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketUsers).ForEach(func(k, _ []byte) error {
			names = append(names, string(k))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}
