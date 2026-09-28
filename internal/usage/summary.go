package usage

import (
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// How a model's tokens are paid for.
const (
	// BillingMetered is billed per token at a known price.
	BillingMetered = "metered"
	// BillingIncluded is a flat plan (OGX, Ollama Cloud) or a local model:
	// nothing is billed per token.
	BillingIncluded = "included"
	// BillingUnpriced is billed per token at a price nothing publishes.
	BillingUnpriced = "unpriced"
	// BillingUnknown is work whose provider was never recorded (sessions from
	// before ogcode stored one) on a model nothing configured serves today, so
	// whether it was billed at all cannot be told. It is never counted as a
	// charge.
	BillingUnknown = "unknown"
)

// Tokens are the counts in one bucket. Effective is everything but cache reads
// and Total is everything, as session.TokenCounts defines them.
type Tokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Reasoning  int `json:"reasoning"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Effective  int `json:"effective"`
	Total      int `json:"total"`
}

func (t *Tokens) add(tc session.TokenCounts) {
	t.Input += tc.Input
	t.Output += tc.Output
	t.Reasoning += tc.Reasoning
	t.CacheRead += tc.CacheRead
	t.CacheWrite += tc.CacheWrite
	t.Effective += tc.Effective()
	t.Total += tc.Consumed()
}

func (t *Tokens) addTokens(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.Reasoning += o.Reasoning
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.Effective += o.Effective
	t.Total += o.Total
}

// ModelUsage is one provider/model pair's usage and what it cost.
type ModelUsage struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Name is the catalogue's display name, empty for a model it does not know.
	Name    string `json:"name,omitempty"`
	Billing string `json:"billing"`
	// InferredProvider is set when Provider is empty — the work predates
	// recorded providers — and a configured provider serves the model today:
	// the row is billed as that provider, and says the provider was inferred.
	InferredProvider string `json:"inferredProvider,omitempty"`
	// Host is the endpoint the calls went to when the provider slot points
	// somewhere other than its own default (the OpenAI slot at Z.ai), so the
	// row can be named by it; empty on default endpoints. Rows from before
	// hosts were recorded take where the provider points today.
	Host string `json:"host,omitempty"`
	// Calls counts ledger rows: agent steps plus utility calls.
	Calls    int `json:"calls"`
	Sessions int `json:"sessions"`
	Tokens
	// CostUSD is what was billed per token: 0 for included usage, null when the
	// model is billed per token at an unknown price.
	CostUSD *float64 `json:"costUsd"`
	// ListUSD is included usage priced at the model's list price — what the
	// same tokens would cost on the vendor's own API. Null for metered usage,
	// which has a real cost, and when the catalogue does not price the model.
	ListUSD *float64 `json:"listUsd"`
}

// ProjectUsage is one project's share of the spend.
type ProjectUsage struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	Tokens
	CostUSD         float64 `json:"costUsd"`
	IncludedListUSD float64 `json:"includedListUsd"`
}

// Day is one calendar day of spend, in the server's local time.
type Day struct {
	Day             string  `json:"day"` // YYYY-MM-DD
	Input           int     `json:"input"`
	Output          int     `json:"output"`
	Effective       int     `json:"effective"`
	CostUSD         float64 `json:"costUsd"`
	IncludedListUSD float64 `json:"includedListUsd"`
}

// Totals add up every model in a view.
type Totals struct {
	Tokens
	Calls    int `json:"calls"`
	Sessions int `json:"sessions"`
	// CostUSD is everything billed per token at a known price.
	CostUSD float64 `json:"costUsd"`
	// IncludedListUSD is plan and local usage at list price, the part of the
	// work the flat plans covered.
	IncludedListUSD float64 `json:"includedListUsd"`
	// IncludedEffective is the effective tokens that were not billed per token.
	IncludedEffective int `json:"includedEffective"`
	// UnpricedEffective is the effective tokens billed per token at a price
	// nothing publishes, so CostUSD leaves them out.
	UnpricedEffective int `json:"unpricedEffective"`
	// UnknownEffective is the effective tokens whose provider was never
	// recorded and cannot be inferred, so CostUSD leaves them out too.
	UnknownEffective int `json:"unknownEffective"`
}

func (t *Totals) addModel(m ModelUsage) {
	t.Tokens.addTokens(m.Tokens)
	t.Calls += m.Calls
	switch m.Billing {
	case BillingMetered:
		if m.CostUSD != nil {
			t.CostUSD += *m.CostUSD
		}
	case BillingIncluded:
		t.IncludedEffective += m.Effective
		if m.ListUSD != nil {
			t.IncludedListUSD += *m.ListUSD
		}
	case BillingUnknown:
		t.UnknownEffective += m.Effective
	default:
		t.UnpricedEffective += m.Effective
	}
}

// Summary is the ledger totalled over a period.
type Summary struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
	// Project is the workspace the summary is narrowed to, empty for all.
	Project string `json:"project,omitempty"`
	// First is when the ledger's oldest row was spent, 0 when it is empty.
	First    int64          `json:"first"`
	Models   []ModelUsage   `json:"models"`
	Projects []ProjectUsage `json:"projects"`
	Days     []Day          `json:"days"`
	Totals   Totals         `json:"totals"`
}

// SessionCost is one session's spend by model.
type SessionCost struct {
	Models []ModelUsage `json:"models"`
	Totals Totals       `json:"totals"`
}

// pricing is how one provider/model pair is billed, resolved once per pair.
type pricing struct {
	billing  string
	price    provider.ModelPrice // the metered price
	list     provider.ModelPrice // the catalogue's list price, for included usage
	hasList  bool
	name     string
	inferred string // the provider priced as, when none was recorded
}

// pricer resolves and caches pricing per provider/model pair, and the
// endpoint host per provider.
type pricer struct {
	reg   *provider.Registry
	cache map[[2]string]pricing
	hosts map[string]string
}

func newPricer(reg *provider.Registry) *pricer {
	if reg == nil {
		reg = provider.NewRegistry()
	}
	return &pricer{reg: reg, cache: map[[2]string]pricing{}, hosts: map[string]string{}}
}

// modelKey identifies one row of a by-model view: the recorded provider (""
// for work that predates recorded providers), the host it called, the model.
type modelKey struct{ provider, host, model string }

// host is where a bucket's calls went: the host recorded with them, or — for
// rows from before hosts were recorded — where the provider (the inferred
// one, for rows with no provider) points today.
func (p *pricer) host(providerID, recorded, modelID string) string {
	if recorded != "" {
		return recorded
	}
	if providerID == "" {
		providerID = p.lookup("", modelID).inferred
	}
	if providerID == "" {
		return ""
	}
	h, ok := p.hosts[providerID]
	if !ok {
		h = p.reg.EndpointHost(providerID)
		p.hosts[providerID] = h
	}
	return h
}

func (p *pricer) lookup(providerID, modelID string) pricing {
	key := [2]string{providerID, modelID}
	if pr, ok := p.cache[key]; ok {
		return pr
	}
	var pr pricing
	if providerID == "" {
		// No provider was recorded: the work predates ogcode storing one. Price
		// it as the provider that serves the model now — the same routing the
		// loop used for it then — and when nothing does, say so rather than
		// assume a per-token bill: a guess here would invent spend.
		if sp := p.reg.ServingProvider(modelID); sp != nil && sp.ID() != "" {
			pr = p.lookup(sp.ID(), modelID)
			pr.inferred = sp.ID()
		} else {
			pr.billing = BillingUnknown
			if cm, ok := provider.LookupCatalogModel(modelID); ok {
				pr.name = cm.Name
			}
		}
		p.cache[key] = pr
		return pr
	}
	if cm, ok := provider.LookupCatalogModel(modelID); ok {
		pr.name = cm.Name
	}
	switch price, ok := p.reg.PriceOn(providerID, modelID); {
	case ok:
		pr.billing, pr.price = BillingMetered, price
	case !provider.BillsPerToken(providerID):
		pr.billing = BillingIncluded
		pr.list, pr.hasList = provider.ListPrice(modelID)
	default:
		pr.billing = BillingUnpriced
	}
	p.cache[key] = pr
	return pr
}

// price fills in m's cost fields from its tokens.
func (p *pricer) price(m *ModelUsage) {
	pr := p.lookup(m.Provider, m.Model)
	m.Billing, m.Name, m.InferredProvider = pr.billing, pr.name, pr.inferred
	m.CostUSD, m.ListUSD = nil, nil
	switch pr.billing {
	case BillingMetered:
		c := pr.price.Cost(m.Input, m.Output, m.CacheRead, m.CacheWrite)
		m.CostUSD = &c
	case BillingIncluded:
		zero := 0.0
		m.CostUSD = &zero
		if pr.hasList {
			l := pr.list.Cost(m.Input, m.Output, m.CacheRead, m.CacheWrite)
			m.ListUSD = &l
		}
	}
}

// costs returns the metered cost and the included list value of tokens spent on
// one provider/model pair.
func (p *pricer) costs(providerID, modelID string, t Tokens) (cost, list float64) {
	m := ModelUsage{Provider: providerID, Model: modelID, Tokens: t}
	p.price(&m)
	if m.Billing == BillingMetered && m.CostUSD != nil {
		cost = *m.CostUSD
	}
	if m.ListUSD != nil {
		list = *m.ListUSD
	}
	return cost, list
}

// sortModels puts the costliest first — billed cost, then list value — and
// breaks ties by tokens, so the model doing the most work leads either way.
func sortModels(ms []ModelUsage) {
	val := func(m ModelUsage) float64 {
		v := 0.0
		if m.CostUSD != nil {
			v += *m.CostUSD
		}
		if m.ListUSD != nil {
			v += *m.ListUSD
		}
		return v
	}
	sort.SliceStable(ms, func(i, j int) bool {
		if a, b := val(ms[i]), val(ms[j]); a != b {
			return a > b
		}
		if ms[i].Effective != ms[j].Effective {
			return ms[i].Effective > ms[j].Effective
		}
		return ms[i].Model < ms[j].Model
	})
}

// Summarize totals the ledger's spend between from and to (unix ms; to <= 0
// means now), by model, by project and by day, pricing each model with reg.
// project narrows it to one workspace — its own rows and those of the task
// worktrees inside it; empty means every project.
func Summarize(global *db.DB, reg *provider.Registry, from, to int64, project string) (*Summary, error) {
	// No end means no upper bound, not "now": a row spent in the same
	// millisecond as the query must still count.
	upper := to
	if to <= 0 {
		upper, to = math.MaxInt64, time.Now().UnixMilli()
	}
	sum := &Summary{From: from, To: to, Project: project, Models: []ModelUsage{}, Projects: []ProjectUsage{}, Days: []Day{}}
	if global == nil {
		return sum, nil
	}
	inProject, projectArgs := projectScope(project)
	var first sql.NullInt64 // MIN over an empty ledger is NULL
	if err := global.QueryRow(`SELECT MIN(time_created) FROM usage_event WHERE 1=1`+inProject,
		projectArgs...).Scan(&first); err != nil {
		return nil, fmt.Errorf("ledger start: %w", err)
	}
	sum.First = first.Int64

	period := `time_created >= ? AND time_created < ?` + inProject
	periodArgs := append([]any{from, upper}, projectArgs...)

	// One row per model, project and day: enough to build all three views, and
	// small however long the ledger grows.
	rows, err := global.Query(`
		SELECT provider, host, model, project,
		       strftime('%Y-%m-%d', time_created / 1000, 'unixepoch', 'localtime') AS day,
		       COUNT(*), SUM(input), SUM(output), SUM(reasoning), SUM(cache_read), SUM(cache_write)
		FROM usage_event
		WHERE `+period+`
		GROUP BY provider, host, model, project, day`, periodArgs...)
	if err != nil {
		return nil, fmt.Errorf("summarize ledger: %w", err)
	}
	defer rows.Close()

	pr := newPricer(reg)
	models := map[modelKey]*ModelUsage{}
	projects := map[string]*ProjectUsage{}
	days := map[string]*Day{}
	for rows.Next() {
		var providerID, host, modelID, project, day string
		var calls int
		var tc session.TokenCounts
		if err := rows.Scan(&providerID, &host, &modelID, &project, &day, &calls,
			&tc.Input, &tc.Output, &tc.Reasoning, &tc.CacheRead, &tc.CacheWrite); err != nil {
			return nil, err
		}
		var t Tokens
		t.add(tc)

		key := modelKey{providerID, pr.host(providerID, host, modelID), modelID}
		m := models[key]
		if m == nil {
			m = &ModelUsage{Provider: providerID, Host: key.host, Model: modelID}
			models[key] = m
		}
		m.Calls += calls
		m.Tokens.addTokens(t)

		cost, list := pr.costs(providerID, modelID, t)
		root := projectRoot(project)
		p := projects[root]
		if p == nil {
			p = &ProjectUsage{Project: root, Name: filepath.Base(root)}
			projects[root] = p
		}
		p.Tokens.addTokens(t)
		p.CostUSD += cost
		p.IncludedListUSD += list

		d := days[day]
		if d == nil {
			d = &Day{Day: day}
			days[day] = d
		}
		d.Input += t.Input
		d.Output += t.Output
		d.Effective += t.Effective
		d.CostUSD += cost
		d.IncludedListUSD += list
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sessions per model and in all, counted apart: a session that ran on two
	// days, models or hosts must count once in each figure. Counted here rather
	// than in SQL because a row's host can come from the fallback, which only
	// the pricer knows.
	srows, err := global.Query(`
		SELECT DISTINCT provider, host, model, session_id FROM usage_event
		WHERE `+period+` AND session_id != ''`, periodArgs...)
	if err != nil {
		return nil, fmt.Errorf("count sessions: %w", err)
	}
	defer srows.Close()
	sessionsOf := map[modelKey]map[string]struct{}{}
	for srows.Next() {
		var providerID, host, modelID, sessionID string
		if err := srows.Scan(&providerID, &host, &modelID, &sessionID); err != nil {
			return nil, err
		}
		key := modelKey{providerID, pr.host(providerID, host, modelID), modelID}
		if sessionsOf[key] == nil {
			sessionsOf[key] = map[string]struct{}{}
		}
		sessionsOf[key][sessionID] = struct{}{}
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}
	for key, ids := range sessionsOf {
		if m := models[key]; m != nil {
			m.Sessions = len(ids)
		}
	}
	if err := global.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM usage_event
		WHERE `+period+` AND session_id != ''`, periodArgs...).Scan(&sum.Totals.Sessions); err != nil {
		return nil, fmt.Errorf("count sessions: %w", err)
	}

	for _, m := range models {
		pr.price(m)
		sum.Totals.addModel(*m)
		sum.Models = append(sum.Models, *m)
	}
	sortModels(sum.Models)
	for _, p := range projects {
		sum.Projects = append(sum.Projects, *p)
	}
	sort.SliceStable(sum.Projects, func(i, j int) bool {
		a, b := sum.Projects[i], sum.Projects[j]
		if av, bv := a.CostUSD+a.IncludedListUSD, b.CostUSD+b.IncludedListUSD; av != bv {
			return av > bv
		}
		return a.Effective > b.Effective
	})
	for _, d := range days {
		sum.Days = append(sum.Days, *d)
	}
	sort.Slice(sum.Days, func(i, j int) bool { return sum.Days[i].Day < sum.Days[j].Day })
	return sum, nil
}

