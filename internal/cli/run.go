package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/config"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/mcp"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/skill"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
	"github.com/spf13/cobra"
)

var (
	runAgentName    string
	runOutputFormat string
	runMaxTurns     int
	runModel        string
)

var runCmd = &cobra.Command{
	Use:   "run [prompt]",
	Short: "Run a one-shot agent prompt non-interactively (prints to stdout)",
	Example: `  ogcode run "add unit tests for auth.go"
  echo "explain this codebase" | ogcode run
  git diff | ogcode run --agent plan "review these changes"`,
	RunE: runPrompt,
}

func init() {
	runCmd.Flags().StringVarP(&runAgentName, "agent", "a", "build", "Agent type: build or plan")
	runCmd.Flags().StringVarP(&runOutputFormat, "output-format", "o", "text", "Output format: text or json")
	runCmd.Flags().IntVar(&runMaxTurns, "max-turns", 100, "Maximum agent loop iterations")
	runCmd.Flags().StringVar(&runModel, "model", "", "Model ID override (e.g. claude-sonnet-4-5)")
	rootCmd.AddCommand(runCmd)
}

func runPrompt(cmd *cobra.Command, args []string) error {
	// Redirect slog to stderr so stdout stays clean for agent output
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	// Collect prompt: positional args + piped stdin
	var parts []string
	if len(args) > 0 {
		parts = append(parts, strings.Join(args, " "))
	}
	if stat, _ := os.Stdin.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		if s := strings.TrimSpace(string(data)); s != "" {
			parts = append(parts, s)
		}
	}
	prompt := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if prompt == "" {
		return fmt.Errorf("prompt required — pass as argument or pipe via stdin")
	}

	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	if path := config.EnsureProjectFile(dir); path != "" {
		slog.Info("created project config file", "path", path)
	}

	// Open project DB
	dbPath := filepath.Join(dir, ".ogcode", "ogcode.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	// Open global config DB (provider keys stored here when set via UI)
	home, _ := os.UserHomeDir()
	globalDBPath := filepath.Join(home, ".ogcode", "config.db")
	if err := os.MkdirAll(filepath.Dir(globalDBPath), 0o755); err != nil {
		return fmt.Errorf("create global config dir: %w", err)
	}
	globalDatabase, err := db.Open(globalDBPath)
	if err != nil {
		return fmt.Errorf("open global config database: %w", err)
	}

	// Build provider registry — env vars take precedence over DB-stored keys
	dbProviderCfgs, _ := session.GetAllProviderConfigs(globalDatabase)
	dbProviderMap := make(map[string]*session.ProviderConfig)
	for _, c := range dbProviderCfgs {
		dbProviderMap[c.ProviderID] = c
	}
	resolveKey := func(envKey, providerID string) string {
		if envKey != "" {
			return envKey
		}
		if c, ok := dbProviderMap[providerID]; ok {
			return c.APIKey
		}
		return ""
	}
	resolveBaseURL := func(envURL, providerID string) string {
		if envURL != "" {
			return envURL
		}
		if c, ok := dbProviderMap[providerID]; ok {
			return c.BaseURL
		}
		return ""
	}

	registry := provider.NewRegistry()
	if key := resolveKey(os.Getenv("ANTHROPIC_API_KEY"), "anthropic"); key != "" {
		baseURL := resolveBaseURL(os.Getenv("ANTHROPIC_BASE_URL"), "anthropic")
		if p, e := provider.NewProviderWithConfig("anthropic", key, baseURL); e == nil {
			registry.Register(p)
		}
	}
	if key := resolveKey(os.Getenv("OPENAI_API_KEY"), "openai"); key != "" {
		baseURL := resolveBaseURL(os.Getenv("OPENAI_BASE_URL"), "openai")
		if p, e := provider.NewProviderWithConfig("openai", key, baseURL); e == nil {
			registry.Register(p)
		}
	}
	if key := resolveKey(os.Getenv("OPENROUTER_API_KEY"), "openrouter"); key != "" {
		if p, e := provider.NewProviderWithConfig("openrouter", key, ""); e == nil {
			registry.Register(p)
		}
	}
	ollamaKey := resolveKey(os.Getenv("OLLAMA_API_KEY"), "ollama")
	ollamaBaseURL := resolveBaseURL(os.Getenv("OLLAMA_BASE_URL"), "ollama")
	// Same fallback the server applies: a stale persisted endpoint yields to a
	// live one, and a machine with no local Ollama still finds a router.
	if os.Getenv("OLLAMA_BASE_URL") == "" {
		ollamaBaseURL = provider.PreferLiveOllamaEndpoint(ollamaBaseURL, provider.DetectOllama())
	}
	if ollamaKey != "" || ollamaBaseURL != "" {
		if p, e := provider.NewProviderWithConfig("ollama", ollamaKey, ollamaBaseURL); e == nil {
			registry.Register(p)
		}
	}

	var defaultProvider provider.Provider
	for _, pid := range []string{"anthropic", "openai", "openrouter", "ollama"} {
		if p := registry.Get(pid); p != nil {
			defaultProvider = p
			break
		}
	}
	if defaultProvider == nil {
		return fmt.Errorf("no provider configured — set ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY, or OLLAMA_BASE_URL")
	}

	// Tool registry (same as server, minus BreakdownTool which is a no-op for standalone runs)
	toolRegistry := tool.NewRegistry()
	toolRegistry.Register(tool.BashTool{})
	toolRegistry.Register(tool.ReadTool{})
	toolRegistry.Register(tool.NewCompactContextTool())
	toolRegistry.Register(tool.WriteTool{})
	toolRegistry.Register(tool.EditTool{})
	toolRegistry.Register(tool.GlobTool{})
	toolRegistry.Register(tool.GrepTool{})
	toolRegistry.Register(tool.ViewImageTool{})

	// Skills, from the same ogcode.json this command already loads for
	// provider settings.
	fullCfg := config.Load(dir)
	skillCfg := fullCfg.Skills
	skillLoader := skill.NewLoader(skill.Config{
		Paths:       skillCfg.Paths,
		URLs:        skillCfg.URLs,
		Permissions: skillCfg.Permissions,
	})
	toolRegistry.Register(tool.NewSkillTool(skillLoader))

	// MCP servers: build the Manager now, then connect synchronously. The CLI
	// is a one-shot headless run (no UI/bus to surface an OAuth prompt), so the
	// eager connect preserves the prior behaviour — the agent's first step has
	// the tools in hand. Close is deferred so subprocesses are torn down when
	// runPrompt returns.
	mcpMgr, mcpErr := mcp.New(context.Background(), fullCfg)
	if mcpErr != nil {
		slog.Warn("mcp: manager construction failed", "err", mcpErr)
	}
	mcpTools, connErr := mcpMgr.Connect(context.Background())
	if connErr != nil {
		slog.Warn("mcp: one or more servers failed to connect", "err", connErr)
	}
	for _, t := range mcpTools {
		toolRegistry.Register(t)
	}
	defer mcpMgr.Close()

	b := bus.New(1024)
	store := session.NewStore(database)

	// Create session
	title := prompt
	if len(title) > 60 {
		title = title[:60] + "…"
	}
	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   dir,
		Directory:   dir,
		Title:       title,
		Model:       runModel,
		SessionType: runAgentName,
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := store.Create(sess); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// Create user message + text part
	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: sess.ID,
		Role:      session.RoleUser,
		Agent:     runAgentName,
		CreatedAt: session.Now(),
	}
	if err := store.CreateMessage(userMsg); err != nil {
		return fmt.Errorf("create message: %w", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: prompt})
	if err := store.CreatePart(&session.Part{
		ID:        session.NewPartID(),
		MessageID: userMsg.ID,
		SessionID: sess.ID,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}); err != nil {
		return fmt.Errorf("create message part: %w", err)
	}

	// Subscribe before starting loop so no events are missed
	events := b.SubscribeAll()
	defer b.Unsubscribe(events)

	lr := &agent.LoopRunner{
		Store:           store,
		Bus:             b,
		Registry:        registry,
		DefaultProvider: defaultProvider,
		Tools:           toolRegistry,
		Dir:             dir,
		MaxSteps:        runMaxTurns,
		Skills:          skillLoader,
	}

	// Register the task sub-agent tool now that the runner exists (the build
	// agent advertises it, so it must resolve to avoid an "unknown tool" result).
	toolRegistry.Register(tool.TaskTool{Run: lr.RunTaskSession})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopDone := make(chan error, 1)
	go func() {
		loopDone <- lr.RunLoop(ctx, sess.ID, runAgentName, 0, 0)
	}()

	// Collect and stream text output from bus events
	partPrinted := make(map[session.PartID]int) // tracks chars already written per part
	var fullText strings.Builder

	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return printResult(&fullText, store, sess.ID, runModel, runOutputFormat)
			}
			switch evt.Type {
			case "message.part.updated":
				var props struct {
					SessionID string `json:"sessionId"`
					PartID    string `json:"partId"`
				}
				if json.Unmarshal(evt.Properties, &props) != nil || props.SessionID != string(sess.ID) {
					continue
				}
				part, err := store.GetPart(session.PartID(props.PartID))
				if err != nil || part == nil || part.Type != session.PartText {
					continue
				}
				var td session.TextPartData
				if json.Unmarshal(part.Data, &td) != nil {
					continue
				}
				prev := partPrinted[part.ID]
				if newChars := td.Text[prev:]; newChars != "" {
					if runOutputFormat == "text" {
						fmt.Print(newChars)
					}
					fullText.WriteString(newChars)
					partPrinted[part.ID] = len(td.Text)
				}
			case "loop.done":
				var props struct {
					SessionID string `json:"sessionId"`
				}
				if json.Unmarshal(evt.Properties, &props) != nil {
					continue
				}
				if props.SessionID == string(sess.ID) {
					return printResult(&fullText, store, sess.ID, runModel, runOutputFormat)
				}
			}
		case err := <-loopDone:
			if err != nil {
				return err
			}
			return printResult(&fullText, store, sess.ID, runModel, runOutputFormat)
		}
	}
}

