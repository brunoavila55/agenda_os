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
