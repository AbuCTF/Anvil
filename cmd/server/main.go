package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anvil-lab/anvil/internal/api"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/anvil-lab/anvil/internal/services/storage"
	"github.com/anvil-lab/anvil/internal/services/upload"
	"github.com/anvil-lab/anvil/internal/services/vm"
	"github.com/anvil-lab/anvil/internal/services/vpn"
	"go.uber.org/zap"
)

func main() {
	// Initialize logger
	logger, _ := zap.NewProduction()
	if os.Getenv("ANVIL_ENV") == "development" {
		logger, _ = zap.NewDevelopment()
	}
	defer logger.Sync()

	sugar := logger.Sugar()
	sugar.Info("Starting Anvil Platform...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		sugar.Fatalf("Failed to load configuration: %v", err)
	}

	sugar.Infof("Loaded configuration for environment: %s", cfg.Environment)

	// Initialize database
	db, err := database.New(cfg.Database)
	if err != nil {
		sugar.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	sugar.Info("Connected to database")

	// Run migrations
	if err := db.Migrate(); err != nil {
		sugar.Fatalf("Failed to run migrations: %v", err)
	}

	sugar.Info("Database migrations completed")

	// Initialize services
	containerSvc, err := container.NewService(cfg.Container, logger)
	if err != nil {
		sugar.Fatalf("Failed to initialize container service: %v", err)
	}

	vpnSvc, err := vpn.NewService(cfg.VPN, logger)
	if err != nil {
		sugar.Fatalf("Failed to initialize VPN service: %v", err)
	}

	// Initialize storage service
	storageSvc, err := storage.NewLocalStorage("./data/storage", logger)
	if err != nil {
		sugar.Fatalf("Failed to initialize storage service: %v", err)
	}

	// Initialize upload service
	uploadSvc := upload.NewService(storageSvc, logger, upload.DefaultConfig())

	// Initialize VM service (optional - may fail if libvirt not available)
	vmSvc, err := vm.NewService(logger, vm.DefaultConfig())
	if err != nil {
		sugar.Warnf("VM service not available (this is OK for Docker-only mode): %v", err)
		vmSvc = nil
	}

	// Reconcile VM state on startup (cleanup orphaned VMs)
	if vmSvc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := vmSvc.ReconcileState(ctx); err != nil {
			sugar.Warnf("Failed to reconcile VM state: %v", err)
		} else {
			sugar.Info("VM state reconciliation completed")
		}
		cancel()
	}

	// Start background cleanup goroutines
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				
				// Clean up expired DB instances
				if _, err := db.Pool.Exec(ctx, `
					UPDATE instances 
					SET status = 'expired', updated_at = NOW()
					WHERE expires_at < NOW() AND status IN ('running', 'pending', 'creating')
				`); err != nil {
					sugar.Errorf("Failed to mark expired instances: %v", err)
				}

				// Delete old failed/stopped/expired instances
				if _, err := db.Pool.Exec(ctx, `
					DELETE FROM instances 
					WHERE status IN ('failed', 'stopped', 'expired') 
					  AND created_at < NOW() - INTERVAL '1 hour'
				`); err != nil {
					sugar.Errorf("Failed to cleanup old instances: %v", err)
				}

				cancel()
			}
		}
	}()

	if vmSvc != nil {
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					if err := vmSvc.CleanupExpired(ctx); err != nil {
						sugar.Errorf("VM cleanup failed: %v", err)
					}
					cancel()
				}
			}
		}()
	}

	// Initialize API server
	server := api.NewServer(cfg, db, containerSvc, vmSvc, uploadSvc, vpnSvc, logger)

	// Create HTTP server with extended timeouts for large file uploads
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      server.Router(),
		ReadTimeout:  30 * time.Minute, // Extended for OVA uploads
		WriteTimeout: 30 * time.Minute, // Extended for large responses
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		sugar.Infof("Server listening on port %d", cfg.Server.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			sugar.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	sugar.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Cleanup running containers
	if err := containerSvc.Cleanup(ctx); err != nil {
		sugar.Errorf("Error during container cleanup: %v", err)
	}

	if err := httpServer.Shutdown(ctx); err != nil {
		sugar.Fatalf("Server forced to shutdown: %v", err)
	}

	sugar.Info("Server exited properly")
}
