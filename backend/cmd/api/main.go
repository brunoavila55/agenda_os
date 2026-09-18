package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agendaos/internal/config"
	"agendaos/internal/httpapi"
	"agendaos/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuração inválida", "error", err)
		os.Exit(1)
	}
	if err := cfg.ValidateAPI(); err != nil {
		logger.Error("configuração da API inválida", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	dataStore, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("banco indisponível", "error", err)
		os.Exit(1)
	}
	defer dataStore.Pool.Close()

	server := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           httpapi.New(dataStore, cfg.Mode, cfg.APIToken, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		logger.Info("API iniciada", "address", cfg.APIAddr, "mode", cfg.Mode)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("API interrompida", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}
