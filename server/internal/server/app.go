// Package server hosts the application coordinator: the single Run entry
// point that wires config, database, the DHCP engine, the HA cluster
// manager, and the HTTP API together and runs them until shutdown.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/api"
	"github.com/shamalawy/gdhcp/server/internal/config"
	"github.com/shamalawy/gdhcp/server/internal/db"
	"github.com/shamalawy/gdhcp/server/internal/dhcp"
	"github.com/shamalawy/gdhcp/server/internal/ha"
	"github.com/shamalawy/gdhcp/server/internal/logger"
	"github.com/shamalawy/gdhcp/server/internal/metrics"
	"github.com/shamalawy/gdhcp/server/internal/store"
	"github.com/shamalawy/gdhcp/server/internal/version"
	"github.com/shamalawy/gdhcp/web"

	"github.com/jackc/pgx/v5/pgxpool"
)

// statsFlushInterval/utilizationSampleInterval match the cadences documented
// by the Store unit's notes_for_integration and docs/CONTRACTS.md Part 1:
// StatsRecorder.Record* only buffers in memory (kept off the DHCP hot path),
// so FlushStats must run periodically for it to reach stats_dhcp_transactions;
// SampleUtilization reconciles stats_active_lease_count and populates the
// utilization-history tables GetUtilizationChart reads from.
const (
	statsFlushInterval        = 10 * time.Second
	utilizationSampleInterval = 60 * time.Second

	// dbPoolStatsInterval is how often db.StartPoolStatsReporter samples
	// pgxpool.Stat() into the gdhcp_db_pool_* gauges. Pool saturation is a
	// slow-moving capacity signal, not a hot metric, so this matches the
	// reporter's own documented default rather than the faster stats cadence
	// above.
	dbPoolStatsInterval = 15 * time.Second

	// leasePurgeInterval is how often retention deletes lease history past
	// database.lease_retention_days. Retention is measured in days, so
	// sweeping more often than this buys nothing and only adds write load;
	// the first sweep lands one interval after startup.
	leasePurgeInterval = 6 * time.Hour

	// shutdownTimeout bounds how long graceful shutdown waits for the HTTP
	// server to drain in-flight requests (including long-lived SSE streams,
	// amendment A12) and for the HA manager's background loops to exit.
	shutdownTimeout = 15 * time.Second
)

