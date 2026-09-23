package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// TestOGXAccountHasPlan pins the registration gate. The gateway's model
// catalogue IS the plan, so a link without one must not be treated as usable:
// it would register a provider that can serve nothing yet still outranks the
// other providers.
func TestOGXAccountHasPlan(t *testing.T) {
	cases := []struct {
		plan string
		want bool
	}{
		{plan: "ogx", want: true},
		{plan: "OGX", want: true},
		{plan: " OGX Pro ", want: true},
		{plan: "ogx-pro", want: true},
		{plan: "", want: false},
		{plan: "   ", want: false},
		{plan: "none", want: false},
		{plan: "NONE", want: false},
		{plan: " None ", want: false},
	}
	for _, c := range cases {
		a := &OGXAccount{Plan: c.plan}
		if got := a.HasPlan(); got != c.want {
			t.Errorf("HasPlan(plan=%q) = %v, want %v", c.plan, got, c.want)
		}
	}
	// A missing link is not a plan.
	var nilAcct *OGXAccount
	if nilAcct.HasPlan() {
		t.Error("nil account reported a plan")
	}
}

// TestOGXAccountPlanSurvivesRoundTrip keeps the store's contract honest: the
// gate reads the plan back out of the row the callback wrote.
func TestOGXAccountPlanSurvivesRoundTrip(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := SetOGXAccount(database, &OGXAccount{Token: "t", Email: "e@x", Plan: OGXPlanNone}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := GetOGXAccount(database)
	if err != nil || got == nil {
		t.Fatalf("get: %v, %v", got, err)
	}
	if got.HasPlan() {
		t.Errorf("plan %q read back as usable", got.Plan)
	}

	if err := SetOGXAccount(database, &OGXAccount{Token: "t", Plan: "ogx"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, _ = GetOGXAccount(database)
	if !got.HasPlan() {
		t.Errorf("plan %q read back as unusable", got.Plan)
	}

	if err := DeleteOGXAccount(database); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := GetOGXAccount(database); got != nil {
		t.Errorf("account survived delete: %+v", got)
	}
}
