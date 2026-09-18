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
	"agendaos/internal/llm"
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

	// Workers AI stays optional: cfg.ValidateAPI already rejected a partial
	// configuration, so either all three fields are set or none are. Without
	// them, the panel keeps using the deterministic grouping on its own.
	var suggester store.GroupingSuggester
	if cfg.CloudflareAccountID != "" {
		client, err := llm.New(cfg.CloudflareAccountID, cfg.CloudflareAPIToken, cfg.CloudflareAIModel, &http.Client{Timeout: cfg.CloudflareAITimeout})
		if err != nil {
			logger.Error("configuração Workers AI inválida", "error", err)
			os.Exit(1)
		}
		suggester = client
		logger.Info("agrupamento por Workers AI habilitado", "model", client.Model())
	}

	server := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           httpapi.New(dataStore, cfg.Mode, cfg.APIToken, suggester, logger),
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
