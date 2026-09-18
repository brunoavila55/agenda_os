package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agendaos/internal/config"
	"agendaos/internal/store"
	syncer "agendaos/internal/sync"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuração inválida", "error", err)
		os.Exit(1)
	}
	if err := cfg.ValidateWorker(); err != nil {
		logger.Error("worker bloqueado", "error", err)
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

	hostname, _ := os.Hostname()
	workerID := fmt.Sprintf("%s-%d", hostname, os.Getpid())
	schedulerInterval := cfg.SyncInterval
	if schedulerInterval > time.Minute {
		schedulerInterval = time.Minute
	}
	logger.Info("worker iniciado", "worker_id", workerID, "mode", cfg.Mode, "scheduler_interval", schedulerInterval)

	scheduleAll(ctx, dataStore, logger)
	syncTicker := time.NewTicker(schedulerInterval)
	pollTicker := time.NewTicker(cfg.WorkerPollInterval)
	defer syncTicker.Stop()
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-syncTicker.C:
			scheduleAll(ctx, dataStore, logger)
		case <-pollTicker.C:
			processOne(ctx, dataStore, workerID, logger)
		}
	}
}

func scheduleAll(ctx context.Context, dataStore *store.Store, logger *slog.Logger) {
	operations, err := dataStore.Operations(ctx)
	if err != nil {
		logger.Error("listar operações para sincronização", "error", err)
		return
	}
	for _, operation := range operations {
		if !operation.Enabled {
			continue
		}
		created, err := dataStore.EnqueueDueSync(ctx, operation, "simulation")
		if err != nil {
			logger.Error("enfileirar sincronização", "operation_id", operation.ID, "error", err)
			continue
		}
		if created {
			logger.Info("sincronização enfileirada", "operation_id", operation.ID)
		}
	}
}

func processOne(ctx context.Context, dataStore *store.Store, workerID string, logger *slog.Logger) {
	job, err := dataStore.ClaimJob(ctx, workerID, 2*time.Minute)
	if err != nil {
		logger.Error("reservar tarefa", "error", err)
		return
	}
	if job == nil {
		return
	}
	logger.Info("processando tarefa", "job_id", job.ID, "kind", job.Kind, "attempt", job.Attempts)
	var processErr error
	switch job.Kind {
	case "sync_operation":
		processErr = syncer.RunSimulation(ctx, dataStore, job)
	default:
		processErr = fmt.Errorf("tipo de tarefa desconhecido: %s", job.Kind)
	}
	if processErr != nil {
		logger.Error("tarefa falhou", "job_id", job.ID, "error", processErr)
		_ = dataStore.FailJob(ctx, job.ID, processErr.Error(), time.Duration(job.Attempts)*10*time.Second)
		return
	}
	if err := dataStore.CompleteJob(ctx, job.ID); err != nil {
		logger.Error("marcar tarefa concluída", "job_id", job.ID, "error", err)
		return
	}
	logger.Info("tarefa concluída", "job_id", job.ID)
}
