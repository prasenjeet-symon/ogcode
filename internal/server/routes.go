package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	r.Route("/api", func(r chi.Router) {
		r.Get("/event", s.handleEvent)
		r.Get("/path", s.handlePath)
		r.Get("/agent", s.handleAgents)
		r.Get("/models", s.handleModels)
		r.Post("/models/refresh", s.handleModelsRefresh)
		r.Get("/config", s.handleConfig)
		r.Get("/mode", s.handleMode)
		r.Get("/git/sync", s.handleGitSync)

		r.Post("/models/preference", s.handleSetModelPreference)
		r.Delete("/models/preference/{id}", s.handleDeleteModelPreference)
		r.Post("/models/capability/clear", s.handleClearModelCapability)

		r.Get("/theme", s.handleGetTheme)
		r.Post("/theme", s.handleSetTheme)
		r.Delete("/theme/{directory}", s.handleDeleteTheme)

		r.Get("/memory/config", s.handleGetMemoryConfig)
		r.Post("/memory/config", s.handleSetMemoryConfig)

		r.Get("/search/config", s.handleGetSearchConfig)
		r.Post("/search/config", s.handleSetSearchConfig)

		r.Get("/providers/config", s.handleGetProviderConfigs)
		r.Post("/providers/config/{id}", s.handleSetProviderConfig)
		r.Post("/providers/config/{id}/validate", s.handleValidateProviderConfig)

		r.Get("/pricing", s.handleGetPricing)

		r.Route("/session", func(r chi.Router) {
			r.Get("/", s.handleListSessions)
			r.Post("/", s.handleCreateSession)

			r.Route("/{sessionID}", func(r chi.Router) {
				r.Get("/", s.handleGetSession)
				r.Patch("/", s.handleUpdateSession)
				r.Delete("/", s.handleDeleteSession)
				r.Post("/abort", s.handleAbortSession)
				r.Post("/prompt", s.handlePrompt)
				r.Get("/message", s.handleGetMessages)
				r.Post("/permission/{permissionID}", s.handlePermissionReply)
			})
		})

		r.Route("/plans", func(r chi.Router) {
			r.Get("/", s.handleListPlans)
			r.Post("/", s.handleCreatePlan)
			r.Route("/{planID}", func(r chi.Router) {
				r.Get("/", s.handleGetPlan)
				r.Patch("/", s.handleUpdatePlan)
				r.Delete("/", s.handleDeletePlan)
				r.Post("/lock", s.handleLockPlan)
				r.Post("/abort", s.handleAbortPlan)
				r.Post("/prompt", s.handlePlanPrompt)
				r.Get("/message", s.handleGetPlanMessages)
				r.Get("/export", s.handleExportPlan)
				r.Get("/tasks", s.handleListTasks)
				r.Post("/tasks", s.handleCreateTasks)
			})
		})

		r.Route("/tasks", func(r chi.Router) {
			r.Route("/{taskID}", func(r chi.Router) {
				r.Get("/", s.handleGetTask)
				r.Patch("/", s.handleUpdateTask)
				r.Post("/start", s.handleStartTask)
				r.Post("/complete", s.handleCompleteTask)
				r.Post("/fail", s.handleFailTask)
				r.Post("/retry", s.handleRetryTask)
			})
		})

		r.Route("/notes", func(r chi.Router) {
			r.Get("/", s.handleListNotes)
			r.Post("/", s.handleCreateNote)
			r.Post("/transform", s.handleTransformText)
			r.Route("/{noteID}", func(r chi.Router) {
				r.Get("/", s.handleGetNote)
				r.Patch("/", s.handleUpdateNote)
				r.Delete("/", s.handleDeleteNote)
				r.Get("/versions", s.handleListNoteVersions)
				r.Get("/export", s.handleExportNote)
			})
		})

		r.Get("/vcs", s.handleVCS)
		r.Get("/version", s.handleVersion)
		r.Post("/version/check", s.handleVersionCheck)

		// Doc index
		r.Route("/docindex", func(r chi.Router) {
			r.Get("/build", s.handleDocIndexBuildStatus)
			r.Post("/build", s.handleBuildDocIndex)
			r.Get("/docs", s.handleListIndexedDocs)
			r.Get("/excludes", s.handleListExcludes)
			r.Post("/excludes", s.handleAddExclude)
			r.Delete("/excludes/{id}", s.handleDeleteExclude)
		})

		// LaTeX compilation and rendering
		s.registerLatexRoutes(r)
	})

	// Serve embedded web UI (or placeholder for dev)
	s.serveStatic(r)

	return r
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Headers", "Content-Type, x-ogcode-directory")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
