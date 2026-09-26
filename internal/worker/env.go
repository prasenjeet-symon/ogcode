package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/config"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/mcp"
	"github.com/prasenjeet-symon/ogcode/internal/modelcatalog"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/question"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/skill"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// env is the agent-hosting environment for one workspace directory: the DB,
// provider/tool/skill/MCP wiring, event bus, and shared LoopRunner. It mirrors
// the headless wiring in internal/cli/run.go (runPrompt) so a worker-hosted
// session behaves exactly like a local `ogcode run` in that directory.
//
// One env is built per directory and shared across every session hosted there
// — the same one-runner-many-sessions shape the interactive server uses (see the
// LoopRunner.MaxAutoResumes comment in internal/agent/loop.go). Building per
// directory (not per session) matters most for MCP, which spawns subprocesses.
type env struct {
	dir    string
	db     *db.DB
	global *db.DB
	bus    *bus.Bus
	store  *session.Store
	runner *agent.LoopRunner
	mcp    *mcp.Manager
	// permissions gates mutating tool calls (bash/write/edit) behind operator
	// approval. It is shared across every session in this env, exactly as the
	// server shares one permission.Manager across all its sessions. The loop
	// prompts only when the session ctx carries WithPermissionGating, so
	// ungated runs never block on it.
	permissions *permission.Manager
}

// buildEnv wires an env for dir, following runPrompt's setup step for step.
func buildEnv(ctx context.Context, dir string, logger *slog.Logger) (*env, error) {
	if path := config.EnsureProjectFile(dir); path != "" {
		logger.Info("created project config file", "path", path)
	}

	// Project DB (per workspace) + global config DB (provider keys).
	dbPath := filepath.Join(dir, ".ogcode", "ogcode.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	home, _ := os.UserHomeDir()
	globalDBPath := filepath.Join(home, ".ogcode", "config.db")
	if err := os.MkdirAll(filepath.Dir(globalDBPath), 0o755); err != nil {
		database.Close()
		return nil, fmt.Errorf("create global config dir: %w", err)
	}
	globalDatabase, err := db.Open(globalDBPath)
	if err != nil {
		database.Close()
		return nil, fmt.Errorf("open global config database: %w", err)
	}
	// The global DB now outlives provider resolution: the permission manager
	// keeps a store on it for the env's whole lifetime, so it is released in
	// Close rather than here.

	registry, defaultProvider, err := buildProviderRegistry(globalDatabase)
	if err != nil {
		database.Close()
		return nil, err
	}

	fullCfg := config.Load(dir)

	// Tool registry — same set as the headless run, minus BreakdownTool.
	toolRegistry := tool.NewRegistry()
	toolRegistry.Register(tool.BashTool{})
	toolRegistry.Register(tool.ReadTool{})
	toolRegistry.Register(tool.NewCompactContextTool())
	toolRegistry.Register(tool.WriteTool{})
	toolRegistry.Register(tool.EditTool{})
	toolRegistry.Register(tool.GlobTool{})
	toolRegistry.Register(tool.GrepTool{})
	toolRegistry.Register(tool.ViewImageTool{})

	skillCfg := fullCfg.Skills
	skillLoader := skill.NewLoader(skill.Config{
		Paths:       skillCfg.Paths,
		URLs:        skillCfg.URLs,
		Permissions: skillCfg.Permissions,
	})
	toolRegistry.Register(tool.NewSkillTool(skillLoader))

	// MCP servers: construct + connect synchronously, as the headless CLI does.
	mcpMgr, mcpErr := mcp.New(ctx, fullCfg)
	if mcpErr != nil {
		logger.Warn("mcp: manager construction failed", "err", mcpErr)
	}
	if mcpMgr != nil {
		mcpTools, connErr := mcpMgr.Connect(ctx)
		if connErr != nil {
			logger.Warn("mcp: one or more servers failed to connect", "err", connErr)
		}
		for _, t := range mcpTools {
			toolRegistry.Register(t)
		}
	}

	b := bus.New(1024)
	store := session.NewStore(database)

	// One permission manager per env, shared across every session hosted in this
	// directory — mirroring server.go:352. The loop only consults it for gated
	// sessions, so headless task/index runs inside a hosted session are unaffected.
	perm := permission.NewManager(permission.NewStore(globalDatabase))
	// ask_user's manager, same lifetime and rationale as the permission one: the
	// browser reaches this worktree server's HTTP through the master tunnel, so
	// the question routes work here exactly as in an interactive local server.
	quest := question.NewManager()

	lr := &agent.LoopRunner{
		Store:           store,
		Bus:             b,
		Registry:        registry,
		DefaultProvider: defaultProvider,
		Tools:           toolRegistry,
		Dir:             dir,
		Skills:          skillLoader,
		Permissions:     perm,
		Questions:       quest,
	}
	// The build agent advertises the task sub-agent tool, so it must resolve.
	toolRegistry.Register(tool.TaskTool{Run: lr.RunTaskSession})
	toolRegistry.Register(tool.AskUserTool{Ask: lr.AskUser})

	return &env{dir: dir, db: database, global: globalDatabase, bus: b, store: store, runner: lr, mcp: mcpMgr, permissions: perm}, nil
}

// Close tears down the env's MCP subprocesses and databases.
func (e *env) Close() {
	if e.mcp != nil {
		e.mcp.Close()
	}
	if e.db != nil {
		e.db.Close()
	}
	if e.global != nil {
		e.global.Close()
	}
}

// buildProviderRegistry replicates runPrompt's provider resolution: env vars win
// over DB-stored keys. It returns an error when no usable provider is
// configured.
func buildProviderRegistry(globalDatabase *db.DB) (*provider.Registry, provider.Provider, error) {
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
	if os.Getenv("OLLAMA_BASE_URL") == "" {
		ollamaBaseURL = provider.PreferLiveOllamaEndpoint(ollamaBaseURL, provider.DetectOllama())
	}
	if ollamaKey != "" || ollamaBaseURL != "" {
		if p, e := provider.NewProviderWithConfig("ollama", ollamaKey, ollamaBaseURL); e == nil {
			registry.Register(p)
		}
	}
	// OGX is stored in the global config DB (the plan follows the user, not the
	// project), so it is registered when that DB is available and the link
	// carries a plan.
	if acct, e := session.GetOGXAccount(globalDatabase); e == nil && acct.HasPlan() {
		if p, e := provider.NewOGXProvider(acct.Token); e == nil {
			registry.Register(p)
		}
	}

	// Seed each provider's catalogue from the persisted copy so a session's
	// first read (a title generation, a model pick) answers without a live
	// fetch. A worker has no long-lived picker to refresh, so seeding is enough.
	if e := modelcatalog.Seed(registry, globalDatabase); e != nil {
		slog.Warn("seed model catalog failed", "err", e)
	}

	defaultProvider := registry.DefaultUsable()
	if defaultProvider == nil {
		return nil, nil, fmt.Errorf("no provider configured — set ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY, or OLLAMA_BASE_URL")
	}
	return registry, defaultProvider, nil
}
