package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/git"
	"github.com/prasenjeet-symon/ogcode/internal/mcp"
	"github.com/prasenjeet-symon/ogcode/internal/memory"
	"github.com/prasenjeet-symon/ogcode/internal/plan"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/task"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
	"github.com/prasenjeet-symon/ogcode/internal/version"
)

// ServerMode determines the operational mode of the server.
type ServerMode string

const (
	ModeBuild ServerMode = "build"
	ModePlan  ServerMode = "plan"
)

type Server struct {
	port       int
	dir        string
	mode       ServerMode
	db         *db.DB
	globalDB   *db.DB // shared config DB at ~/.ogcode/config.db
	bus        *bus.Bus
	store      *session.Store
	planStore  *plan.Store
	taskStore  *task.Store
	registry   *provider.Registry
	defaultProvider provider.Provider
	loopRunner *agent.LoopRunner
	mcpClient  *mcp.Client
	mcpCfg     mcp.ServerConfig
	mem        *memory.Memory

	// Version check manager
	versionManager *version.Manager

	// Track running agent loops so they can be cancelled on abort
	mu           sync.Mutex
	running      map[session.SessionID]context.CancelFunc
	runningToken map[session.SessionID]uint64 // prevents goroutine from deleting a newer cancel
	nextToken    uint64

	// gitMu serializes all repo-level git operations (worktree add/remove/prune,
	// branch creation) to prevent concurrent writes from corrupting .git metadata.
	gitMu sync.Mutex
}

func New(port int, dir string, mode ServerMode) *Server {
	return &Server{port: port, dir: dir, mode: mode, running: make(map[session.SessionID]context.CancelFunc), runningToken: make(map[session.SessionID]uint64)}
}