// runTokens is the token breakdown a run reports, summed over every assistant
// turn. The JSON names are fixed independently of session.TokenCounts so an
// external harness parsing this output does not break if the internal struct
// is renamed.
type runTokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Reasoning  int `json:"reasoning"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
	Total      int `json:"total"`
}

// runResult is the JSON document `--output-format json` prints.
type runResult struct {
	Result    string    `json:"result"`
	SessionID string    `json:"session_id"`
	Model     string    `json:"model,omitempty"`
	NumTurns  int       `json:"num_turns"`
	Finish    string    `json:"finish,omitempty"`
	Tokens    runTokens `json:"tokens"`
	// CostUSD is null rather than 0 when it cannot be established, so a
	// consumer can tell "not priced" from "free".
	CostUSD *float64 `json:"cost_usd"`
}

// Cache-token prices as a multiple of the model's base input price. The
// catalog stores one input price per model, and both providers it covers bill
// cache traffic off that number: Anthropic charges 1.25x to write an entry and
// 0.1x to read one, and the OpenAI models listed discount cached input to 0.1x
// while never billing a write separately (their cache write count stays 0).
const (
	cacheWriteMultiplier = 1.25
	cacheReadMultiplier  = 0.10
)

// allTurnsLimit is a ceiling, not a page size — every message of the session is
// wanted. No agent loop approaches it: MaxSteps caps a run far below.
const allTurnsLimit = 1_000_000

// collectUsage sums the per-turn counts the loop recorded on assistant
// messages and returns the last finish reason, which distinguishes a run that
// stopped because the model was done from one that hit --max-turns.
func collectUsage(store *session.Store, sessionID session.SessionID) (tokens runTokens, turns int, finish string) {
	msgs, err := store.GetMessages(sessionID, "", allTurnsLimit)
	if err != nil {
		slog.Warn("usage: could not read messages", "err", err)
		return tokens, 0, ""
	}
	for _, m := range msgs {
		if m.Info.Role != session.RoleAssistant {
			continue
		}
		turns++
		if m.Info.Finish != nil {
			finish = *m.Info.Finish
		}
		t := m.Info.Tokens
		if t == nil {
			continue
		}
		tokens.Input += t.Input
		tokens.Output += t.Output
		tokens.Reasoning += t.Reasoning
		tokens.CacheRead += t.CacheRead
		tokens.CacheWrite += t.CacheWrite
		tokens.Total += t.Total
	}
	return tokens, turns, finish
}

// estimateCost prices a run against the static catalog, or returns nil when it
// cannot: with no --model the provider applies its own default and the CLI
// never learns which model answered, and a dynamic provider's models are not in
// the catalog at all. Reasoning tokens are not added separately — providers
// bill them inside the output count, which is why TokenCounts.Total excludes
// them too.
func estimateCost(t runTokens, modelID string) *float64 {
	if modelID == "" {
		return nil
	}
	m, ok := provider.CatalogModelByID(modelID)
	if !ok || (m.InputPricePerM == 0 && m.OutputPricePerM == 0) {
		return nil
	}
	const perMillion = 1_000_000.0
	cost := (float64(t.Input)*m.InputPricePerM +
		float64(t.CacheWrite)*m.InputPricePerM*cacheWriteMultiplier +
		float64(t.CacheRead)*m.InputPricePerM*cacheReadMultiplier +
		float64(t.Output)*m.OutputPricePerM) / perMillion
	return &cost
}

func printResult(text *strings.Builder, store *session.Store, sessionID session.SessionID, modelID, format string) error {
	tokens, turns, finish := collectUsage(store, sessionID)
	cost := estimateCost(tokens, modelID)

	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(runResult{
			Result:    text.String(),
			SessionID: string(sessionID),
			Model:     modelID,
			NumTurns:  turns,
			Finish:    finish,
			Tokens:    tokens,
			CostUSD:   cost,
		})
	default: // text
		fmt.Println() // trailing newline after streamed output
		// Summary goes to stderr: stdout is the agent's answer, and a caller
		// piping it should not have to strip this off.
		costStr := "n/a"
		if cost != nil {
			costStr = fmt.Sprintf("$%.4f", *cost)
		}
		fmt.Fprintf(os.Stderr, "turns=%d finish=%s in=%d out=%d cache_read=%d cache_write=%d total=%d cost=%s\n",
			turns, finish, tokens.Input, tokens.Output, tokens.CacheRead, tokens.CacheWrite, tokens.Total, costStr)
		return nil
	}
}
