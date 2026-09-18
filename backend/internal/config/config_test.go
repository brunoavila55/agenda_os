package config

import "testing"

func TestWorkerRejectsRealMode(t *testing.T) {
	cfg := Config{Mode: "real"}
	if cfg.ValidateWorker() == nil {
		t.Fatal("modo real deveria permanecer bloqueado")
	}
}

func TestAPIRequiresLongToken(t *testing.T) {
	cfg := Config{APIToken: "curto"}
	if cfg.ValidateAPI() == nil {
		t.Fatal("token curto deveria ser rejeitado")
	}
}

func TestAPIRejectsPartialWorkersAIConfiguration(t *testing.T) {
	cfg := Config{APIToken: "123456789012345678901234", CloudflareAccountID: "account-only"}
	if cfg.ValidateAPI() == nil {
		t.Fatal("configuração parcial do Workers AI deveria ser rejeitada")
	}
	cfg.CloudflareAPIToken = "token"
	cfg.CloudflareAIModel = "@cf/example/model"
	if err := cfg.ValidateAPI(); err != nil {
		t.Fatalf("configuração completa rejeitada: %v", err)
	}
}
