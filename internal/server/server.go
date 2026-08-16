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
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	"github.com/prasenjeet-symon/ogcode/internal/git"
	"github.com/prasenjeet-symon/ogcode/internal/indexer"
	"github.com/prasenjeet-symon/ogcode/internal/memory"
	"github.com/prasenjeet-symon/ogcode/internal/note"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/plan"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/search"
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
	port            int
	dir             string
	mode            ServerMode
	db              *db.DB
	globalDB        *db.DB // shared config DB at ~/.ogcode/config.db
	bus             *bus.Bus
	store           *session.Store
	planStore       *plan.Store
	taskStore       *task.Store
	noteStore       *note.Store
	docindexStore   *docindex.Store
	registry        *provider.Registry
	defaultProvider provider.Provider
	loopRunner      *agent.LoopRunner
	mem             *memory.Memory
	permissions     *permission.Manager

	// Version check manager
	versionManager *version.Manager

	// Search bridge (optional — enabled via OGCODE_SEARCH_ENABLED=true)
	searchBridge *search.BridgeProcess

	// PostHog analytics client (optional — enabled via the settings UI)
	posthogClient *PostHogClient

	// Track running agent loops so they can be cancelled on abort
	mu           sync.Mutex
	running      map[session.SessionID]context.CancelFunc
	runningToken map[session.SessionID]uint64 // prevents goroutine from deleting a newer cancel
	nextToken    uint64

	// loopControls holds the LoopControl for each running agent loop, keyed by
	// session ID. It lets the guidance endpoint push mid-loop instructions and
	// cancel in-flight tools without killing the loop. Entries are managed
	// alongside the running map (set when a loop starts, cleared when it exits).
	loopControls map[session.SessionID]*agent.LoopControl

	// gitMu serializes all repo-level git operations (worktree add/remove/prune,
	// branch creation) to prevent concurrent writes from corrupting .git metadata.
	gitMu sync.Mutex

	// docindexMu protects docindexRunning.
	docindexMu      sync.Mutex
	docindexRunning bool
	indexerProgress *indexer.ProgressTracker // nil when not indexing
}

