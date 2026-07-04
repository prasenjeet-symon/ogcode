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
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
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
	toolRegistry.Register(tool.WriteTool{})
	toolRegistry.Register(tool.EditTool{})
	toolRegistry.Register(tool.GlobTool{})
	toolRegistry.Register(tool.GrepTool{})

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
	}

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
				return printResult(&fullText, sess.ID, runOutputFormat)
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
					return printResult(&fullText, sess.ID, runOutputFormat)
				}
			}
		case err := <-loopDone:
			if err != nil {
				return err
			}
			return printResult(&fullText, sess.ID, runOutputFormat)
		}
	}
}

func printResult(text *strings.Builder, sessionID session.SessionID, format string) error {
	switch format {
	case "json":
		out := map[string]any{
			"result":     text.String(),
			"session_id": string(sessionID),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	default: // text
		fmt.Println() // trailing newline after streamed output
		return nil
	}
}