func (s *Server) Start() error {
	dbPath := filepath.Join(s.dir, ".ogcode", "ogcode.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	s.db = database

	// Global config DB shared across all workspaces.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	globalDBPath := filepath.Join(home, ".ogcode", "config.db")
	if err := os.MkdirAll(filepath.Dir(globalDBPath), 0o755); err != nil {
		return fmt.Errorf("create global config dir: %w", err)
	}
	globalDatabase, err := db.Open(globalDBPath)
	if err != nil {
		return fmt.Errorf("open global config database: %w", err)
	}
	s.globalDB = globalDatabase

	s.bus = bus.New(256)
	s.store = session.NewStore(database)
	s.planStore = plan.NewStore(database)
	s.taskStore = task.NewStore(database)

	// Recover tasks that were in_progress when the server last stopped.
	failedTasks, err := s.taskStore.FailStuckTasks()
	if err != nil {
		slog.Warn("recover stuck tasks", "err", err)
	} else if len(failedTasks) > 0 {
		slog.Info("marked stuck tasks as failed", "count", len(failedTasks))
		// Clean up orphaned worktrees from crashed tasks
		for _, t := range failedTasks {
			if t.BranchName != "" {
				s.gitMu.Lock()
				if err := git.RemoveTaskWorktree(s.dir, t.BranchName); err != nil {
					slog.Warn("cleanup orphaned worktree", "task", t.ID, "branch", t.BranchName, "err", err)
				}
				s.gitMu.Unlock()
			}
		}
	}

	// Load DB-stored provider credentials (env vars take precedence).
	dbProviderCfgs, err := session.GetAllProviderConfigs(globalDatabase)
	if err != nil {
		slog.Warn("failed to load provider configs from DB", "err", err)
	}
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

	// Initialize provider
	registry := provider.NewRegistry()
	if key := resolveKey(os.Getenv("ANTHROPIC_API_KEY"), "anthropic"); key != "" {
		p, _ := provider.NewProviderWithConfig("anthropic", key, "")
		registry.Register(p)
		slog.Info("registered anthropic provider")
	}
	if key := resolveKey(os.Getenv("OPENAI_API_KEY"), "openai"); key != "" {
		baseURL := resolveBaseURL(os.Getenv("OPENAI_BASE_URL"), "openai")
		p, _ := provider.NewProviderWithConfig("openai", key, baseURL)
		registry.Register(p)
		slog.Info("registered openai provider")
	}
	if key := resolveKey(os.Getenv("OPENROUTER_API_KEY"), "openrouter"); key != "" {
		p, _ := provider.NewProviderWithConfig("openrouter", key, "")
		registry.Register(p)
		slog.Info("registered openrouter provider")
	}
	ollamaEnvURL := os.Getenv("OLLAMA_BASE_URL")
	ollamaEnvKey := os.Getenv("OLLAMA_API_KEY")
	ollamaKey := resolveKey(ollamaEnvKey, "ollama")
	ollamaBaseURL := resolveBaseURL(ollamaEnvURL, "ollama")
	if ollamaKey != "" || ollamaBaseURL != "" || fileExists("/usr/local/bin/ollama") || fileExists("/opt/homebrew/bin/ollama") {
		if ollamaKey != "" && ollamaBaseURL == "" {
			slog.Warn("Ollama API key is set but no base URL configured; using http://localhost:11434/v1")
		}
		p, _ := provider.NewProviderWithConfig("ollama", ollamaKey, ollamaBaseURL)
		registry.Register(p)
		slog.Info("registered ollama provider")
	}

	// Initialize tools
	toolRegistry := tool.NewRegistry()
	toolRegistry.Register(tool.BashTool{})
	toolRegistry.Register(tool.ReadTool{})
	toolRegistry.Register(tool.WriteTool{})
	toolRegistry.Register(tool.EditTool{})
	toolRegistry.Register(tool.GlobTool{})
	toolRegistry.Register(tool.GrepTool{})
	toolRegistry.Register(tool.BreakdownTool{})
	// memory_recall will be registered below after mem is initialized

	// Determine default provider with stable priority
	var defaultProvider provider.Provider
	priority := []string{"anthropic", "openai", "openrouter", "ollama"}
	for _, id := range priority {
		if p := registry.Get(id); p != nil {
			defaultProvider = p
			break
		}
	}
	if defaultProvider == nil {
		slog.Warn("no LLM provider configured; set ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY, OLLAMA_API_KEY, or install Ollama")
		defaultProvider = provider.NewAnthropicProvider()
	}

	s.registry = registry
	s.defaultProvider = defaultProvider

	// Load custom model preferences from DB
	prefs, err := session.GetModelPreferences(s.db)
	if err != nil {
		slog.Warn("failed to load model preferences", "err", err)
	} else {
		for _, p := range prefs {
			if p.IsCustom {
				s.registry.RegisterCustomModel(p.ID, p.ProviderID)
				slog.Info("registered custom model", "id", p.ID, "provider", p.ProviderID)
			}
		}
	}

	var mem *memory.Memory
	if strings.EqualFold(os.Getenv("OGCODE_AGENTIC_MEMORY_MODE"), "true") {
		// Legacy env-var path (takes precedence over DB config).
		embedProviderID := os.Getenv("OGCODE_EMBED_PROVIDER")
		if embedProviderID == "" {
			return fmt.Errorf("OGCODE_AGENTIC_MEMORY_MODE is enabled but OGCODE_EMBED_PROVIDER is not set; set it to openai, openrouter, or ollama")
		}
		embedP := registry.Get(embedProviderID)
		if embedP == nil {
			return fmt.Errorf("unknown embed provider %q; ensure corresponding API key is set", embedProviderID)
		}
		if _, ok := embedP.(provider.Embedder); !ok {
			return fmt.Errorf("provider %q does not support embeddings; choose openai, openrouter, or ollama", embedProviderID)
		}
		if os.Getenv("OGCODE_EMBED_MODEL") == "" {
			return fmt.Errorf("OGCODE_AGENTIC_MEMORY_MODE is enabled but OGCODE_EMBED_MODEL is not set; set it to e.g. text-embedding-3-small (openai) or nomic-embed-text (ollama)")
		}

		memStore, err := memory.Open(memory.DefaultDBPath())
		if err != nil {
			return fmt.Errorf("memory db: %w", err)
		}

		mem = memory.New(memStore, &memory.GraphOpts{
			ChatProvider:  defaultProvider,
			EmbedProvider: embedP,
		})
		s.mem = mem
		toolRegistry.Register(tool.NewMemoryRecallTool(mem))
	} else {
		// DB-config path: user enabled memory from the settings UI.
		dbMemCfg, err := session.GetMemoryConfig(globalDatabase)
		if err != nil {
			slog.Warn("failed to read memory config from DB", "err", err)
		} else if dbMemCfg.Enabled && dbMemCfg.EmbedProviderID != "" {
			embedP, err := provider.NewEmbedProvider(dbMemCfg.EmbedProviderID, dbMemCfg.EmbedAPIKey, dbMemCfg.EmbedModel)
			if err != nil {
				slog.Warn("memory config has unsupported embed provider; memory disabled", "err", err)
			} else {
				// Resolve chat provider: use DB config if set, otherwise fall back to defaultProvider.
				chatP := defaultProvider
				chatModel := ""
				if dbMemCfg.ChatProviderID != "" {
					cp, err := provider.NewChatProvider(dbMemCfg.ChatProviderID, dbMemCfg.ChatAPIKey, dbMemCfg.ChatModel)
					if err != nil {
						slog.Warn("memory config has unsupported chat provider; using default", "err", err)
					} else {
						chatP = cp
						chatModel = dbMemCfg.ChatModel
					}
				}

				memStore, err := memory.Open(memory.DefaultDBPath())
				if err != nil {
					slog.Warn("failed to open memory store; memory disabled", "err", err)
				} else {
					mem = memory.New(memStore, &memory.GraphOpts{
						ChatProvider:  chatP,
						ChatModel:     chatModel,
						EmbedProvider: embedP,
					})
					s.mem = mem
					toolRegistry.Register(tool.NewMemoryRecallTool(mem))
					slog.Info("agentic memory enabled via DB config",
						"embed_provider", dbMemCfg.EmbedProviderID, "embed_model", dbMemCfg.EmbedModel,
						"chat_provider", chatP.ID(), "chat_model", chatModel)
				}
			}
		}
	}

	// Initialize MCP client (for tool exposure, unrelated to agentic memory
	// database, which is now local SQLite with its own embed provider)
	if strings.EqualFold(os.Getenv("OGCODE_MCP_ENABLED"), "true") {
		cfg := mcp.ConfigFromEnv()
		s.mcpCfg = cfg
		mcpClient, err := mcp.NewClient(context.Background(), cfg)
		if err != nil {
			slog.Warn("failed to connect to MCP server, MCP tools unavailable", "err", err)
		} else {
			s.mcpClient = mcpClient
		}
	}

	s.loopRunner = &agent.LoopRunner{
		Store:           s.store,
		Bus:             s.bus,
		Registry:        registry,
		DefaultProvider: defaultProvider,
		Tools:           toolRegistry,
		Dir:             s.dir,
		Memory:          mem,
		MCP:             s.mcpClient,
	}

	// Initialize version manager
	s.versionManager = version.New()

	r := s.routes()

	// Try ports starting from the configured port, up to 10 attempts.
	var listener net.Listener
	tryPort := s.port
	for i := 0; i < 10; i++ {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", tryPort))
		if err == nil {
			listener = l
			s.port = tryPort
			break
		}
		if strings.Contains(err.Error(), "address already in use") {
			slog.Info("port in use, trying next", "port", tryPort)
			tryPort++
			continue
		}
		return fmt.Errorf("bind port: %w", err)
	}
	if listener == nil {
		return fmt.Errorf("no available port found (tried %d–%d)", s.port, tryPort-1)
	}

	addr := fmt.Sprintf(":%d", s.port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  0,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	url := fmt.Sprintf("http://localhost:%d", s.port)
	slog.Info("starting ogcode server", "addr", addr, "dir", s.dir)
	go openBrowser(url)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			quit <- syscall.SIGTERM
		}
	}()

	<-quit
	slog.Info("shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Shutdown HTTP server (closes all connections and releases port)
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("server shutdown error", "err", err)
	}

	// Close listener explicitly
	if err := listener.Close(); err != nil {
		slog.Warn("close listener", "err", err)
	}

	// Close MCP client
	if s.mcpClient != nil {
		if err := s.mcpClient.Close(); err != nil {
			slog.Warn("close mcp client", "err", err)
		}
	}

	// Close memory store
	if s.mem != nil {
		if err := s.mem.Store.Close(); err != nil {
			slog.Warn("close memory store", "err", err)
		}
	}

	// Close database
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			slog.Warn("close database", "err", err)
		}
	}
	if s.globalDB != nil {
		if err := s.globalDB.Close(); err != nil {
			slog.Warn("close global config database", "err", err)
		}
	}

	slog.Info("server stopped, port released")
	return nil
}

func openBrowser(url string) {
	time.Sleep(500 * time.Millisecond)
	var cmd string
	var args []string
	switch {
	case fileExists("/usr/bin/open"):
		cmd, args = "open", []string{url}
	case fileExists("/usr/bin/xdg-open"):
		cmd, args = "xdg-open", []string{url}
	default:
		return
	}
	_ = exec.Command(cmd, args...).Start()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}