func New(port int, dir string, mode ServerMode) *Server {
	return &Server{port: port, dir: dir, mode: mode, running: make(map[session.SessionID]context.CancelFunc), runningToken: make(map[session.SessionID]uint64), loopControls: make(map[session.SessionID]*agent.LoopControl)}
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
	s.noteStore = note.NewStore(database)
	s.docindexStore = docindex.NewStore(database)

	// Recover notes stuck in "generating" status from a previous server crash.
	if stuck, err := s.noteStore.RecoverStuckNotes(); err != nil {
		slog.Warn("recover stuck notes", "err", err)
	} else if len(stuck) > 0 {
		slog.Info("recovered stuck notes", "count", len(stuck))
	}

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

	// Initialize provider registry from DB-stored credentials + environment
	// variables (env takes precedence). Built here at startup and rebuilt in
	// place by reloadProviders() when credentials change at runtime.
	registry := provider.NewRegistry()
	for _, p := range s.loadProviderMap() {
		registry.Register(p)
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
	toolRegistry.Register(tool.NewSubmitDocIndexTool(s.docindexStore))
	toolRegistry.Register(tool.ReadPdfPageTool{})
	toolRegistry.Register(tool.NewPdfIndexTool(s.docindexStore))
	toolRegistry.Register(tool.ReadDocxPageTool{})
	toolRegistry.Register(tool.NewDocxIndexTool(s.docindexStore))
	toolRegistry.Register(tool.NewProjectIndexTool(s.docindexStore))
	toolRegistry.Register(tool.LatexToPdfTool{})
	toolRegistry.Register(tool.ViewImageTool{})

	// Search bridge — opt-in via OGCODE_SEARCH_ENABLED=true env var OR the settings UI.
	// Must be started before loopRunner is built so RunSearchSession can be wired in.
	searchEnabledEnv := strings.EqualFold(os.Getenv("OGCODE_SEARCH_ENABLED"), "true")
	searchEnabledDB := false
	if dbSearchCfg, err := session.GetSearchConfig(globalDatabase); err != nil {
		slog.Warn("failed to read search config from DB", "err", err)
	} else {
		searchEnabledDB = dbSearchCfg.Enabled
	}
	if searchEnabledEnv || searchEnabledDB {
		cfg := search.ConfigFromEnv()
		// DB config takes precedence for UseRealProfile over env var when DB is the source of truth.
		if searchEnabledDB {
			if dbSearchCfg, err := session.GetSearchConfig(globalDatabase); err == nil {
				cfg.UseRealProfile = cfg.UseRealProfile || dbSearchCfg.UseRealProfile
			}
		}
		bridgeDir := os.Getenv("OGCODE_SEARCH_BRIDGE_DIR") // empty → auto-detect from binary
		proc, err := search.StartBridge(context.Background(), cfg, bridgeDir)
		if err != nil {
			slog.Warn("search bridge unavailable; search tools disabled", "err", err)
		} else {
			s.searchBridge = proc
			client := proc.Client()
			toolRegistry.Register(tool.WebSearchTool{Bridge: client})
			toolRegistry.Register(tool.FetchPageTool{Bridge: client})
			slog.Info("search bridge started; web_search and fetch_page tools registered")
		}
	}

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
	// Agentic memory is enabled from the settings UI. Embedding is always
	// produced by the inbuilt local embedder (gte-small) — zero config,
	// no third-party service. The synthesis LLM is NOT configured here: it is
	// injected per request (WriteMemory/Recall) using the session's selected
	// model, so memory rides on whatever LLM the user is chatting with.
	dbMemCfg, err := session.GetMemoryConfig(globalDatabase)
	if err != nil {
		slog.Warn("failed to read memory config from DB", "err", err)
	} else if dbMemCfg.Enabled {
		embedP := provider.NewEmbedder()
		memStore, err := memory.Open(memory.DefaultDBPath())
		if err != nil {
			slog.Warn("failed to open memory store; memory disabled", "err", err)
		} else {
			mem = memory.New(memStore, &memory.GraphOpts{
				EmbedProvider: embedP,
			})
			s.mem = mem
			toolRegistry.Register(tool.NewMemoryRecallTool(mem, registry))
			slog.Info("agentic memory enabled (local embedder; synthesis uses session LLM)")
		}
	}

	// Eagerly download the inbuilt local embedder's model weights (the default
	// embedding backend) before the server accepts requests. The local embedder
	// is the default and may be enabled at runtime via the settings UI without a
	// restart, so we always run this preflight regardless of whether agentic
	// memory is configured yet — it ensures the one-time ~133 MB download is out
	// of the way. Subsequent LocalEmbedder instances share the cache directory
	// and skip the download entirely. Errors are non-fatal: the next Embed call
	// retries, so we log and continue rather than refusing to start.
	dlCtx, dlCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	if err := provider.EnsureLocalEmbedderModel(dlCtx); err != nil {
		slog.Warn("local embedder: startup model download failed; will retry on first use", "err", err)
	}
	dlCancel()

	s.permissions = permission.NewManager()
	s.loopRunner = &agent.LoopRunner{
		Store:           s.store,
		Bus:             s.bus,
		Registry:        registry,
		DefaultProvider: defaultProvider,
		Tools:           toolRegistry,
		Dir:             s.dir,
		Memory:          mem,
		NoteStore:       s.noteStore,
		Permissions:     s.permissions,
	}

	// Register deep_search after loopRunner is built (needs RunSearchSession).
	if s.searchBridge != nil {
		toolRegistry.Register(tool.DeepSearchTool{Run: s.loopRunner.RunSearchSession})
		slog.Info("deep_search tool registered")
	}

	// Register the task sub-agent tool (needs RunTaskSession). Available
	// regardless of the search bridge — the sub-agent is a read-only codebase
	// investigator that only optionally uses deep_search.
	toolRegistry.Register(tool.TaskTool{Run: s.loopRunner.RunTaskSession})

	// Initialize version manager
	s.versionManager = version.New()

	// Initialize PostHog analytics client from hardcoded credentials baked
	// into the binary. Analytics is always on; there is no user-facing
	// toggle. Events are sent server-side via the PostHog /capture REST endpoint.
	if PostHogAPIKey != "" {
		s.posthogClient = NewPostHogClient(PostHogAPIKey, PostHogAPIHost)
		if s.posthogClient != nil {
			s.posthogClient.Capture("ogcode_server_started", posthogDistinctID(), map[string]any{
				"mode": string(s.mode),
			})
			slog.Info("posthog analytics enabled", "host", PostHogAPIHost)
		}
	}

	r := s.routes()

	// Try ports starting from the configured port, up to 50 attempts.
	var listener net.Listener
	tryPort := s.port
	for i := 0; i < 50; i++ {
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

	// Stop search bridge
	if s.searchBridge != nil {
		s.searchBridge.Stop()
	}

	// Stop PostHog analytics client (flushes queued events)
	if s.posthogClient != nil {
		s.posthogClient.Capture("ogcode_server_stopped", posthogDistinctID(), nil)
		s.posthogClient.Stop()
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

// loadProviderMap builds the set of available LLM providers from DB-stored
// credentials and environment variables (env takes precedence). It is used both
// at startup and by reloadProviders to apply credential changes without a
// restart. Requires s.globalDB to be initialized.
func (s *Server) loadProviderMap() map[string]provider.Provider {
	providers := make(map[string]provider.Provider)

	dbProviderCfgs, err := session.GetAllProviderConfigs(s.globalDB)
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

	if key := resolveKey(os.Getenv("ANTHROPIC_API_KEY"), "anthropic"); key != "" {
		baseURL := resolveBaseURL(os.Getenv("ANTHROPIC_BASE_URL"), "anthropic")
		p, _ := provider.NewProviderWithConfig("anthropic", key, baseURL)
		providers["anthropic"] = p
		slog.Info("registered anthropic provider")
	}
	if key := resolveKey(os.Getenv("OPENAI_API_KEY"), "openai"); key != "" {
		baseURL := resolveBaseURL(os.Getenv("OPENAI_BASE_URL"), "openai")
		p, _ := provider.NewProviderWithConfig("openai", key, baseURL)
		providers["openai"] = p
		slog.Info("registered openai provider")
	}
	if key := resolveKey(os.Getenv("OPENROUTER_API_KEY"), "openrouter"); key != "" {
		p, _ := provider.NewProviderWithConfig("openrouter", key, "")
		providers["openrouter"] = p
		slog.Info("registered openrouter provider")
	}
	ollamaKey := resolveKey(os.Getenv("OLLAMA_API_KEY"), "ollama")
	ollamaBaseURL := resolveBaseURL(os.Getenv("OLLAMA_BASE_URL"), "ollama")
	// Detect a running/installed local Ollama instance. Registration is
	// driven by the live health probe + $PATH binary lookup (cross-platform),
	// replacing the old hardcoded /usr/local/bin + /opt/homebrew path checks.
	// The provider is registered when any of: an explicit key/base URL is set,
	// the binary is on $PATH, or the Ollama server responds to a health probe.
	ollamaStatus := provider.DetectOllama()
	ollamaDetected := ollamaKey != "" || ollamaBaseURL != "" || ollamaStatus.Installed || ollamaStatus.Running
	if ollamaDetected {
		if ollamaBaseURL == "" {
			ollamaBaseURL = ollamaStatus.BaseURL
		}
		if ollamaKey != "" && ollamaBaseURL == "" {
			slog.Warn("Ollama API key is set but no base URL configured; using http://localhost:11434/v1")
		}
		p, _ := provider.NewProviderWithConfig("ollama", ollamaKey, ollamaBaseURL)
		providers["ollama"] = p
		slog.Info("registered ollama provider",
			"installed", ollamaStatus.Installed, "running", ollamaStatus.Running, "baseUrl", ollamaBaseURL)

		// Auto-persist a lightweight Ollama config (base URL only, no API key) on
		// first detection so that subsequent launches treat Ollama as already
		// "configured" even when the server is not currently running. This prevents
		// the onboarding gate from bouncing the user back to the wizard when Ollama
		// is merely stopped (not uninstalled). We only insert when no row exists yet
		// so we never clobber a user-saved key or custom base URL.
		if _, exists := dbProviderMap["ollama"]; !exists && ollamaBaseURL != "" {
			if err := session.SetProviderConfig(s.globalDB, &session.ProviderConfig{
				ProviderID: "ollama",
				APIKey:     "",
				BaseURL:    ollamaBaseURL,
			}); err != nil {
				slog.Warn("failed to auto-persist ollama config", "err", err)
			} else {
				slog.Info("auto-persisted ollama provider config (base URL only)")
			}
		}
	}
	// Auto-provision free-tier providers from the shared community key pool
	// (a public GitHub-hosted JSON of OpenAI-compatible provider keys). These
	// give ogcode a zero-friction out-of-the-box experience: the user can start
	// chatting immediately using a free model without configuring anything.
	//
	// Free providers are keyed "ogcode-<collection>" so they coexist as
	// separately selectable instances. They never override a user's own
	// first-party credentials — those are registered above under their
	// canonical IDs ("openai", "anthropic", …). The fetch is best-effort and
	// cached locally so offline launches still work.
	freeCtx, freeCancel := context.WithTimeout(context.Background(), provider.FreePoolTimeout)
	freeDefs, freeErr := provider.FetchFreePool(freeCtx)
	freeCancel()
	if freeErr != nil {
		slog.Warn("free pool: unavailable (onboarding will require user-configured keys)", "err", freeErr)
	} else {
		for id, def := range freeDefs {
			regID := "ogcode-" + id
			if _, exists := providers[regID]; exists {
				continue // already registered (e.g. env var override)
			}
			// Don't shadow a user-configured OpenAI provider pointing at the
			// same collection's base URL.
			if op, ok := providers["openai"].(*provider.OpenAIProvider); ok && op != nil {
				if collectionFromBaseURLEq(op, def.BaseURL) {
					continue
				}
			}
			p, err := provider.NewFreePoolProvider(def)
			if err != nil {
				slog.Warn("free pool: skipping provider (no keys)", "id", id, "err", err)
				continue
			}
			providers[regID] = p
			slog.Info("registered free-tier provider", "id", regID, "collection", def.Collection, "baseURL", def.BaseURL)
		}
	}

	return providers
}

// collectionFromBaseURLEq reports whether an existing OpenAI provider's base URL
// resolves to the same collection as the given base URL. Used to avoid
// registering a free-pool provider that duplicates a user-configured endpoint.
func collectionFromBaseURLEq(p *provider.OpenAIProvider, baseURL string) bool {
	return p != nil && provider.CollectionFromBaseURL(p.BaseURL()) == provider.CollectionFromBaseURL(baseURL)
}

// credentials and swaps it into the running server in place, so credential
// changes from the settings/onboarding UI take effect without a restart. The
// shared *provider.Registry pointer (held by the loop runner and handlers) is
// preserved, and custom-model routing survives the swap.
func (s *Server) reloadProviders() {
	s.registry.ReplaceProviders(s.loadProviderMap())
	slog.Info("reloaded provider registry", "providers", s.registry.List())
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
