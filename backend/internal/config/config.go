package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

type Config struct {
	Mode               string
	Timezone           *time.Location
	DatabaseURL        string
	APIAddr            string
	APIToken           string
	SyncInterval       time.Duration
	WorkerPollInterval time.Duration
}

func Load() (Config, error) {
	locationName := getenv("APP_TIMEZONE", "America/Sao_Paulo")
	location, err := time.LoadLocation(locationName)
	if err != nil {
		return Config{}, fmt.Errorf("APP_TIMEZONE inválido: %w", err)
	}

	syncInterval, err := time.ParseDuration(getenv("SYNC_INTERVAL", "10m"))
	if err != nil || syncInterval < time.Minute {
		return Config{}, errors.New("SYNC_INTERVAL deve ser uma duração de pelo menos 1m")
	}
	pollInterval, err := time.ParseDuration(getenv("WORKER_POLL_INTERVAL", "3s"))
	if err != nil || pollInterval < 250*time.Millisecond {
		return Config{}, errors.New("WORKER_POLL_INTERVAL deve ser uma duração de pelo menos 250ms")
	}

	cfg := Config{
		Mode:               getenv("APP_MODE", "simulation"),
		Timezone:           location,
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		APIAddr:            getenv("API_ADDR", ":8080"),
		APIToken:           os.Getenv("APP_API_TOKEN"),
		SyncInterval:       syncInterval,
		WorkerPollInterval: pollInterval,
	}
	if cfg.Mode != "simulation" && cfg.Mode != "real" {
		return Config{}, errors.New("APP_MODE deve ser simulation ou real")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL é obrigatório")
	}
	return cfg, nil
}

func (c Config) ValidateAPI() error {
	if len(c.APIToken) < 24 {
		return errors.New("APP_API_TOKEN deve ter pelo menos 24 caracteres")
	}
	return nil
}

func (c Config) ValidateWorker() error {
	if c.Mode == "real" {
		return errors.New("modo real bloqueado: contratos de listagem e agendamento do MK ainda não foram confirmados")
	}
	return nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
