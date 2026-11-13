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
	"strconv"
	"syscall"
	"time"

	"github.com/giangdinhtt/pixbowl/internal/config"
	"github.com/giangdinhtt/pixbowl/internal/imageproc"
	"github.com/giangdinhtt/pixbowl/internal/server"
	"github.com/giangdinhtt/pixbowl/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pixbowl:", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.LogLevel)
	if err := run(cfg, logger); err != nil {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var verbosity slog.Level
	switch level {
	case "debug":
		verbosity = slog.LevelDebug
	case "warn":
		verbosity = slog.LevelWarn
	case "error":
		verbosity = slog.LevelError
	default:
		verbosity = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: verbosity}))
}

func run(cfg *config.Config, logger *slog.Logger) error {
	if err := imageproc.Start(imageproc.RuntimeConfig{
		Concurrency:   cfg.Concurrency,
		OutputFormat:  cfg.OutputFormat,
		Quality:       cfg.Quality,
		MaxDimension:  cfg.MaxDimension,
		MaxPixels:     cfg.MaxPixels,
		StripMetadata: cfg.StripMetadata,
	}); err != nil {
		return err
	}
	defer imageproc.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	storage, err := store.New(ctx, cfg)
	if err != nil {
		return err
	}
	if err := storage.Init(ctx); err != nil {
		return fmt.Errorf("init storage: %w", err)
	}

	handler := server.New(cfg, storage, logger)
	defer handler.Close()

	httpServer := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
		// Uploads can be slow on a bad mobile connection; cap generously.
		WriteTimeout:   3 * time.Minute,
		MaxHeaderBytes: 1 << 16,
		ErrorLog:       slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("pixbowl started",
			"addr", httpServer.Addr,
			"storage", cfg.Storage,
			"output_format", cfg.OutputFormat,
			"libvips", imageproc.Version(),
			"formats", imageproc.Formats(),
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}
