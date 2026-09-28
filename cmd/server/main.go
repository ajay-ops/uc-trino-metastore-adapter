// Command server runs the Unity Catalog adapter infrastructure.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/config"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/observability"
	hmsserver "github.com/ajay-ops/uc-trino-metastore-adapter/internal/thrift"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/unity"
)

func main() {
	logger := observability.NewLogger(os.Stdout, slog.LevelInfo)
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	logger = observability.NewLogger(os.Stdout, cfg.LogLevel)
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = run(ctx, cfg, logger); err != nil {
		logger.Error("service failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	metrics := observability.NewMetrics()
	uc, err := unity.New(cfg, metrics)
	if err != nil {
		return err
	}
	defer uc.Close()
	thrift, err := hmsserver.New(cfg, logger, metrics, uc)
	if err != nil {
		return fmt.Errorf("bind Thrift listener: %w", err)
	}
	// The client foundation is initialized and credentials are locally validated.
	// The five read-only RPCs use UC metadata; Trino owns Delta schema resolution.
	health := &observability.Health{}
	httpServer := &http.Server{Handler: health.Handler(metrics), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	listener, err := net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		shutdown, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = thrift.Shutdown(shutdown)
		return fmt.Errorf("bind HTTP listener: %w", err)
	}
	results := make(chan error, 2)
	go func() { results <- thrift.Serve() }()
	go func() { results <- httpServer.Serve(listener) }()
	health.SetReady(true)
	logger.Info("service started", "thrift_address", thrift.Addr().String(), "http_address", listener.Addr().String(), "catalog", cfg.UCCatalog, "metadata_reads", "schemas_and_external_delta_tables")
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-results:
	}
	health.SetReady(false)
	logger.Info("service draining")
	shutdown, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	drained := make(chan error, 1)
	go func() { drained <- thrift.Shutdown(shutdown) }()
	httpErr := httpServer.Shutdown(shutdown)
	if httpErr != nil {
		_ = httpServer.Close()
	}
	thriftErr := <-drained
	if errors.Is(thriftErr, context.DeadlineExceeded) {
		logger.Warn("Thrift drain deadline reached; connections closed")
		thriftErr = nil
	}
	if errors.Is(serveErr, http.ErrServerClosed) || errors.Is(serveErr, net.ErrClosed) {
		serveErr = nil
	}
	logger.Info("service stopped")
	return errors.Join(serveErr, httpErr, thriftErr)
}
