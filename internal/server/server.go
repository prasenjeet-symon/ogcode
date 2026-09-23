package server

import (
	"context"
	"errors"
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/config"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	"github.com/prasenjeet-symon/ogcode/internal/git"
	"github.com/prasenjeet-symon/ogcode/internal/indexer"
	"github.com/prasenjeet-symon/ogcode/internal/mcp"
	"github.com/prasenjeet-symon/ogcode/internal/memfile"
	"github.com/prasenjeet-symon/ogcode/internal/note"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/plan"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/question"
	"github.com/prasenjeet-symon/ogcode/internal/resource"
	"github.com/prasenjeet-symon/ogcode/internal/search"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/skill"
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
	port            atomic.Int32 // written once by Serve after bind, read cross-goroutine via Port()
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
	permissions     *permission.Manager
	questions       *question.Manager
	skillLoader     *skill.Loader
	mcpManager      *mcp.Manager
	// toolRegistry is the live tool set the agent loop reads each step. Held so
	// the MCP settings toggle can register a newly enabled server's tools and
	// remove a disabled one's, taking effect on the next turn.
	toolRegistry *tool.Registry
	// mcpConnect, when non-nil, dials the MCP servers lazily after the HTTP
	// server starts listening (set in Start, invoked from the goroutine below).
	mcpConnect func()
	// mcpCancel cancels the lazy-connect context on shutdown so an in-flight
	// OAuth/dial does not block past the server's lifetime.
	mcpCancel context.CancelFunc

	// Version check manager
	versionManager *version.Manager

	// ogxPending holds the single-use states of OGX connect flows awaiting
	// their browser redirect. Zero value ready; see ogx_routes.go.
	ogxPending ogxPendingStates

	// searchBackend is the active web-search backend, or nil when the user has
	// turned search off. It is compiled in, so there is no process to manage.
	// When search is on this is a *search.SwitchableBackend (also held in
	// searchSwitch) so the provider can be swapped live.
	searchBackend search.Backend

	// searchSwitch is the live handle onto the search backend, non-nil only while
	// search is enabled. Changing the search provider or key in settings rebuilds
	// the concrete backend and Sets it here, so the change applies without a
	// restart. (The enable toggle still needs a restart — it changes which tools
	// are registered.)
	searchSwitch *search.SwitchableBackend

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

	// resources samples this process's own CPU/memory for the UI. It idles
	// while no client is watching, so it costs nothing with no UI open.
	resources       *resource.Sampler
	resourcesCancel context.CancelFunc

	// docindexMu protects docindexRunning.
	docindexMu      sync.Mutex
	docindexRunning bool
	indexerProgress *indexer.ProgressTracker // nil when not indexing

	// opts carries the runtime toggles set at construction (see Options).
	opts Options

	// stopCh is closed by Stop() to end the Serve wait loop without a signal.
	// Allocated in serve; Stop before Serve is a no-op by design (Serve is what
	// wires the shutdown path). stopOnce guards a double Stop. stopMu guards the
	// field itself: Stop and a second Serve read it cross-goroutine while serve
	// writes it (pinned race-free by the lifecycle tests under -race).
	stopMu   sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once
}

func New(port int, dir string, mode ServerMode) *Server {
	return NewWithOptions(port, dir, mode, Options{})
}

// Options tunes a server's runtime behavior without changing what it serves.
// The zero value reproduces the interactive `ogcode serve` behavior.
type Options struct {
	// NoBrowser suppresses opening the operator's browser on start. The worker
	// spawns one server per worktree; none of them should open a tab.
	NoBrowser bool
	// Loopback binds the listener to 127.0.0.1 instead of all interfaces. The
	// worker's tunnel is the only remote path to a hosted server, so a hosted
	// server must not also be reachable on the machine's LAN address.
	Loopback bool
	// OnListen, when set, is called once with the ACTUAL bound port right after
	// the listener is established — which can differ from the requested port when
	// that one was busy and the bind loop walked past it. The CLI uses it to
	// record a project's port; leave nil to skip (worker-spawned servers do).
	OnListen func(port int)
}

