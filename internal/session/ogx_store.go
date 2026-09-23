package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// OGXAccount is the stored link between this install and an OG Lab account
// holding an OGX plan. The token is the credential the web side handed back
// through the connect flow; email and plan are display fields for the
// settings screen. Kept in the global config DB — the plan follows the user,
// not the project.
type OGXAccount struct {
	Token         string `json:"token"`
	Email         string `json:"email"`
	Plan          string `json:"plan"`
	TimeConnected int64  `json:"timeConnected"`
}

// OGXPlanNone is the plan value the web side reports for an account that has
// signed in but holds no plan. It mirrors billing's PlanNone.
const OGXPlanNone = "none"

// HasPlan reports whether the linked account holds a plan that grants model
// access. A link without one is connected but unusable: the gateway would serve
// it an empty model catalogue. Callers use this to decide whether to register
// the OGX provider at all — registering a planless one would put it in
// ProviderPriority ahead of the other providers while it can serve nothing, and
// the user would land on a default that cannot answer.
//
// A missing plan field is treated the same as "none": both mean no usable plan,
// and being wrong in that direction merely leaves the other providers in place.
func (a *OGXAccount) HasPlan() bool {
	if a == nil {
		return false
	}
	plan := strings.ToLower(strings.TrimSpace(a.Plan))
	return plan != "" && plan != OGXPlanNone
}

// GetOGXAccount returns the stored account link, or nil when none has been
// connected. nil rather than a zero value: "no row" is the disconnected
// state, and callers branch on it.
func GetOGXAccount(database *db.DB) (*OGXAccount, error) {
	var a OGXAccount
	err := database.QueryRow(
		`SELECT token, email, plan, time_connected FROM ogx_account WHERE id = 1`,
	).Scan(&a.Token, &a.Email, &a.Plan, &a.TimeConnected)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get ogx account: %w", err)
	}
	return &a, nil
}

// SetOGXAccount stores the account link, replacing any previous one —
// reconnecting is how a user switches OG Lab accounts.
func SetOGXAccount(database *db.DB, a *OGXAccount) error {
	if a.TimeConnected == 0 {
		a.TimeConnected = time.Now().UnixMilli()
	}
	_, err := database.Exec(`
		INSERT INTO ogx_account (id, token, email, plan, time_connected)
		VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			token = excluded.token,
			email = excluded.email,
			plan = excluded.plan,
			time_connected = excluded.time_connected`,
		a.Token, a.Email, a.Plan, a.TimeConnected,
	)
	if err != nil {
		return fmt.Errorf("set ogx account: %w", err)
	}
	return nil
}

// DeleteOGXAccount removes the account link. The plan itself lives on the web
// side and is untouched; this only forgets the local connection.
func DeleteOGXAccount(database *db.DB) error {
	if _, err := database.Exec(`DELETE FROM ogx_account WHERE id = 1`); err != nil {
		return fmt.Errorf("delete ogx account: %w", err)
	}
	return nil
}
