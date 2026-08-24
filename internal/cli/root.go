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

	var defaultProvider provider.Provider
	priority := []string{"anthropic", "openai", "openrouter", "ollama"}
	for _, pid := range priority {
		if p := registry.Get(pid); p != nil {
			defaultProvider = p
			break
		}
	}
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

	idx := indexer.New(dir, docindexStore, lr)
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

	switch format {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	default:
		handler = slog.NewTextHandler(os.Stdout, opts)
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
	srv := server.New(port, dir, mode)
	return srv.Start()
}

func Execute() error {
	_ = godotenv.Load()
	setupLogging()
	if dir, err := os.Getwd(); err == nil {
		config.Load(dir).ApplyEnv()
	}
	return rootCmd.Execute()
}