func NewWithOptions(port int, dir string, mode ServerMode, opts Options) *Server {
	s := &Server{dir: dir, mode: mode, opts: opts, running: make(map[session.SessionID]context.CancelFunc), runningToken: make(map[session.SessionID]uint64), loopControls: make(map[session.SessionID]*agent.LoopControl)}
	s.port.Store(int32(port))
	return s
}

// Serve runs the HTTP server until ctx is cancelled, a SIGINT/SIGTERM arrives,
// or Stop is called — then shuts down gracefully (in-flight requests drained
// within the 10s shutdown timeout, MCP torn down, DBs closed) and returns. The
// signal path keeps standalone `ogcode serve` behavior unchanged; a ctx-driven
// caller (the worker hosting one server per worktree) cancels its ctx per
// worktree and never has signals racing across N servers. Serve must be called
// once per Server.
func (s *Server) Serve(ctx context.Context) error {
	s.stopMu.Lock()
	started := s.stopCh != nil
	s.stopMu.Unlock()
	if started {
		return errors.New("ogcode server: Serve called twice")
	}
	return s.serve(ctx)
}

// Stop asks a Serve loop to begin graceful shutdown; Serve then returns once
// shutdown completes. Safe to call from another goroutine; a no-op when Serve
// has not started (or has already finished).
func (s *Server) Stop() {
	s.stopMu.Lock()
	ch := s.stopCh
	s.stopMu.Unlock()
	if ch != nil {
		s.stopOnce.Do(func() { close(ch) })
	}
}

// Port reports the port the server actually bound. Meaningful only after Serve
// has bound the listener (the caller's Serve goroutine is running); callers
// that passed port 0 read the kernel-assigned value here to dial the tunnel.
func (s *Server) Port() int {
	return int(s.port.Load())
}

