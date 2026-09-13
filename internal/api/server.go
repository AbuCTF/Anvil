package api

import (
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/api/handlers"
	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/anvil-lab/anvil/internal/services/storage"
	"github.com/anvil-lab/anvil/internal/services/upload"
	"github.com/anvil-lab/anvil/internal/services/vm"
	"github.com/anvil-lab/anvil/internal/services/vpn"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Server represents the API server
type Server struct {
	config       *config.Config
	db           *database.DB
	containerSvc *container.Service
	vmSvc        *vm.Service
	uploadSvc    *upload.Service
	storageSvc   storage.StorageBackend
	vpnSvc       *vpn.Service
	logger       *zap.Logger
	router       *gin.Engine
}

// NewServer creates a new API server
func NewServer(
	cfg *config.Config,
	db *database.DB,
	containerSvc *container.Service,
	vmSvc *vm.Service,
	uploadSvc *upload.Service,
	storageSvc storage.StorageBackend,
	vpnSvc *vpn.Service,
	logger *zap.Logger,
) *Server {
	// Set Gin mode based on environment
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	s := &Server{
		config:       cfg,
		db:           db,
		containerSvc: containerSvc,
		vmSvc:        vmSvc,
		uploadSvc:    uploadSvc,
		storageSvc:   storageSvc,
		vpnSvc:       vpnSvc,
		logger:       logger,
	}

	s.setupRouter()
	return s
}

// Router returns the HTTP router
func (s *Server) Router() http.Handler {
	return s.router
}

func (s *Server) setupRouter() {
	r := gin.New()
	if err := r.SetTrustedProxies(s.config.Server.TrustedProxies); err != nil {
		s.logger.Error("invalid trusted proxy configuration", zap.Error(err))
		// Invalid proxy configuration must not leave Gin trusting forwarded
		// addresses from arbitrary clients.
		_ = r.SetTrustedProxies(nil)
	}

	// Set max multipart memory for large file uploads (20GB)
	r.MaxMultipartMemory = 20 << 30 // 20GB

	// Global middleware
	r.Use(gin.Recovery())
	r.Use(middleware.Logger(s.logger))
	r.Use(middleware.CORS())
	r.Use(middleware.RequestID())
	r.Use(middleware.SecurityHeaders())

	// Health probes must remain independent from user traffic limits.
	r.GET("/health", s.healthCheck)
	r.GET("/api/health", s.healthCheck)

	// Rate limiting (if enabled)
	if s.config.RateLimit.Enabled {
		r.Use(middleware.RateLimiter(s.config.RateLimit))
	}

	// API v1
	v1 := r.Group("/api/v1")
	{
		// Public routes
		public := v1.Group("")
		{
			// Platform info
			public.GET("/info", handlers.NewPlatformHandler(s.config, s.db).GetInfo)

			// Auth routes
			auth := public.Group("/auth")
			{
				authHandler := handlers.NewAuthHandler(s.config, s.db, s.logger)
				auth.POST("/register", authHandler.Register)
				if s.config.RateLimit.Enabled {
					auth.POST("/login", middleware.RateLimitEndpoint(
						s.config.RateLimit.Login,
					), authHandler.Login)
				} else {
					auth.POST("/login", authHandler.Login)
				}
				auth.POST("/token", authHandler.TokenAuth) // For team token auth
				auth.POST("/refresh", authHandler.RefreshToken)
				auth.POST("/logout", authHandler.Logout)
			}

			// Public challenge listing — optionally enriched with per-user progress
			// when the caller supplies a valid JWT.
			challengesPublic := v1.Group("")
			challengesPublic.Use(middleware.OptionalAuth(s.config, s.db))
			{
				attachmentHandler := handlers.NewAttachmentHandler(s.db, s.storageSvc, s.logger)
				challengeHandler := handlers.NewChallengeHandlerWithAttachments(s.config, s.db, s.containerSvc, s.vmSvc, s.logger, attachmentHandler)
				challengesPublic.GET("/challenges", challengeHandler.List)
				challengesPublic.GET("/challenges/:slug", challengeHandler.Get)
				// File attachment downloads
				challengesPublic.GET("/challenges/:slug/attachments/:attachment_id/download", attachmentHandler.Download)
			}

			// Scoreboard routes accept optional auth so scoreboard_public can keep
			// the same URLs while limiting private events to signed-in players.
			scoreboardHandler := handlers.NewScoreboardHandler(s.config, s.db, s.logger)
			scoreboardRoutes := public.Group("")
			scoreboardRoutes.Use(middleware.OptionalAuth(s.config, s.db))
			scoreboardRoutes.GET("/scoreboard", scoreboardHandler.Get)
			scoreboardRoutes.GET("/scoreboard/history", scoreboardHandler.History)
			scoreboardRoutes.GET("/scoreboard/matrix", scoreboardHandler.Matrix)
			scoreboardRoutes.GET("/profile/:username", scoreboardHandler.Profile)

			// Public stats
			public.GET("/stats", handlers.NewStatsHandler(s.db, s.logger).Get)

			// Arena (Attack-Defense + KotH) read endpoints
			arenaRead := handlers.NewGameHandler(s.config, s.db, s.logger)
			public.GET("/arena/state", arenaRead.State)
			public.GET("/arena/scoreboard", arenaRead.Scoreboard)
			public.GET("/arena/hills", arenaRead.Hills)
			public.GET("/arena/status", arenaRead.Status)
			public.GET("/arena/history", arenaRead.History)
			public.GET("/arena/services", arenaRead.Services)
			public.GET("/arena/events", arenaRead.Events)
		}

		// Protected routes (require authentication)
		protected := v1.Group("")
		protected.Use(middleware.Auth(s.config, s.db))
		{
			// User profile
			user := protected.Group("/user")
			{
				userHandler := handlers.NewUserHandler(s.config, s.db, s.logger)
				user.GET("/me", userHandler.GetProfile)
				user.PUT("/me", userHandler.UpdateProfile)
				user.GET("/me/stats", userHandler.GetStats)
				user.GET("/me/solves", userHandler.GetSolves)
			}

			// Challenge interactions
			challenges := protected.Group("/challenges")
			{
				challengeHandler := handlers.NewChallengeHandler(s.config, s.db, s.containerSvc, s.vmSvc, s.logger)
				challenges.GET("/:slug/flags", challengeHandler.GetFlags)
				challenges.POST("/:slug/submit", middleware.RateLimitEndpoint(
					s.config.RateLimit.FlagSubmission,
				), challengeHandler.SubmitFlag)
				challenges.GET("/:slug/hints", challengeHandler.GetHints)
				challenges.POST("/:slug/hints/:hint_id/unlock", challengeHandler.UnlockHint)
			}

			// Arena (Attack-Defense + KotH) flag submission
			arenaRoutes := protected.Group("/arena")
			{
				arenaHandler := handlers.NewGameHandler(s.config, s.db, s.logger)
				arenaRoutes.POST("/submit", middleware.RateLimitEndpoint(
					s.config.RateLimit.FlagSubmission,
				), arenaHandler.SubmitFlag)
			}

			// Instance management
			instances := protected.Group("/instances")
			{
				instanceHandler := handlers.NewInstanceHandler(s.config, s.db, s.containerSvc, s.vmSvc, s.logger)
				instances.GET("", instanceHandler.List)
				instances.POST("", middleware.RateLimitEndpoint(
					s.config.RateLimit.InstanceStart,
				), instanceHandler.Create)
				instances.GET("/:id", instanceHandler.Get)
				instances.POST("/:id/extend", instanceHandler.Extend)
				instances.POST("/:id/revert", middleware.RateLimitEndpoint(
					s.config.RateLimit.InstanceStart,
				), instanceHandler.Revert)
				instances.POST("/:id/stop", instanceHandler.Stop)
				instances.DELETE("/:id", instanceHandler.Delete)
			}

			// VPN management
			vpnRoutes := protected.Group("/vpn")
			{
				vpnHandler := handlers.NewVPNHandler(s.config, s.db, s.vpnSvc, s.logger)
				vpnRoutes.GET("/config", vpnHandler.GetConfig)
				vpnRoutes.POST("/config", middleware.RateLimitEndpoint(
					s.config.RateLimit.VPNConfigGen,
				), vpnHandler.GenerateConfig)
				vpnRoutes.POST("/config/regenerate", middleware.RateLimitEndpoint(
					s.config.RateLimit.VPNConfigGen,
				), vpnHandler.RegenerateConfig)
				vpnRoutes.GET("/status", vpnHandler.GetStatus)
			}

			// File uploads (chunked upload support for large files)
			uploads := protected.Group("/uploads")
			{
				uploadHandler := handlers.NewUploadHandler(s.uploadSvc, s.logger)
				uploads.GET("", uploadHandler.ListUserUploads)
				uploads.POST("", uploadHandler.InitUpload)
				uploads.POST("/simple", uploadHandler.SimpleUpload)
				uploads.GET("/types", uploadHandler.GetSupportedTypes)
				uploads.GET("/:id", uploadHandler.GetUploadStatus)
				uploads.GET("/:id/progress", uploadHandler.GetUploadProgress)
				uploads.GET("/:id/missing", uploadHandler.GetMissingChunks)
				uploads.PUT("/:id/chunks/:number", uploadHandler.UploadChunk)
				uploads.POST("/:id/complete", uploadHandler.CompleteUpload)
				uploads.DELETE("/:id", uploadHandler.CancelUpload)
			}

			// VM management (only if VM service is available)
			if s.vmSvc != nil {
				vms := protected.Group("/vms")
				{
					vmHandler := handlers.NewVMHandler(s.vmSvc, s.logger)
					vms.GET("", vmHandler.ListUserVMs)
					vms.POST("", vmHandler.CreateVM)
					vms.GET("/templates", vmHandler.ListTemplates)
					vms.GET("/templates/:id", vmHandler.GetTemplate)
					vms.GET("/:id", vmHandler.GetVM)
					vms.POST("/:id/start", vmHandler.StartVM)
					vms.POST("/:id/stop", vmHandler.StopVM)
					vms.POST("/:id/reset", vmHandler.ResetVM)
					vms.POST("/:id/extend", vmHandler.ExtendVM)
					vms.DELETE("/:id", vmHandler.DestroyVM)
				}
			}
		}

		// Admin routes
		admin := v1.Group("/admin")
		admin.Use(middleware.Auth(s.config, s.db))
		admin.Use(middleware.RequireRole("admin"))
		{
			// User management
			users := admin.Group("/users")
			{
				adminUserHandler := handlers.NewAdminUserHandler(s.config, s.db, s.logger)
				users.GET("", adminUserHandler.List)
				users.GET("/:id", adminUserHandler.Get)
				users.PUT("/:id", adminUserHandler.Update)
				users.POST("/:id/ban", adminUserHandler.Ban)
				users.POST("/:id/unban", adminUserHandler.Unban)
				users.DELETE("/:id", adminUserHandler.Delete)
			}

			// Arena (Attack-Defense + KotH) setup
			gameAdmin := admin.Group("/arena")
			{
				gameAdminHandler := handlers.NewGameAdminHandler(s.config, s.db, s.logger)
				gameAdmin.GET("/teams", gameAdminHandler.ListTeams)
				gameAdmin.POST("/teams", gameAdminHandler.CreateTeam)
				gameAdmin.DELETE("/teams/:id", gameAdminHandler.DeleteTeam)
				gameAdmin.POST("/teams/:id/members", gameAdminHandler.AddMember)

				gameAdmin.GET("/services", gameAdminHandler.ListServices)
				gameAdmin.POST("/services", gameAdminHandler.CreateService)
				gameAdmin.PATCH("/services/:id", gameAdminHandler.UpdateService)
				gameAdmin.DELETE("/services/:id", gameAdminHandler.DeleteService)

				gameAdmin.GET("/hills", gameAdminHandler.ListHills)
				gameAdmin.POST("/hills", gameAdminHandler.CreateHill)
				gameAdmin.PATCH("/hills/:id", gameAdminHandler.UpdateHill)
				gameAdmin.DELETE("/hills/:id", gameAdminHandler.DeleteHill)
			}

			// Challenge management
			challenges := admin.Group("/challenges")
			{
				adminChallengeHandler := handlers.NewAdminChallengeHandler(s.config, s.db, s.containerSvc, s.logger)
				challenges.GET("", adminChallengeHandler.List)
				challenges.POST("", adminChallengeHandler.Create)
				challenges.POST("/ova", adminChallengeHandler.CreateOVAChallenge)
				challenges.GET("/:id", adminChallengeHandler.Get)
				challenges.PUT("/:id", adminChallengeHandler.Update)
				challenges.DELETE("/:id", adminChallengeHandler.Delete)
				challenges.POST("/:id/publish", adminChallengeHandler.Publish)
				challenges.POST("/:id/unpublish", adminChallengeHandler.Unpublish)
				challenges.POST("/:id/archive", adminChallengeHandler.Archive)

				// Flag management
				challenges.GET("/:id/flags", adminChallengeHandler.ListFlags)
				challenges.POST("/:id/flags", adminChallengeHandler.CreateFlag)
				challenges.PUT("/:id/flags/:flag_id", adminChallengeHandler.UpdateFlag)
				challenges.DELETE("/:id/flags/:flag_id", adminChallengeHandler.DeleteFlag)

				// Hint management
				challenges.GET("/:id/hints", adminChallengeHandler.ListHints)
				challenges.POST("/:id/hints", adminChallengeHandler.CreateHint)
				challenges.PUT("/:id/hints/:hint_id", adminChallengeHandler.UpdateHint)
				challenges.DELETE("/:id/hints/:hint_id", adminChallengeHandler.DeleteHint)

				// Attachment management (admin: upload/list/delete; download is public)
				adminAttachmentHandler := handlers.NewAttachmentHandler(s.db, s.storageSvc, s.logger)
				challenges.GET("/:id/attachments", adminAttachmentHandler.List)
				challenges.POST("/:id/attachments", adminAttachmentHandler.Upload)
				challenges.DELETE("/:id/attachments/:attachment_id", adminAttachmentHandler.Delete)
			}

			// Category management
			categories := admin.Group("/categories")
			{
				categoryHandler := handlers.NewCategoryHandler(s.config, s.db, s.logger)
				categories.GET("", categoryHandler.List)
				categories.POST("", categoryHandler.Create)
				categories.PUT("/:id", categoryHandler.Update)
				categories.DELETE("/:id", categoryHandler.Delete)
			}

			// Dynamic flag monitoring (admin only)
			adminChalMonitor := handlers.NewAdminChallengeHandler(s.config, s.db, s.containerSvc, s.logger)
			admin.GET("/instance-flags", adminChalMonitor.ListInstanceFlags)
			admin.GET("/flag-shares", adminChalMonitor.ListFlagShareEvents)

			// Instance management (admin view)
			instances := admin.Group("/instances")
			{
				adminInstanceHandler := handlers.NewAdminInstanceHandler(s.config, s.db, s.containerSvc, s.vmSvc, s.logger)
				instances.GET("", adminInstanceHandler.List)
				instances.GET("/stats", adminInstanceHandler.Stats)
				instances.POST("/cleanup", adminInstanceHandler.Cleanup)
				instances.POST("/:id/stop", adminInstanceHandler.ForceStop)
				instances.DELETE("/:id", adminInstanceHandler.ForceDelete)
			}

			// Team tokens & invite codes
			tokens := admin.Group("/tokens")
			{
				tokenHandler := handlers.NewTokenHandler(s.config, s.db, s.logger)
				tokens.GET("/team", tokenHandler.ListTeamTokens)
				tokens.POST("/team", tokenHandler.CreateTeamToken)
				tokens.DELETE("/team/:id", tokenHandler.DeleteTeamToken)

				tokens.GET("/invite", tokenHandler.ListInviteCodes)
				tokens.POST("/invite", tokenHandler.CreateInviteCode)
				tokens.DELETE("/invite/:id", tokenHandler.DeleteInviteCode)
			}

			// Platform settings
			settings := admin.Group("/settings")
			{
				settingsHandler := handlers.NewSettingsHandler(s.config, s.db, s.logger)
				settings.GET("", settingsHandler.List)
				settings.PUT("", settingsHandler.Update)
			}

			// Audit log
			admin.GET("/audit", handlers.NewAuditHandler(s.db, s.logger).List)

			// Statistics
			admin.GET("/stats", handlers.NewStatsHandler(s.db, s.logger).Get)

			// VM Template management (admin)
			vmTemplates := admin.Group("/vm-templates")
			{
				templateHandler := handlers.NewVMTemplateHandler(s.config, s.db, s.logger)
				vmTemplates.GET("", templateHandler.List)
				vmTemplates.POST("/upload", templateHandler.Upload)
				vmTemplates.POST("/register", templateHandler.Register)
				vmTemplates.GET("/upload/:id/status", templateHandler.GetUploadStatus)
				vmTemplates.GET("/:id", templateHandler.Get)
				vmTemplates.PUT("/:id", templateHandler.Update)
				vmTemplates.DELETE("/:id", templateHandler.Delete)
			}

			// VM Node management (admin)
			nodes := admin.Group("/nodes")
			{
				nodeHandler := handlers.NewNodeHandler(s.config, s.db, s.logger)
				nodes.GET("", nodeHandler.List)
				nodes.POST("", nodeHandler.Create)
				nodes.GET("/:id", nodeHandler.Get)
				nodes.PUT("/:id", nodeHandler.Update)
				nodes.DELETE("/:id", nodeHandler.Delete)
			}

			// Infrastructure stats
			infrastructure := admin.Group("/infrastructure")
			{
				nodeHandler := handlers.NewNodeHandler(s.config, s.db, s.logger)
				infrastructure.GET("/stats", nodeHandler.GetInfrastructureStats)

				templateHandler := handlers.NewVMTemplateHandler(s.config, s.db, s.logger)
				infrastructure.GET("/instances", templateHandler.ListActiveInstances)
				infrastructure.GET("/docker-instances", templateHandler.ListActiveDockerInstances)
			}
		}
	}

	s.router = r
}

func (s *Server) healthCheck(c *gin.Context) {
	// Check database connection
	ctx := c.Request.Context()
	err := s.db.Pool.Ping(ctx)
	containerStatus := s.containerSvc.Status()
	vpnStatus := s.vpnSvc.Status()

	status := "healthy"
	httpStatus := http.StatusOK
	dbStatus := "connected"
	if err != nil || containerStatus != "connected" {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}
	if err != nil {
		dbStatus = "disconnected"
	}

	c.JSON(httpStatus, gin.H{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"services": gin.H{
			"database":  dbStatus,
			"container": containerStatus,
			"vpn":       vpnStatus,
		},
	})
}
