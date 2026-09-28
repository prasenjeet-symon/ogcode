package usage

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func openDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func rowCount(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM usage_event`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// claude is a catalogued, metered model the tests price against, read from
// the catalogue rather than pinned, so a price update does not break them.
func claude(t *testing.T) (string, provider.ModelPrice) {
	t.Helper()
	for _, m := range provider.AnthropicModels {
		if m.InputPricePerM > 0 && m.OutputPricePerM > 0 {
			price, ok := provider.NewRegistry().PriceOn("anthropic", m.ID)
			if !ok {
				t.Fatalf("PriceOn(anthropic, %s) has no price", m.ID)
			}
			return m.ID, price
		}
	}
	t.Fatal("no priced Claude model")
	return "", provider.ModelPrice{}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// A step reported twice keeps its final counts: the ledger upserts by message
// id instead of adding a second row.
func TestRecordStepReplacesRatherThanDoubles(t *testing.T) {
	d := openDB(t)
	l := NewLedger(d, "/work/app")
	l.RecordStep("ses_1", "msg_1", "anthropic", "claude", session.TokenCounts{Input: 10, Output: 5}, 1)
	l.RecordStep("ses_1", "msg_1", "anthropic", "claude", session.TokenCounts{Input: 40, Output: 9}, 1)
	if n := rowCount(t, d); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
	var in, out int
	if err := d.QueryRow(`SELECT input, output FROM usage_event WHERE id = 'msg_1'`).Scan(&in, &out); err != nil {
		t.Fatal(err)
	}
	if in != 40 || out != 9 {
		t.Errorf("row = in %d out %d, want the final 40/9", in, out)
	}
}

// Utility calls have no natural key, so each is its own row; a call that
// reported nothing, and a nil ledger, write nothing.
func TestRecordUtilityAppendsAndSkipsEmpty(t *testing.T) {
	d := openDB(t)
	l := NewLedger(d, "/work/app")
	l.RecordUtility("ses_1", "ollama", "glm", session.TokenCounts{Input: 3}, 1)
	l.RecordUtility("ses_1", "ollama", "glm", session.TokenCounts{Input: 3}, 2)
	l.RecordUtility("ses_1", "ollama", "glm", session.TokenCounts{}, 3)
	var nilLedger *Ledger
	nilLedger.RecordUtility("ses_1", "ollama", "glm", session.TokenCounts{Input: 3}, 4)
	nilLedger.RecordStep("ses_1", "msg_9", "ollama", "glm", session.TokenCounts{Input: 3}, 4)
	if n := rowCount(t, d); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

// Backfill copies a project's history once: steps at the model their message
// names or else their session's, and the session's utility total as one row.
func TestBackfillCopiesHistoryOnce(t *testing.T) {
	d := openDB(t)
	store := session.NewStore(d)
	sess := &session.Session{ID: session.NewSessionID(), Directory: "/work/app", Model: "sess-model", Provider: "ollama",
		CreatedAt: 1, UpdatedAt: 50}
	if err := store.Create(sess); err != nil {
		t.Fatal(err)
	}
	addStep := func(model, prov string, tc *session.TokenCounts, role session.MessageRole) session.MessageID {
		m := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: role, Model: model, Provider: prov, Tokens: tc, CreatedAt: 10}
		if err := store.CreateMessage(m); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	legacy := addStep("", "", &session.TokenCounts{Input: 100, Output: 10}, session.RoleAssistant)
	stamped := addStep("other-model", "anthropic", &session.TokenCounts{Input: 7, Output: 1}, session.RoleAssistant)
	addStep("", "", nil, session.RoleUser) // a prompt carries no usage
	if err := store.AddUtilityUsage(sess.ID, session.TokenCounts{Input: 20, CacheRead: 5}); err != nil {
		t.Fatal(err)
	}

	l := NewLedger(d, "/work/app")
	steps, util, err := l.Backfill(store)
	if err != nil {
		t.Fatal(err)
	}
	if steps != 2 || util != 1 {
		t.Fatalf("backfill = %d steps, %d utility; want 2, 1", steps, util)
	}
	check := func(id, wantProvider, wantModel string) {
		t.Helper()
		var p, m string
		if err := d.QueryRow(`SELECT provider, model FROM usage_event WHERE id = ?`, id).Scan(&p, &m); err != nil {
			t.Fatalf("row %s: %v", id, err)
		}
		if p != wantProvider || m != wantModel {
			t.Errorf("row %s = %s/%s, want %s/%s", id, p, m, wantProvider, wantModel)
		}
	}
	check(string(legacy), "ollama", "sess-model")
	check(string(stamped), "anthropic", "other-model")
	check("backfill:"+string(sess.ID), "ollama", "sess-model")

	// Once done, the project is not copied again, even with new history.
	addStep("", "", &session.TokenCounts{Input: 1}, session.RoleAssistant)
	if steps, util, err := l.Backfill(store); err != nil || steps+util != 0 {
		t.Errorf("second backfill = %d+%d rows, %v; want nothing", steps, util, err)
	}
	if n := rowCount(t, d); n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}
}

// Summarize prices each model by how it is billed: metered at its price,
// plan and local usage at nothing but with its list-price value, and a model
// nobody prices left out of the cost and counted apart.
func TestSummarizePricesByBilling(t *testing.T) {
	d := openDB(t)
	claudeID, claudePrice := claude(t)
	now := time.Now().UnixMilli()
	a := NewLedger(d, "/work/app")
	b := NewLedger(d, "/work/other")

	metered := session.TokenCounts{Input: 1_000, Output: 500, CacheRead: 20_000}
	a.RecordStep("s1", "m1", "anthropic", claudeID, metered, now)
	included := session.TokenCounts{Input: 360_350, Output: 19_631, CacheRead: 1_379_072}
	b.RecordStep("s2", "m2", "ollama", "glm-5.3-flash:cloud", included, now)
	unpriced := session.TokenCounts{Input: 50, Output: 5}
	b.RecordUtility("s2", "openai", "house-model-x", unpriced, now)

	sum, err := Summarize(d, provider.NewRegistry(), 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Models) != 3 {
		t.Fatalf("models = %d, want 3", len(sum.Models))
	}
	by := map[string]ModelUsage{}
	for _, m := range sum.Models {
		by[m.Model] = m
	}

	mc := by[claudeID]
	wantCost := claudePrice.Cost(metered.Input, metered.Output, metered.CacheRead, metered.CacheWrite)
	if mc.Billing != BillingMetered || mc.CostUSD == nil || !near(*mc.CostUSD, wantCost) || mc.ListUSD != nil {
		t.Errorf("metered = %+v, want billing metered, cost %v, no list value", mc, wantCost)
	}

	mi := by["glm-5.3-flash:cloud"]
	list, _ := provider.ListPrice("glm-5.3-flash:cloud")
	wantList := list.Cost(included.Input, included.Output, included.CacheRead, included.CacheWrite)
	if mi.Billing != BillingIncluded || mi.CostUSD == nil || *mi.CostUSD != 0 || mi.ListUSD == nil || !near(*mi.ListUSD, wantList) {
		t.Errorf("included = %+v, want billing included, cost 0, list %v", mi, wantList)
	}
	if mi.Effective != included.Effective() || mi.CacheRead != included.CacheRead {
		t.Errorf("included tokens = %+v", mi.Tokens)
	}

	mu := by["house-model-x"]
	if mu.Billing != BillingUnpriced || mu.CostUSD != nil {
		t.Errorf("unpriced = %+v, want billing unpriced and no cost", mu)
	}

	tot := sum.Totals
	if !near(tot.CostUSD, wantCost) || !near(tot.IncludedListUSD, wantList) {
		t.Errorf("totals cost %v list %v, want %v and %v", tot.CostUSD, tot.IncludedListUSD, wantCost, wantList)
	}
	if tot.UnpricedEffective != unpriced.Effective() || tot.IncludedEffective != included.Effective() {
		t.Errorf("totals unpriced %d included %d", tot.UnpricedEffective, tot.IncludedEffective)
	}
	if tot.Sessions != 2 || tot.Calls != 3 {
		t.Errorf("totals sessions %d calls %d, want 2 and 3", tot.Sessions, tot.Calls)
	}
	if len(sum.Projects) != 2 || len(sum.Days) != 1 || sum.First != now {
		t.Errorf("projects %d days %d first %d; want 2, 1, %d", len(sum.Projects), len(sum.Days), sum.First, now)
	}

	// A window that ends before anything was spent is empty.
	if empty, err := Summarize(d, provider.NewRegistry(), 0, now-1, ""); err != nil || len(empty.Models) != 0 {
		t.Errorf("early window = %+v, %v; want no models", empty, err)
	}
}

// ForSession prices each step at the model that answered it, falls back to the
// session's model for steps from before messages recorded one, and prices the
// utility total at the session's model.
func TestForSessionPricesEachStepAtItsModel(t *testing.T) {
	d := openDB(t)
	claudeID, claudePrice := claude(t)
	store := session.NewStore(d)
	sess := &session.Session{ID: session.NewSessionID(), Directory: "/work/app", Model: "glm-5.3-flash:cloud", Provider: "ollama",
		CreatedAt: 1, UpdatedAt: 1}
	if err := store.Create(sess); err != nil {
		t.Fatal(err)
	}
	add := func(model, prov string, tc session.TokenCounts) {
		m := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleAssistant,
			Model: model, Provider: prov, Tokens: &tc, CreatedAt: 2}
		if err := store.CreateMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	add("", "", session.TokenCounts{Input: 1000, Output: 100})                      // legacy: the session's model
	add("glm-5.3-flash:cloud", "ollama", session.TokenCounts{Input: 10, Output: 1}) // stamped, same model
	add(claudeID, "anthropic", session.TokenCounts{Input: 2000, Output: 200})       // switched mid-session
	if err := store.AddUtilityUsage(sess.ID, session.TokenCounts{Input: 5}); err != nil {
		t.Fatal(err)
	}

	cost, err := ForSession(store, provider.NewRegistry(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cost.Models) != 2 {
		t.Fatalf("models = %+v, want the session's model and Claude", cost.Models)
	}
	for _, m := range cost.Models {
		switch m.Model {
		case "glm-5.3-flash:cloud":
			if m.Input != 1015 || m.Calls != 3 || m.Billing != BillingIncluded {
				t.Errorf("session model = %+v, want input 1015 over 3 calls, included", m)
			}
		case claudeID:
			want := claudePrice.Cost(2000, 200, 0, 0)
			if m.CostUSD == nil || !near(*m.CostUSD, want) {
				t.Errorf("claude cost = %v, want %v", m.CostUSD, want)
			}
		default:
			t.Errorf("unexpected model %q", m.Model)
		}
	}
	if !near(cost.Totals.CostUSD, claudePrice.Cost(2000, 200, 0, 0)) || cost.Totals.IncludedListUSD <= 0 {
		t.Errorf("totals = %+v", cost.Totals)
	}
}

// A project summary holds that project's rows and its task worktrees' and
// nothing from a neighbour whose path merely starts the same; the all-projects
// summary folds each worktree into the project it belongs to.
func TestSummarizeNarrowsToOneProject(t *testing.T) {
	d := openDB(t)
	now := time.Now().UnixMilli()
	tc := session.TokenCounts{Input: 100, Output: 10}
	NewLedger(d, "/work/app").RecordStep("s1", "m1", "ollama", "glm", tc, now)
	NewLedger(d, "/work/app/.ogcode/worktrees/og-task-1").RecordStep("s2", "m2", "ollama", "glm", tc, now)
	NewLedger(d, "/work/app-two").RecordStep("s3", "m3", "ollama", "glm", tc, now)
	NewLedger(d, "/work/a_p").RecordStep("s4", "m4", "ollama", "glm", tc, now) // "_" must not act as a wildcard

	one, err := Summarize(d, provider.NewRegistry(), 0, 0, "/work/app")
	if err != nil {
		t.Fatal(err)
	}
	if one.Totals.Input != 200 || one.Totals.Sessions != 2 || one.Project != "/work/app" {
		t.Errorf("project summary = input %d over %d sessions (%q); want 200 over 2 for /work/app",
			one.Totals.Input, one.Totals.Sessions, one.Project)
	}
	if other, _ := Summarize(d, provider.NewRegistry(), 0, 0, "/work/a_p"); other.Totals.Input != 100 {
		t.Errorf("/work/a_p input = %d, want only its own 100", other.Totals.Input)
	}

	all, err := Summarize(d, provider.NewRegistry(), 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]int{}
	for _, p := range all.Projects {
		names[p.Project] = p.Input
	}
	if len(names) != 3 || names["/work/app"] != 200 {
		t.Errorf("projects = %v, want 3 with the worktree folded into /work/app (200)", names)
	}
}

// stubProvider serves a fixed list of models, to stand in for a configured
// provider when inferring where provider-less work ran.
type stubProvider struct {
	id     string
	models []string
}

func (s stubProvider) ID() string { return s.id }
func (s stubProvider) Models() []provider.ModelInfo {
	out := make([]provider.ModelInfo, 0, len(s.models))
	for _, m := range s.models {
		out = append(out, provider.ModelInfo{ID: m, ProviderID: s.id})
	}
	return out
}
func (s stubProvider) StreamChat(context.Context, provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}

// Work recorded without a provider is priced as the configured provider that
// serves its model, and marked as inferred; when none does it is "unknown"
// and never counted as billed — even for a model the catalogue prices, which
// is exactly the case that used to be billed at list price by default.
func TestProviderlessUsageIsInferredOrUnknownNeverAssumedBilled(t *testing.T) {
	d := openDB(t)
	claudeID, _ := claude(t)
	now := time.Now().UnixMilli()
	l := NewLedger(d, "/work/app")
	served := session.TokenCounts{Input: 1000, Output: 100, CacheRead: 5000}
	l.RecordStep("s1", "m1", "", "glm-5.3-flash", served, now)
	l.RecordStep("s2", "m2", "", claudeID, session.TokenCounts{Input: 700, Output: 70}, now)
	l.RecordStep("s3", "m3", "", "mystery-model", session.TokenCounts{Input: 30, Output: 3}, now)

	reg := provider.NewRegistry()
	reg.Register(stubProvider{id: "ollama", models: []string{"glm-5.3-flash"}})
	sum, err := Summarize(d, reg, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ModelUsage{}
	for _, m := range sum.Models {
		by[m.Model] = m
	}

	glm := by["glm-5.3-flash"]
	list, _ := provider.ListPrice("glm-5.3-flash")
	wantList := list.Cost(served.Input, served.Output, served.CacheRead, served.CacheWrite)
	if glm.Billing != BillingIncluded || glm.InferredProvider != "ollama" || glm.ListUSD == nil || !near(*glm.ListUSD, wantList) {
		t.Errorf("served model = %+v; want included, inferred as ollama, list %v", glm, wantList)
	}
	for _, id := range []string{claudeID, "mystery-model"} {
		if m := by[id]; m.Billing != BillingUnknown || m.CostUSD != nil || m.InferredProvider != "" {
			t.Errorf("%s = %+v; want unknown billing and no cost", id, m)
		}
	}
	if sum.Totals.CostUSD != 0 {
		t.Errorf("billed = %v, want 0: nothing here is known to be billed", sum.Totals.CostUSD)
	}
	if want := 770 + 33; sum.Totals.UnknownEffective != want {
		t.Errorf("unknown effective = %d, want %d", sum.Totals.UnknownEffective, want)
	}
}

// hostedProvider is a stubProvider with an endpoint, so EndpointHost can name it.
type hostedProvider struct {
	stubProvider
	base string
}

func (h hostedProvider) BaseURL() string { return h.base }

// A row records the host its provider calls, so a slot repointed later keeps
// its history. Rows from before hosts were recorded take where the provider
// points now, and merge with the recorded rows for that host; a different
// recorded host stays its own row.
func TestSummarizeGroupsByRecordedHost(t *testing.T) {
	d := openDB(t)
	now := time.Now().UnixMilli()
	reg := provider.NewRegistry()
	reg.Register(hostedProvider{stubProvider{id: "openai", models: []string{"glm-5.3-flash"}}, "https://api.z.ai/api/paas/v4"})

	l := NewLedger(d, "/work/app")
	l.SetHosts(reg.EndpointHost)
	tc := session.TokenCounts{Input: 100, Output: 10}
	l.RecordStep("s1", "m1", "openai", "glm-5.3-flash", tc, now) // recorded: api.z.ai
	var host string
	if err := d.QueryRow(`SELECT host FROM usage_event WHERE id = 'm1'`).Scan(&host); err != nil || host != "api.z.ai" {
		t.Fatalf("recorded host = %q, %v; want api.z.ai", host, err)
	}
	// An older row with no host, and one recorded when the slot pointed elsewhere.
	if _, err := d.Exec(`INSERT INTO usage_event (id, kind, project, session_id, provider, model, input, output, time_created, host)
		VALUES ('m2', 'step', '/work/app', 's2', 'openai', 'glm-5.3-flash', 50, 5, ?, ''),
		       ('m3', 'step', '/work/app', 's3', 'openai', 'glm-5.3-flash', 7, 1, ?, 'router.local')`, now, now); err != nil {
		t.Fatal(err)
	}

	sum, err := Summarize(d, reg, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ModelUsage{}
	for _, m := range sum.Models {
		by[m.Host] = m
	}
	if len(sum.Models) != 2 {
		t.Fatalf("models = %+v; want one row per host", sum.Models)
	}
	if z := by["api.z.ai"]; z.Input != 150 || z.Sessions != 2 {
		t.Errorf("api.z.ai row = input %d over %d sessions; want 150 over 2 (recorded + fallback)", z.Input, z.Sessions)
	}
	if r := by["router.local"]; r.Input != 7 || r.Sessions != 1 {
		t.Errorf("router.local row = %+v; want its own 7-token row", r)
	}
}