// worktreesDir is where a project's task worktrees live: <project>/.ogcode/worktrees/<branch>.
var worktreesDir = string(filepath.Separator) + filepath.Join(".ogcode", "worktrees") + string(filepath.Separator)

// projectRoot folds a task worktree into the project it belongs to, so a
// project's totals include the tasks it ran.
func projectRoot(dir string) string {
	if i := strings.Index(dir, worktreesDir); i >= 0 {
		return dir[:i]
	}
	return dir
}

// projectScope is the SQL condition that narrows the ledger to one project:
// its own rows and those of anything inside it (its task worktrees). Empty
// project means no condition.
func projectScope(project string) (string, []any) {
	if project == "" {
		return "", nil
	}
	project = strings.TrimSuffix(project, string(filepath.Separator))
	escape := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return ` AND (project = ? OR project LIKE ? ESCAPE '\')`,
		[]any{project, escape.Replace(project) + string(filepath.Separator) + "%"}
}

// ForSession prices one session from its own messages: each step at the model
// its message names — or the session's model, for steps from before messages
// recorded one — and its utility calls at the session's model. Sub-agent work
// is inside the utility total, folded in when the sub-agent finished.
//
// It reads the session's messages rather than the ledger so it prices exactly
// the tokens the session's token view counts.
func ForSession(store *session.Store, reg *provider.Registry, id session.SessionID) (*SessionCost, error) {
	sess, err := store.Get(id)
	if err != nil {
		return nil, err
	}
	steps, err := store.ListStepUsage(id)
	if err != nil {
		return nil, err
	}
	out := &SessionCost{Models: []ModelUsage{}}
	pr := newPricer(reg)
	models := map[modelKey]*ModelUsage{}
	// Messages do not record a host, so each row takes where its provider
	// points today.
	bucket := func(providerID, modelID string) *ModelUsage {
		if modelID == "" && sess != nil {
			providerID, modelID = sess.Provider, sess.Model
		}
		key := modelKey{providerID, pr.host(providerID, "", modelID), modelID}
		m := models[key]
		if m == nil {
			m = &ModelUsage{Provider: providerID, Host: key.host, Model: modelID, Sessions: 1}
			models[key] = m
		}
		return m
	}
	for _, s := range steps {
		m := bucket(s.Provider, s.Model)
		m.Calls++
		m.Tokens.add(s.Tokens)
	}
	if sess != nil && sess.UtilityTokens != nil && sess.UtilityTokens.Consumed() > 0 {
		m := bucket("", "")
		m.Calls++
		m.Tokens.add(*sess.UtilityTokens)
	}

	for _, m := range models {
		pr.price(m)
		out.Totals.addModel(*m)
		out.Models = append(out.Models, *m)
	}
	sortModels(out.Models)
	if len(out.Models) > 0 {
		out.Totals.Sessions = 1
	}
	return out, nil
}