// Run starts gdhcp and blocks until ctx is canceled or a fatal error occurs.
// Callers (cmd/gdhcp's `serve` command) are expected to derive ctx from
// signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
// so Run can shut down gracefully on SIGTERM.
//
// Wiring order: logger -> database pool (retry/backoff) -> store.Postgres ->
// metrics registry/server (if enabled) -> ha.Manager (always -- amendment
// A11's standalone mode still self-registers and heartbeats) -> dhcp.Engine
// (gated by the HA manager's ShouldServe, skipped entirely when no DHCP
// interfaces are configured) -> the REST API, mounted alongside the
// metrics endpoint (if same-port) and the embedded Angular SPA (with SPA
// fallback) behind one http.Server. Migrations are not run here: `gdhcp
// serve --auto-migrate` (the default) already ran them before calling Run
// (server/internal/cli/serve.go).
func Run(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("server: run: nil config")
	}

	log, err := logger.New(cfg.Logging)
	if err != nil {
		return fmt.Errorf("server: build logger: %w", err)
	}
	ver := version.Get()
	log.Info("gdhcp: starting", "version", ver.String(), "api_addr", cfg.Server.APIAddr, "ha_enabled", cfg.HA.Enabled)

	pool, err := openPoolWithRetry(ctx, cfg.Database, log)
	if err != nil {
		return fmt.Errorf("server: open database: %w", err)
	}
	log.Info("server: database connected")

	st := store.NewPostgres(pool, store.WithOptionCacheTTL(cfg.Database.OptionCacheTTL))

	// -- Metrics -------------------------------------------------------
	var reg *metrics.Registry
	var sameportMetrics http.Handler
	var metricsServer *http.Server
	if cfg.Metrics.Enabled {
		reg = metrics.New()
		reg.SetBuildInfo(ver.Version, ver.Commit, ver.BuildDate)

		// The gdhcp_db_pool_* gauges are the only metrics nothing on a
		// request path writes -- they have to be polled off pgxpool.Stat().
		// Without this goroutine the registry exports them as a constant 0,
		// which reads on a capacity dashboard as "the pool is idle" rather
		// than "nobody is sampling it".
		go db.StartPoolStatsReporter(ctx, pool, reg, dbPoolStatsInterval)

		if cfg.Metrics.ListenAddr == cfg.Server.APIAddr {
			sameportMetrics = metricsHandler(cfg.Metrics.Path, reg)
		} else {
			metricsServer = &http.Server{
				Addr:              cfg.Metrics.ListenAddr,
				Handler:           metricsHandler(cfg.Metrics.Path, reg),
				ReadHeaderTimeout: 10 * time.Second,
			}
			go func() {
				log.Info("server: metrics listening", "addr", cfg.Metrics.ListenAddr, "path", cfg.Metrics.Path)
				if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error("server: metrics server failed", "error", err)
				}
			}()
		}
	}

	// -- HA cluster manager (always constructed: amendment A11 standalone
	// mode still self-registers this node and writes heartbeats). --------
	haManager, err := ha.New(cfg, st, log)
	if err != nil {
		pool.Close()
		return fmt.Errorf("server: build ha manager: %w", err)
	}
	haManager.SetMetrics(reg)
	if err := haManager.Start(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("server: start ha manager: %w", err)
	}
	log.Info("server: ha manager started",
		"cluster_id", haManager.ClusterID(), "server_id", haManager.LocalServerID())

	// The HA config watcher's notification channel is this node's only
	// signal that a peer server changed shared config (options, templates,
	// etc.) -- notes_for_integration: wire it to invalidate the option
	// cache so a secondary's cache clears promptly instead of only on its
	// own local writes.
	if inv, ok := st.(optionCacheInvalidator); ok {
		go watchOptionCacheInvalidation(ctx, haManager.Subscribe(), inv)
	}

	// Periodic stats persistence (StatsFlusher, off the DHCP hot path) and
	// utilization-history sampling -- notes_for_integration cadences.
	if sf, ok := st.(store.StatsFlusher); ok {
		go runPeriodic(ctx, statsFlushInterval, func(c context.Context) {
			if err := sf.FlushStats(c); err != nil {
				log.Error("server: flush stats", "error", err)
			}
		})
	}
	go runPeriodic(ctx, utilizationSampleInterval, func(c context.Context) {
		if err := st.SampleUtilization(c); err != nil {
			log.Error("server: sample utilization", "error", err)
		}
	})

	// Lease-history retention. The DHCP engine's own reaper only rewrites
	// binding_state to 'expired'; nothing deleted those rows, so
	// database.lease_retention_days was configured, defaulted and validated
	// while having no effect at all. A high-churn network rewrites the same
	// addresses continuously, so this is the difference between a bounded
	// dhcp_leases table and one that only ever grows. 0 disables it.
	if cfg.Database.LeaseRetentionDays > 0 {
		retention := time.Duration(cfg.Database.LeaseRetentionDays) * 24 * time.Hour
		go runPeriodic(ctx, leasePurgeInterval, func(c context.Context) {
			n, err := st.PurgeLeasesBefore(c, time.Now().Add(-retention))
			if err != nil {
				log.Error("server: purge lease history", "error", err)
				return
			}
			if n > 0 {
				log.Info("server: purged lease history past retention",
					"count", n, "retention_days", cfg.Database.LeaseRetentionDays)
			}
		})
	} else {
		log.Info("server: lease history retention disabled", "reason", "lease_retention_days = 0")
	}

	// -- DHCP engine (skipped entirely in API/UI-only mode). --------------
	var eng *dhcp.Engine
	var engineInfo api.EngineInfo
	if len(cfg.DHCP.Interfaces) > 0 {
		eng, err = dhcp.New(cfg, st, haManager, log)
		if err != nil {
			_ = haManager.Stop(context.Background())
			pool.Close()
			return fmt.Errorf("server: build dhcp engine: %w", err)
		}
		eng.UseMetrics(reg)

		adapter := newEngineInfoAdapter(len(cfg.DHCP.Interfaces))
		if err := eng.Start(ctx); err != nil {
			_ = haManager.Stop(context.Background())
			pool.Close()
			return fmt.Errorf("server: start dhcp engine: %w", err)
		}
		adapter.markStarted()
		engineInfo = adapter
		log.Info("server: dhcp engine started", "interfaces", len(cfg.DHCP.Interfaces))
	} else {
		log.Info("server: no dhcp interfaces configured, running API/UI-only (amendment A11)")
	}

	// -- HTTP: REST API + metrics (if same-port) + embedded SPA. ----------
	assets, err := web.Assets()
	if err != nil {
		stopEngineAndHA(eng, haManager, log)
		pool.Close()
		return fmt.Errorf("server: load web assets: %w", err)
	}

	apiHandler := api.New(cfg, st, newHAStatusAdapter(haManager), engineInfo, log)

	httpServer := &http.Server{
		Addr: cfg.Server.APIAddr,
		Handler: securityHeaders(&rootHandler{
			api:         apiHandler,
			assets:      spaFileServer(assets),
			metricsPath: cfg.Metrics.Path,
			metrics:     sameportMetrics,
		}, cfg.Server.APITLSEnabled),
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErrCh := make(chan error, 1)
	go func() {
		log.Info("server: http listening", "addr", cfg.Server.APIAddr, "tls", cfg.Server.APITLSEnabled)
		var serveErr error
		if cfg.Server.APITLSEnabled {
			serveErr = httpServer.ListenAndServeTLS(cfg.Server.APITLSCert, cfg.Server.APITLSKey)
		} else {
			serveErr = httpServer.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serveErrCh <- serveErr
			return
		}
		serveErrCh <- nil
	}()

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("server: shutdown signal received")
	case serveErr := <-serveErrCh:
		if serveErr != nil {
			log.Error("server: http server failed", "error", serveErr)
			runErr = fmt.Errorf("server: http server: %w", serveErr)
		}
	}

	// -- Graceful shutdown, in the mandated order: http, engine, ha (drops
	// the VIP), pool. Each phase gets its own independent shutdownTimeout
	// budget instead of sharing one context across all of them: amendment
	// A12's long-lived SSE streams can legitimately make httpServer.Shutdown
	// consume its entire budget draining, and a shared context left no time
	// for metricsServer.Shutdown or haManager.Stop to do their own work --
	// haManager.Stop in particular releases the VIP, so entering it with an
	// already-expired context made it return immediately without waiting for
	// that release, risking a dual-VIP condition on a node that was PRIMARY
	// at shutdown time. ------------------------------------------------------
	httpShutdownCtx, httpCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	if err := httpServer.Shutdown(httpShutdownCtx); err != nil {
		log.Error("server: http shutdown", "error", err)
	}
	httpCancel()

	if metricsServer != nil {
		metricsShutdownCtx, metricsCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := metricsServer.Shutdown(metricsShutdownCtx); err != nil {
			log.Error("server: metrics server shutdown", "error", err)
		}
		metricsCancel()
	}
	if eng != nil {
		if err := eng.Stop(); err != nil {
			log.Error("server: dhcp engine stop", "error", err)
		}
	}
	haShutdownCtx, haCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	if err := haManager.Stop(haShutdownCtx); err != nil {
		log.Error("server: ha manager stop", "error", err)
	}
	haCancel()

	pool.Close()
	log.Info("server: shutdown complete")

	return runErr
}

