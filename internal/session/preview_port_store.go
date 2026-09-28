package session

import (
	"database/sql"
	"errors"
)

// Announced preview ports are the services a project's agent handed the user a
// live-preview URL for, plus any the user added on the Preview page. The agent
// writes that URL into its own prose when it starts a service, so the
// announcement is durable — a preview keeps showing up after a restart — and a
// port a tool merely printed is never mistaken for one (see
// internal/server/preview_ports.go, which reads what this records).
//
// The record is also the preview proxy's allowlist: a port is served at its
// preview hostname only while it is recorded here. The directory scopes which
// project's grid lists a port; publication itself is per server — this DB
// belongs to one server — so the proxy asks about a port in any directory, and
// forgetting a port forgets it in all of them.

// RecordAnnouncedPort records that a service on port was announced in directory.
// Re-announcing a port is idempotent: the first time wins, so a long-lived
// session records a service once. Nil-safe, so a caller without a store can
// record nothing rather than panicking.
func (s *Store) RecordAnnouncedPort(directory string, port int) error {
	if s == nil || s.db == nil || directory == "" || port < 1 || port > 65535 {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO announced_preview_port (directory, port, time_created)
		 VALUES (?, ?, ?)`,
		directory, port, Now(),
	)
	return err
}

// AnnouncedPorts returns the ports announced in directory, sorted ascending. A
// missing or empty directory yields nothing, as does a nil store.
func (s *Store) AnnouncedPorts(directory string) ([]int, error) {
	if s == nil || s.db == nil || directory == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT port FROM announced_preview_port WHERE directory = ? ORDER BY port ASC`,
		directory,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ports []int
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		ports = append(ports, p)
	}
	return ports, rows.Err()
}

// PreviewPortPublished reports whether port is recorded in any directory — the
// question the preview proxy asks before it forwards a request. A nil store
// publishes nothing, so a server without one refuses every preview rather than
// serving every port.
func (s *Store) PreviewPortPublished(port int) (bool, error) {
	if s == nil || s.db == nil || port < 1 || port > 65535 {
		return false, nil
	}
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM announced_preview_port WHERE port = ? LIMIT 1`, port,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ForgetAnnouncedPort removes port from every directory's record, which takes
// its tile off the Preview grid and stops the proxy serving it. Forgetting a
// port that was never recorded is not an error.
func (s *Store) ForgetAnnouncedPort(port int) error {
	if s == nil || s.db == nil || port < 1 || port > 65535 {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM announced_preview_port WHERE port = ?`, port)
	return err
}
