package session

import "fmt"

// ActiveDays counts the distinct sessions with activity on each local day
// between from and to (unix ms; from inclusive, to exclusive), keyed
// YYYY-MM-DD as the server's local calendar day. A session is active on the
// day it was created and again on every day it has a message, but counts once
// per day however many messages it wrote. Note, index and search sessions are
// skipped, exactly as List skips them, and only sessions of this workspace
// directory count.
//
// Days with no activity are absent from the map: it is sparse.
func (s *Store) ActiveDays(directory string, from, to int64) (map[string]int, error) {
	rows, err := s.db.Query(`
		SELECT day, COUNT(DISTINCT session_id) FROM (
			SELECT strftime('%Y-%m-%d', s.time_created / 1000, 'unixepoch', 'localtime') AS day, s.id AS session_id
			FROM session s
			WHERE s.directory = ? AND s.session_type NOT IN ('note', 'index', 'search')
			  AND s.time_created >= ? AND s.time_created < ?
			UNION
			SELECT strftime('%Y-%m-%d', m.time_created / 1000, 'unixepoch', 'localtime') AS day, m.session_id
			FROM message m JOIN session s ON s.id = m.session_id
			WHERE s.directory = ? AND s.session_type NOT IN ('note', 'index', 'search')
			  AND m.time_created >= ? AND m.time_created < ?
		)
		GROUP BY day`,
		directory, from, to, directory, from, to)
	if err != nil {
		return nil, fmt.Errorf("active days: %w", err)
	}
	defer rows.Close()

	days := map[string]int{}
	for rows.Next() {
		var day string
		var sessions int
		if err := rows.Scan(&day, &sessions); err != nil {
			return nil, err
		}
		days[day] = sessions
	}
	return days, rows.Err()
}