// stopEngineAndHA is the best-effort teardown used when startup fails after
// the engine and/or HA manager are already running, so a later error path
// doesn't leak listener sockets or leave this node's HA state dangling.
func stopEngineAndHA(eng *dhcp.Engine, haManager *ha.Manager, log *slog.Logger) {
	if eng != nil {
		if err := eng.Stop(); err != nil {
			log.Error("server: dhcp engine stop", "error", err)
		}
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := haManager.Stop(stopCtx); err != nil {
		log.Error("server: ha manager stop", "error", err)
	}
}

// openPoolWithRetry opens the PostgreSQL connection pool, retrying with
// exponential backoff (capped at 30s) until it succeeds or ctx is canceled.
// A fresh deployment's database can plausibly still be starting up (e.g. a
// container orchestrator racing gdhcp against postgres), so failing fast
// here would make routine startup ordering flaky.
func openPoolWithRetry(ctx context.Context, cfg config.DatabaseConfig, log *slog.Logger) (*pgxpool.Pool, error) {
	const maxBackoff = 30 * time.Second
	backoff := time.Second

	for {
		pool, err := db.Open(ctx, cfg)
		if err == nil {
			return pool, nil
		}

		log.Warn("server: database connection failed, retrying", "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("giving up after %v: %w", backoff, ctx.Err())
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// watchOptionCacheInvalidation invalidates inv whenever ch signals a
// cluster config change, until ctx is canceled or ch closes.
func watchOptionCacheInvalidation(ctx context.Context, ch <-chan struct{}, inv optionCacheInvalidator) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			inv.InvalidateOptionCache()
		}
	}
}

// runPeriodic calls fn every interval until ctx is canceled.
func runPeriodic(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}
