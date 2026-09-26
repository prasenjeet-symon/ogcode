package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/config"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	"github.com/prasenjeet-symon/ogcode/internal/indexer"
	"github.com/prasenjeet-symon/ogcode/internal/modelcatalog"
	"github.com/prasenjeet-symon/ogcode/internal/portmap"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/server"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
	"github.com/spf13/cobra"
)

var port int
var indexModel string
var ollamaURLFlag string
var ollamaKeyFlag string

var rootCmd = &cobra.Command{
	Use:   "ogcode",
	Short: "Agentic coding assistant with web UI",
	// Runs before every command (including bare `ogcode`): explicit flags
	// beat everything else, so apply them as env var overrides last, after
	// the config file has already filled any gaps in Execute().
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if ollamaURLFlag != "" {
			os.Setenv("OLLAMA_BASE_URL", ollamaURLFlag)
		}
		if ollamaKeyFlag != "" {
			os.Setenv("OLLAMA_API_KEY", ollamaKeyFlag)
		}
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return serve(cmd, args)
	},
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the ogcode server",
	RunE:  serve,
}

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Start ogcode in Plan Mode",
	RunE: func(cmd *cobra.Command, args []string) error {
		return serveWithMode(cmd, args, server.ModePlan)
	},
}

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Scan workspace for PDF files and index them with semantic labels",
	RunE:  runIndex,
}

func init() {
	rootCmd.Flags().IntVarP(&port, "port", "p", 9595, "Port to listen on")
	serveCmd.Flags().IntVarP(&port, "port", "p", 9595, "Port to listen on")
	planCmd.Flags().IntVarP(&port, "port", "p", 9595, "Port to listen on")
	indexCmd.Flags().StringVar(&indexModel, "model", "", "Model to use for the IndexAgent (default: provider default)")
	rootCmd.PersistentFlags().StringVar(&ollamaURLFlag, "ollama-url", "", "Ollama server address, e.g. http://100.x.x.x:11434 (overrides OLLAMA_BASE_URL and the config file)")
	rootCmd.PersistentFlags().StringVar(&ollamaKeyFlag, "ollama-key", "", "API key for a hosted/authenticated Ollama-compatible endpoint (overrides OLLAMA_API_KEY)")
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(indexCmd)
}

func runIndex(cmd *cobra.Command, args []string) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	if path := config.EnsureProjectFile(dir); path != "" {
		slog.Info("created project config file", "path", path)
	}

	dbPath := filepath.Join(dir, ".ogcode", "ogcode.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Open the global config DB too. The OGX link (and the provider credentials
	// the settings UI writes) live there, not in the project DB.
	home, _ := os.UserHomeDir()
	globalDBPath := filepath.Join(home, ".ogcode", "config.db")
	if err := os.MkdirAll(filepath.Dir(globalDBPath), 0o755); err != nil {
		return fmt.Errorf("create global config dir: %w", err)
	}
	globalDatabase, err := db.Open(globalDBPath)
	if err != nil {
		return fmt.Errorf("open global config database: %w", err)
	}
	defer globalDatabase.Close()

	b := bus.New(256)
	sessionStore := session.NewStore(database)
	docindexStore := docindex.NewStore(database)

	// Register providers using the same priority logic as the server.
	registry := provider.NewRegistry()
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		baseURL := os.Getenv("ANTHROPIC_BASE_URL")
		p, _ := provider.NewProviderWithConfig("anthropic", key, baseURL)
		registry.Register(p)
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		baseURL := os.Getenv("OPENAI_BASE_URL")
		p, _ := provider.NewProviderWithConfig("openai", key, baseURL)
		registry.Register(p)
	}
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		p, _ := provider.NewProviderWithConfig("openrouter", key, "")
		registry.Register(p)
	}
	ollamaKey := os.Getenv("OLLAMA_API_KEY")
	ollamaBaseURL := os.Getenv("OLLAMA_BASE_URL")
	ollamaStatus := provider.DetectOllama()
	if ollamaKey != "" || ollamaBaseURL != "" || ollamaStatus.Installed || ollamaStatus.Running {
		if ollamaBaseURL == "" {
			ollamaBaseURL = ollamaStatus.BaseURL
		}
		p, _ := provider.NewProviderWithConfig("ollama", ollamaKey, ollamaBaseURL)
		registry.Register(p)
	}
	// A connected OG Lab subscription, stored globally and registered only when
	// the link carries a plan (see session.OGXAccount.HasPlan).
	if acct, err := session.GetOGXAccount(globalDatabase); err == nil && acct.HasPlan() {
		if p, err := provider.NewOGXProvider(acct.Token); err == nil {
			registry.Register(p)
		}
	}

	// Seed each provider's catalogue from the persisted copy so Models() has an
	// answer without a live fetch on this one-shot path. There is no background
	// refresh here: `ogcode index` resolves its model and exits.
	if err := modelcatalog.Seed(registry, globalDatabase); err != nil {
		slog.Warn("seed model catalog failed", "err", err)
	}

	defaultProvider := registry.DefaultUsable()
	if defaultProvider == nil {
		return fmt.Errorf("no LLM provider configured; set ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY, or OLLAMA_API_KEY")
	}

	toolRegistry := tool.NewRegistry()
	toolRegistry.Register(tool.ReadTool{})
	toolRegistry.Register(tool.NewCompactContextTool())
	toolRegistry.Register(tool.GlobTool{})
	toolRegistry.Register(tool.GrepTool{})
	toolRegistry.Register(tool.NewSubmitDocIndexTool(docindexStore))

	lr := &agent.LoopRunner{
		Store:           sessionStore,
		Bus:             b,
		Registry:        registry,
		DefaultProvider: defaultProvider,
		Tools:           toolRegistry,
		Dir:             dir,
		MaxSteps:        50,
	}

	// Seed and apply the shipped default excludes, exactly as the server's
	// index paths do. Without this the same project would index different files
	// from the terminal than from the app, and the defaults would be a claim
	// about the UI rather than about the index.
	if err := docindexStore.SeedDefaultExcludes(dir); err != nil {
		slog.Warn("seed default excludes failed", "dir", dir, "err", err)
	}
	var excludePatterns []string
	if excludes, err := docindexStore.ListExcludes(dir); err != nil {
		slog.Warn("fetch excludes failed, indexing without them", "dir", dir, "err", err)
	} else {
		for _, e := range excludes {
			excludePatterns = append(excludePatterns, e.Pattern)
		}
	}

	idx := indexer.New(dir, docindexStore, lr).WithExcludes(excludePatterns)
	if indexModel != "" {
		idx = idx.WithModel(indexModel)
	}
	ctx := context.Background()
	if err := idx.Run(ctx); err != nil {
		return fmt.Errorf("indexing failed: %w", err)
	}

	fmt.Println("Indexing complete")
	return nil
}

