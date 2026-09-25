package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anvil-lab/anvil/internal/api"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/anvil-lab/anvil/internal/services/instancer"
	"github.com/anvil-lab/anvil/internal/services/storage"
	"github.com/anvil-lab/anvil/internal/services/upload"
	"github.com/anvil-lab/anvil/internal/services/vm"
	"github.com/anvil-lab/anvil/internal/services/vpn"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewProduction()
	if os.Getenv("ANVIL_ENV") == "development" {
		logger, _ = zap.NewDevelopment()
	}
	defer logger.Sync()

	sugar := logger.Sugar()
	sugar.Info("Starting Anvil Platform...")

	cfg, err := config.Load()
	if err != nil {
		sugar.Fatalf("Failed to load configuration: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		sugar.Fatalf("Invalid configuration: %v", err)
	}

	sugar.Infof("Loaded configuration for environment: %s", cfg.Environment)

	db, err := database.New(cfg.Database)
	if err != nil {
		sugar.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	sugar.Info("Connected to database")

	if err := db.Migrate(); err != nil {
		sugar.Fatalf("Failed to run migrations: %v", err)
	}

	sugar.Info("Database migrations completed")

	containerSvc, err := container.NewService(cfg.Container, logger)
	if err != nil {
		sugar.Fatalf("Failed to initialize container service: %v", err)
	}

	instancerSvc, err := instancer.NewService(cfg.Instancer, logger)
	if err != nil {
		sugar.Fatalf("Failed to initialize instancer service: %v", err)
	}

	vpnSvc, err := vpn.NewService(cfg.VPN, db, logger)
	if err != nil {
		sugar.Fatalf("Failed to initialize VPN service: %v", err)
	}

	storageSvc, err := storage.NewLocalStorage(cfg.Storage.Path, logger)
	if err != nil {
		sugar.Fatalf("Failed to initialize storage service: %v", err)
	}

	uploadSvc := upload.NewService(storageSvc, logger, upload.DefaultConfig())

	// vm service is optional; it may fail when libvirt is unavailable
	vmSvc, err := vm.NewService(logger, vm.DefaultConfig(), db)
	if err != nil {
		sugar.Warnf("VM service not available (this is OK for Docker-only mode): %v", err)
		vmSvc = nil
	}

	if vmSvc != nil {
		nodeIP := os.Getenv("VM_NODE_IP")
		if nodeIP == "" {
			nodeIP = "172.17.0.1" // docker host default
		}
		sshUser := os.Getenv("VM_NODE_SSH_USER")
		if sshUser == "" {
			sshUser = "root"
		}
		sshKeyPath := os.Getenv("VM_NODE_SSH_KEY")
		if sshKeyPath == "" {
			sshKeyPath = "/root/.ssh/id_rsa"
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := vmSvc.ReconcileState(ctx, nodeIP, nodeIP, sshUser, sshKeyPath); err != nil {
			sugar.Warnf("Failed to reconcile VM state: %v", err)
		} else {
			sugar.Info("VM state reconciliation completed")
		}
		cancel()
	}

	// container instance expiry: every minute, stop/remove docker containers
	// whose timer has expired.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

			type expiredInst struct {
				ID          string
				ContainerID string
				UserID      string
				ChallengeID string
			}
			rows, err := db.Pool.Query(ctx, `
				SELECT i.id, i.container_id, i.user_id::text, i.challenge_id
				FROM instances i
				JOIN challenges c ON i.challenge_id = c.id
				WHERE i.expires_at < NOW()
				  AND i.status IN ('running', 'creating', 'pending')
				  AND c.resource_type = 'docker'
				  AND i.container_id IS NOT NULL AND i.container_id <> ''
			`)
			if err != nil {
				sugar.Errorf("Container expiry query failed: %v", err)
				cancel()
				continue
			}

			var expired []expiredInst
			for rows.Next() {
				var inst expiredInst
				if scanErr := rows.Scan(&inst.ID, &inst.ContainerID, &inst.UserID, &inst.ChallengeID); scanErr == nil {
					expired = append(expired, inst)
				}
			}
			rows.Close()

			for _, inst := range expired {
				shortID := inst.ContainerID
				if len(shortID) > 12 {
					shortID = shortID[:12]
				}
				sugar.Infof("Expiring container instance %s (container %s)", inst.ID, shortID)

				if stopErr := containerSvc.StopInstance(ctx, inst.ContainerID); stopErr != nil {
					sugar.Warnf("Failed to stop expired container %s: %v", shortID, stopErr)
					// mark as expired in the db anyway so it doesn't stay 'running'
				}

				var cooldownMinutes int
				if scanErr := db.Pool.QueryRow(ctx,
					`SELECT COALESCE(cooldown_minutes, 15) FROM challenges WHERE id = $1`,
					inst.ChallengeID).Scan(&cooldownMinutes); scanErr != nil {
					cooldownMinutes = 15
				}
				cooldownUntil := time.Now().Add(time.Duration(cooldownMinutes) * time.Minute)
				db.Pool.Exec(ctx,
					`INSERT INTO user_cooldowns (id, user_id, challenge_id, cooldown_until, created_at)
					 VALUES (uuid_generate_v4(), $1::uuid, $2, $3, NOW())
					 ON CONFLICT (user_id, challenge_id) DO UPDATE SET cooldown_until = $3`,
					inst.UserID, inst.ChallengeID, cooldownUntil)

				db.Pool.Exec(ctx,
					`UPDATE instances SET status = 'expired', updated_at = NOW() WHERE id = $1`,
					inst.ID)
			}

			// orphaned container cleanup: docker containers that are running but
			// have no matching active db record get stopped/removed.
			if containerSvc != nil {
				allContainers, listErr := containerSvc.ListInstances(ctx)
				if listErr != nil {
					sugar.Warnf("Failed to list Docker containers for orphan cleanup: %v", listErr)
				} else if len(allContainers) > 0 {
					activeRows, activeErr := db.Pool.Query(ctx, `
						SELECT container_id FROM instances
						WHERE status IN ('running', 'creating', 'pending')
						  AND container_id IS NOT NULL AND container_id <> ''
					`)
					if activeErr == nil {
						activeIDs := make(map[string]bool)
						for activeRows.Next() {
							var cid string
							if scanErr := activeRows.Scan(&cid); scanErr == nil {
								activeIDs[cid] = true
							}
						}
						activeRows.Close()

						for _, ctr := range allContainers {
							if !activeIDs[ctr.ID] {
								shortCID := ctr.ID
								if len(shortCID) > 12 {
									shortCID = shortCID[:12]
								}
								sugar.Infof("Removing orphaned container %s (names: %v)", shortCID, ctr.Names)
								if stopErr := containerSvc.StopInstance(ctx, ctr.ID); stopErr != nil {
									sugar.Warnf("Failed to remove orphaned container %s: %v", shortCID, stopErr)
								}
							}
						}
					}
				}
			}

			cancel()
		}
	}()

	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

				var expiringCount int
				db.Pool.QueryRow(ctx, `
					SELECT COUNT(*) FROM instances i  
					JOIN challenges c ON i.challenge_id = c.id
					WHERE i.expires_at < NOW() 
					  AND i.status IN ('running', 'pending', 'creating')
					  AND c.resource_type = 'vm'
				`).Scan(&expiringCount)

				// vm only; container expiry is handled by the 1-minute goroutine above,
				// which stops docker containers first.
				if _, err := db.Pool.Exec(ctx, `
					UPDATE instances
					SET status = 'expired', updated_at = NOW()
					WHERE expires_at < NOW() AND status IN ('running', 'pending', 'creating')
					  AND challenge_id IN (
					    SELECT id FROM challenges WHERE resource_type = 'vm'
					  )
				`); err != nil {
					sugar.Errorf("Failed to mark expired instances: %v", err)
				} else if expiringCount > 0 {
					db.Pool.Exec(ctx, `
						UPDATE vm_nodes 
						SET active_vms = GREATEST(0, active_vms - $1),
							used_vcpu = GREATEST(0, used_vcpu - ($1 * 1)),
							used_memory_mb = GREATEST(0, used_memory_mb - ($1 * 1024)),
							updated_at = NOW()
						WHERE name = 'core'
					`, expiringCount)
				}

				var deletingCount int
				db.Pool.QueryRow(ctx, `
					SELECT COUNT(*) FROM instances i
					JOIN challenges c ON i.challenge_id = c.id 
					WHERE i.status IN ('failed', 'stopped', 'expired') 
					  AND i.created_at < NOW() - INTERVAL '1 hour'
					  AND c.resource_type = 'vm'
				`).Scan(&deletingCount)

				// vm rows only: container rows carry the dynamic flags, which must
				// stay submittable after the instance is gone.
				if _, err := db.Pool.Exec(ctx, `
					DELETE FROM instances i
					USING challenges c
					WHERE i.challenge_id = c.id
					  AND c.resource_type = 'vm'
					  AND i.status IN ('failed', 'stopped', 'expired')
					  AND i.created_at < NOW() - INTERVAL '1 hour'
					  AND NOT EXISTS (SELECT 1 FROM submissions s WHERE s.instance_id = i.id)
				`); err != nil {
					sugar.Errorf("Failed to cleanup old instances: %v", err)
				}

				// safety net: force node counters to match reality.
				// 1 vcpu / 1024 mb per vm (current template defaults).
				db.Pool.Exec(ctx, `
					UPDATE vm_nodes n
					SET active_vms = (
							SELECT COUNT(*) FROM instances i  
							JOIN challenges c ON i.challenge_id = c.id
							WHERE i.status IN ('running', 'creating', 'pending')
							  AND c.resource_type = 'vm'
						),
						used_vcpu = (
							SELECT COUNT(*) FROM instances i 
							JOIN challenges c ON i.challenge_id = c.id
							WHERE i.status IN ('running', 'creating', 'pending')
							  AND c.resource_type = 'vm'
						),
						used_memory_mb = (
							SELECT COUNT(*) * 1024 FROM instances i 
							JOIN challenges c ON i.challenge_id = c.id
							WHERE i.status IN ('running', 'creating', 'pending')
							  AND c.resource_type = 'vm'
						),
						updated_at = NOW()
					WHERE n.name = 'core'
				`)

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

					// after cleanup, resync node counters from db state; joins challenges to count vms only
					db.Pool.Exec(ctx, `
					UPDATE vm_nodes n
					SET active_vms = (
							SELECT COUNT(*) FROM instances i
							JOIN challenges c ON i.challenge_id = c.id 
							WHERE i.status IN ('running', 'creating', 'pending')
							  AND c.resource_type = 'vm'
						),
						used_vcpu = (
							SELECT COALESCE(SUM(2), 0) FROM instances i
							JOIN challenges c ON i.challenge_id = c.id 
							WHERE i.status IN ('running', 'creating', 'pending')
							  AND c.resource_type = 'vm'
						),
						used_memory_mb = (
							SELECT COALESCE(SUM(2048), 0) FROM instances i
							JOIN challenges c ON i.challenge_id = c.id 
							WHERE i.status IN ('running', 'creating', 'pending')
							  AND c.resource_type = 'vm'
							),
							updated_at = NOW()
						WHERE n.name = 'core'
					`)

					cancel()
				}
			}
		}()
	}

	// game engine (attack-defense + koth) — no-op unless game.enabled.
	gameCtx, gameCancel := context.WithCancel(context.Background())
	go game.NewController(cfg.Game, db, logger).Run(gameCtx)

	server := api.NewServer(cfg, db, containerSvc, instancerSvc, vmSvc, uploadSvc, storageSvc, vpnSvc, logger)

	// extended timeouts for large file uploads
	httpServer := &http.Server{
		Addr:              net.JoinHostPort(cfg.Server.Host, fmt.Sprintf("%d", cfg.Server.Port)),
		Handler:           server.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		sugar.Infof("Server listening on port %d", cfg.Server.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			sugar.Fatalf("Failed to start server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	sugar.Info("Shutting down server...")

	gameCancel()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := containerSvc.Cleanup(ctx); err != nil {
		sugar.Errorf("Error during container cleanup: %v", err)
	}

	if err := httpServer.Shutdown(ctx); err != nil {
		sugar.Fatalf("Server forced to shutdown: %v", err)
	}

	sugar.Info("Server exited properly")
}