func (s *Server) serve(ctx context.Context) error {
	dbPath := filepath.Join(s.dir, ".ogcode", "ogcode.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// The workspace's public/ folder is served over HTTP at /public by every
	// server instance (interactive, plan-mode, worker-hosted) — see routes().
	// Create it eagerly so the route is stable even when it is still empty.
	if err := s.ensurePublicDir(); err != nil {
		return fmt.Errorf("create public dir: %w", err)
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

	// 2s cadence, 120 samples retained — a four-minute window, which is long
	// enough to see a memory reindex spike rise and fall.
	s.resources = resource.NewSampler(2*time.Second, 120)
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	s.resourcesCancel = resourceCancel
	go s.resources.Run(resourceCtx)

	s.store = session.NewStore(database)
	s.planStore = plan.NewStore(database)
	s.taskStore = task.NewStore(database)
	s.noteStore = note.NewStore(database)
	s.docindexStore = docindex.NewStore(database)
	// Per-turn markdown memory: the index over .ogcode/memory/ lives in the same
	// project-local DB, and one barrier serializes background summary writes
	// against recall. Both are cheap to construct even when the feature is off.
	memfileStore := memfile.NewStore(database)
	memBarrier := memfile.NewManager()

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

	// Initialize tools. The core set is shared with every other entry point
	// (see tool.RegisterCoreTools) so a tool the agents' prompts name by id
	// cannot be present here and missing there; what follows is what only the
	// server offers.
	toolRegistry := tool.NewRegistry()
	tool.RegisterCoreTools(toolRegistry, s.docindexStore)
	toolRegistry.Register(tool.BreakdownTool{})
	toolRegistry.Register(tool.NewSubmitDocIndexTool(s.docindexStore))
	toolRegistry.Register(tool.NewMemoryMapTool(memfileStore))

	// Skills: the "skills" section of ogcode.json decides which extra
	// directories and remote manifests are consulted; the standard project and
	// global skill directories are scanned regardless, so a project with no
	// config still picks up the skills a user has written.
	fullCfg := config.Load(s.dir)
	skillCfg := fullCfg.Skills
	skillLoader := skill.NewLoader(skill.Config{
		Paths:       skillCfg.Paths,
		URLs:        skillCfg.URLs,
		Permissions: skillCfg.Permissions,
	})
	toolRegistry.Register(tool.NewSkillTool(skillLoader))
	s.skillLoader = skillLoader
	s.toolRegistry = toolRegistry

	// MCP servers: build the Manager now (cheap — binds the OAuth callback
	// receiver only) but defer the actual connections to after the HTTP server
	// is listening, via a background goroutine (s.mcpConnect). Connecting earlier
	// blocked startup on slow/OAuth servers: an OAuth-requiring server could hold
	// startup for up to authTimeout (5 min), during which no HTTP server was
	// listening and the UI could not surface the OAuth prompt. With lazy connect
	// the UI is live when the browser opens. Tools are registered as they
	// arrive; the tool.Registry is locked for concurrent Register+ForAgent.
	// Failures to connect to an individual server are logged but do not prevent
	// startup; the server simply contributes no tools. Tools are registered as
	// "mcp_<server>_<tool>" (the "mcp_" prefix makes the id match the "mcp_*"
	// glob in the coding agent's toolset) and picked up automatically.
	mcpMgr, mcpErr := mcp.New(context.Background(), fullCfg)
	if mcpErr != nil {
		slog.Warn("mcp: manager construction failed", "err", mcpErr)
	}
	s.mcpManager = mcpMgr
	// A cancellable context for the lazy connect: shutdown cancels it so an
	// in-flight OAuth/dial does not outlive the server (the goroutine unblocks
	// via ctx cancellation rather than waiting out the 5-min authTimeout).
	mcpCtx, mcpCancel := context.WithCancel(context.Background())
	s.mcpCancel = mcpCancel
	// Deferred connect runs after s.routes()/listener bind below; see the
	// goroutine launched just before the HTTP server starts serving.
	s.mcpConnect = func() {
		tools, err := mcpMgr.Connect(mcpCtx)
		for _, t := range tools {
			toolRegistry.Register(t)
		}
		if err != nil {
			slog.Warn("mcp: one or more servers failed to connect", "err", err)
		}
		if len(tools) > 0 {
			slog.Info("mcp: tools registered after lazy connect", "count", len(tools))
		}
	}

	// Web search. On by default — the backend is compiled into this binary, so
	// there is nothing to install and nothing to start. It is resolved before
	// loopRunner is built so RunSearchSession can be wired in.
	//
	// Precedence: OGCODE_SEARCH_ENABLED wins when set (either direction, for
	// scripted and CI runs), otherwise the settings-screen toggle decides. A
	// database that cannot be read leaves search on rather than silently
	// stripping the research tools.
	searchCfg, err := session.GetSearchConfig(globalDatabase)
	if err != nil {
		slog.Warn("failed to read search config from DB; leaving web search enabled on the native engine", "err", err)
		searchCfg = &session.SearchConfig{Enabled: true, Provider: session.SearchProviderNative}
	}
	searchEnabled := searchCfg.Enabled
	if v := os.Getenv("OGCODE_SEARCH_ENABLED"); v != "" {
		searchEnabled = strings.EqualFold(v, "true")
	}

	// searchBackend is an interface, so only ever assign a non-nil implementation
	// to it: a typed-nil pointer would still compare != nil and would get dead
	// tools registered against it.
	var searchBackend search.Backend
	if searchEnabled {
		// The concrete backend is chosen from config, then wrapped in a
		// SwitchableBackend so a later provider change can be applied live. The
		// tools and the deep-research pipeline hold the wrapper, not the concrete
		// backend, so swapping it in place needs no re-registration.
		sw := search.NewSwitchableBackend(buildSearchBackend(searchCfg))
		s.searchSwitch = sw
		searchBackend = sw
		s.searchBackend = sw

		toolRegistry.Register(tool.WebSearchTool{Bridge: sw})
		toolRegistry.Register(tool.FetchPageTool{Bridge: sw})
		logSearchProvider("web search enabled", searchCfg)
		slog.Info("web_search and fetch_page tools registered")
	} else {
		slog.Info("web search disabled by configuration")
	}

	// Determine default provider. DefaultUsable applies the stable priority but
	// skips an installed-but-stopped Ollama so the user's own keys are preferred
	// over a daemon that would just refuse.
	defaultProvider := registry.DefaultUsable()
	if defaultProvider == nil {
		slog.Warn("no LLM provider configured; set ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY, OLLAMA_API_KEY, or install Ollama")
		defaultProvider = provider.NewAnthropicProvider()
	}

	s.registry = registry
	s.defaultProvider = defaultProvider

	// Custom model definitions and model enable/disable preferences live in the
	// global config DB so they persist across every project/workspace (like
	// provider credentials). Older builds stored them in the per-project DB, so
	// first backfill any that predate the move (non-destructively).
	s.migrateModelPreferencesToGlobal()

	// Load custom model preferences from the global DB and register their routing.
	prefs, err := session.GetModelPreferences(s.globalDB)
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

	s.permissions = permission.NewManager()
	s.questions = question.NewManager()
	s.loopRunner = &agent.LoopRunner{
		Store:           s.store,
		Bus:             s.bus,
		Registry:        registry,
		DefaultProvider: defaultProvider,
		Tools:           toolRegistry,
		Dir:             s.dir,
		// Interactive turns get a generous 10,000-iteration budget, plus
		// auto-resume: a turn that still exhausts it extends up to twice more
		// (a 30,000-iteration hard ceiling) instead of stranding the user
		// mid-task, then stops but stays continuable. Reaching even 10,000 is
		// extraordinary, so the ceiling is a runaway backstop, not a normal
		// operating point.
		MaxSteps:       10000,
		MaxAutoResumes: 2,
		MemFiles:       memfileStore,
		MemBarrier:     memBarrier,
		TurnMemory:     memfile.TurnMemoryEnabled(),
		NoteStore:      s.noteStore,
		SearchBridge:   searchBackend,
		// Whether the agent may compact its own context mid-turn is a per-project
		// choice, so it is read from the project DB (not the global one), once at
		// the start of each turn — flipping it in the settings screen applies to
		// the next turn without a restart.
		CompactContextEnabled: func() bool { return session.CompactContextEnabled(database) },
		// Lets the system prompt say up front whether codebase_map has anything
		// to return, so a session in an unindexed project does not spend a call
		// finding out. Queried per turn, so building the index mid-session is
		// reflected on the next one.
		IndexedFileCount: func(dir string) int {
			paths, err := s.docindexStore.ListDocPaths(dir)
			if err != nil {
				// Unknown beats wrong: -1 omits the line and leaves the agent on
				// the probe-and-recover path rather than asserting "not indexed"
				// about a project that may well be.
				slog.Warn("index status lookup failed, omitting from prompt", "dir", dir, "err", err)
				return -1
			}
			return len(paths)
		},
		Permissions: s.permissions,
		Questions:   s.questions,
		Skills:      skillLoader,
	}

	// Register deep_search after loopRunner is built (needs RunSearchSession).
	if searchBackend != nil {
		toolRegistry.Register(tool.DeepSearchTool{Run: s.loopRunner.RunSearchSession})
		slog.Info("deep_search tool registered")
	}

	// Register the task sub-agent tool (needs RunTaskSession). Available
	// regardless of the search bridge — the sub-agent is a read-only codebase
	// investigator that only optionally uses deep_search.
	toolRegistry.Register(tool.TaskTool{Run: s.loopRunner.RunTaskSession})

	// Register ask_user (needs AskUser). It is offered only in an interactive,
	// permission-gated turn — the agent loop decides that per turn — so
	// registering it here is harmless for the headless paths that share this
	// registry.
	toolRegistry.Register(tool.AskUserTool{Ask: s.loopRunner.AskUser})

	// Memory recall tools delegate to the read-only recall sub-agent over the
	// project's markdown turn summaries, waiting on the summary barrier first.
	// Registered only when turn-memory is on (its env gate); off ⇒ no memory.
	if memfile.TurnMemoryEnabled() {
		recallFn := s.loopRunner.RunMemoryRecallSession
		toolRegistry.Register(tool.NewMemoryRecallTool(recallFn, memBarrier))
		toolRegistry.Register(tool.NewProjectMemoryRecallTool(recallFn, memBarrier))
	}

	// Repair any interactive session whose last turn was cut short by a process
	// that is no longer running. A crash records nothing on the way out, and
	// what it leaves behind — a turn with no finish reason, a tool call nothing
	// answered — can make the session's next request invalid, whether that
	// request comes from a resume or from the user simply typing again. This
	// needs loopRunner, so it runs here rather than beside the task recovery
	// above, and before the HTTP server can accept anything.
	s.recoverInterruptedSessions()

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
	host := ""
	if s.opts.Loopback {
		host = "127.0.0.1"
	}
	var listener net.Listener
	tryPort := int(s.port.Load())
	for i := 0; i < 50; i++ {
		l, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", tryPort)))
		if err == nil {
			listener = l
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
		return fmt.Errorf("no available port found (tried %d–%d)", s.port.Load(), tryPort-1)
	}
	// Surface the ACTUAL bound port: tryPort 0 means the kernel picked one, and
	// a caller that dials the tunnel needs the real value (Port()).
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
		s.port.Store(int32(tcpAddr.Port))
	}
	// Report the port actually bound (post-walk) so the CLI can remember it for
	// this project. Fired before serving begins; nil for callers that don't care.
	if s.opts.OnListen != nil {
		s.opts.OnListen(int(s.port.Load()))
	}

	addr := listener.Addr().String()
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  0,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	url := fmt.Sprintf("http://localhost:%d", s.port.Load())
	slog.Info("starting ogcode server", "addr", addr, "dir", s.dir)
	if !s.opts.NoBrowser {
		go openBrowser(url)
	}

	// MCP servers connect lazily now that the HTTP server is listening — the
	// UI/bus are live so an OAuth-required server's browser prompt reaches the
	// user instead of blocking a startup that has no listener yet.
	if s.mcpConnect != nil {
		go s.mcpConnect()
	}

	// signalCh owns process signals; stopCh is the programmatic Stop() path, so
	// a ctx-driven caller (the worker hosting N servers) is never torn down by a
	// signal meant for some other component.
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signalCh)
	s.stopMu.Lock()
	s.stopCh = make(chan struct{})
	s.stopMu.Unlock()

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			signalCh <- syscall.SIGTERM
		}
	}()

	s.stopMu.Lock()
	stopCh := s.stopCh
	s.stopMu.Unlock()

	select {
	case <-ctx.Done():
	case <-signalCh:
	case <-stopCh:
	}
	slog.Info("shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Shutdown HTTP server (closes all connections and releases port)
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("server shutdown error", "err", err)
	}

	// Close listener explicitly
	if err := listener.Close(); err != nil {
		slog.Warn("close listener", "err", err)
	}

	// Stop the resource sampler.
	if s.resourcesCancel != nil {
		s.resourcesCancel()
	}

	// Cancel any in-flight lazy MCP connect so its dials/OAuth unblock before
	// we tear the Manager down. Connect's own race-guard then closes any session
	// that landed after this point.
	if s.mcpCancel != nil {
		s.mcpCancel()
	}

	// Close MCP server connections (terminates stdio subprocesses and HTTP
	// sessions). Done after the HTTP server is down so in-flight tool calls
	// have already been cancelled by the context.
	if s.mcpManager != nil {
		if err := s.mcpManager.Close(); err != nil {
			slog.Warn("close mcp manager", "err", err)
		}
	}

	// Stop PostHog analytics client (flushes queued events)
	if s.posthogClient != nil {
		s.posthogClient.Capture("ogcode_server_stopped", posthogDistinctID(), nil)
		s.posthogClient.Stop()
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
	// A base URL persisted from an earlier launch must not permanently shadow a
	// live endpoint that detection just found. Without this, a row written when
	// local Ollama was installed keeps pointing at a dead localhost:11434 even
	// after the user has moved to a router. An explicit OLLAMA_BASE_URL stays
	// authoritative and is never second-guessed.
	if os.Getenv("OLLAMA_BASE_URL") == "" {
		if live := provider.PreferLiveOllamaEndpoint(ollamaBaseURL, ollamaStatus); live != ollamaBaseURL {
			slog.Info("persisted ollama endpoint is not responding; using detected endpoint",
				"stale", ollamaBaseURL, "detected", live)
			ollamaBaseURL = live
		}
	}
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
	// OGX: a connected OG Lab subscription, whose gateway serves exactly the
	// models the plan grants. Registered only when the link carries a plan — a
	// planless link would register a provider that can serve nothing yet still
	// outranks the other providers in ProviderPriority.
	if acct, err := session.GetOGXAccount(s.globalDB); err != nil {
		slog.Warn("failed to load ogx account", "err", err)
	} else if acct.HasPlan() {
		if p, err := provider.NewOGXProvider(acct.Token); err == nil {
			providers[provider.OGXProviderID] = p
			slog.Info("registered ogx provider", "plan", acct.Plan)
		}
	}

	return providers
}