func setupLogging() {
	level := slog.LevelInfo
	levelStr := strings.ToLower(strings.TrimSpace(os.Getenv("OGCODE_LOG_LEVEL")))
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	format := strings.ToLower(strings.TrimSpace(os.Getenv("OGCODE_LOG_FORMAT")))
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level, AddSource: level <= slog.LevelDebug}

	// Logs go to stderr, never stdout. PersistentPreRun calls this before any
	// command runs, so a log line on stdout would land ahead of the command's
	// real output — which silently corrupted `run --output-format json`, whose
	// stdout is a single JSON document a caller parses.
	switch format {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	slog.SetDefault(slog.New(handler))

	slog.Info("logging initialized", "level", level, "format", format)
}

func serve(cmd *cobra.Command, args []string) error {
	return serveWithMode(cmd, args, server.ModeBuild)
}

func serveWithMode(cmd *cobra.Command, args []string, mode server.ServerMode) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	if path := config.EnsureProjectFile(dir); path != "" {
		slog.Info("created project config file", "path", path)
	}

	// Per-project port stability. The server walks past a busy port and reports
	// the one it actually bound, but without memory a project's port depends on
	// start order. So: an explicit --port wins and becomes this project's port;
	// otherwise reuse the port the project used last; and a brand-new project is
	// placed on a port no other project has claimed. Only the first-time case
	// records the bound port (via OnListen) — once a project has a port, a later
	// clash makes the server walk for this run without overwriting the remembered
	// port, so "already running elsewhere" never reassigns the project's home.
	startPort := port
	var onListen func(int)
	switch {
	case cmd.Flags().Changed("port"):
		if err := portmap.Save(dir, port); err != nil {
			slog.Warn("could not record project port", "dir", dir, "err", err)
		}
	default:
		if remembered, ok := portmap.Lookup(dir); ok {
			startPort = remembered
		} else {
			startPort = portmap.SuggestStart(dir, port)
			onListen = func(bound int) {
				if err := portmap.Save(dir, bound); err != nil {
					slog.Warn("could not record project port", "dir", dir, "err", err)
				}
			}
		}
	}
	if startPort != port {
		slog.Info("using this project's port", "dir", dir, "port", startPort)
	}

	srv := server.NewWithOptions(startPort, dir, mode, server.Options{OnListen: onListen})
	return srv.Serve(context.Background())
}

func Execute() error {
	_ = godotenv.Load()
	setupLogging()
	if dir, err := os.Getwd(); err == nil {
		config.Load(dir).ApplyEnv()
	}
	return rootCmd.Execute()
}