// credentials and swaps it into the running server in place, so credential
// changes from the settings/onboarding UI take effect without a restart. The
// shared *provider.Registry pointer (held by the loop runner and handlers) is
// preserved, and custom-model routing survives the swap.
func (s *Server) reloadProviders() {
	s.registry.ReplaceProviders(s.loadProviderMap())
	slog.Info("reloaded provider registry", "providers", s.registry.List())
}

// migrateModelPreferencesToGlobal backfills the global config DB with any model
// preferences an older build wrote to this workspace's per-project DB. It only
// inserts IDs not already present globally, so it never clobbers a preference
// the user has since changed, and it leaves the per-project rows untouched
// (harmless once every read points at the global DB). Best-effort: a failure
// here must never block startup.
func (s *Server) migrateModelPreferencesToGlobal() {
	local, err := session.GetModelPreferences(s.db)
	if err != nil || len(local) == 0 {
		return
	}
	global, err := session.GetModelPreferences(s.globalDB)
	if err != nil {
		slog.Warn("model-preference migration: read global DB", "err", err)
		return
	}
	seen := make(map[string]bool, len(global))
	for _, p := range global {
		seen[p.ID] = true
	}
	migrated := 0
	for _, p := range local {
		if seen[p.ID] {
			continue
		}
		if err := session.SetModelPreference(s.globalDB, p); err != nil {
			slog.Warn("model-preference migration: write global DB", "id", p.ID, "err", err)
			continue
		}
		migrated++
	}
	if migrated > 0 {
		slog.Info("migrated model preferences to global config DB", "count", migrated)
	}
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

// buildSearchBackend adapts this server's config to search.BuildBackend, which
// is the single source of truth for provider selection and chain order — shared
// with the headless CLI so the two entry points cannot offer deep_search backed
// by different engines. Called both at startup and on a live provider change.
//
// The chain itself is documented on search.BuildBackend; the short version is
// that the native engines are always the last link, so a Tavily failure falls
// through rather than losing an answerable query.
func buildSearchBackend(cfg *session.SearchConfig) search.Backend {
	return search.BuildBackend(cfg.Provider, cfg.TavilyAPIKey)
}

// tavilyKeyFor returns the Tavily key in effect: the environment overrides the
// stored value, mirroring the provider-key env overlay.
func tavilyKeyFor(cfg *session.SearchConfig) string {
	if env := strings.TrimSpace(os.Getenv("TAVILY_API_KEY")); env != "" {
		return env
	}
	return strings.TrimSpace(cfg.TavilyAPIKey)
}

// logSearchProvider records which backend cfg resolves to, with a warning when
// Tavily is selected but unusable (so it silently runs on native).
func logSearchProvider(prefix string, cfg *session.SearchConfig) {
	if cfg.Provider == session.SearchProviderTavily {
		if tavilyKeyFor(cfg) != "" {
			slog.Info(prefix + "; provider=tavily (native fallback)")
			return
		}
		slog.Warn(prefix + "; provider=tavily but no API key is configured — using the native engine")
		return
	}
	slog.Info(prefix + "; provider=native")
}
