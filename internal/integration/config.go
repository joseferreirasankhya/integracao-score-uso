package integration

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config reúne as credenciais e endpoints da integração. Carregada uma única
// vez em LoadConfig e repassada explicitamente — sem estado global.
type Config struct {
	BaseURL      string
	MitraToken   string
	SankhyaToken string
}

// LoadConfig carrega o .env (opcional: em produção as variáveis vêm direto do
// ambiente) e valida que todas as obrigatórias estão presentes, reportando de
// uma vez todas as que faltarem.
func LoadConfig() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		BaseURL:      os.Getenv("BASE_URL"),
		MitraToken:   os.Getenv("MITRA_TOKEN"),
		SankhyaToken: os.Getenv("SANKHYA_TOKEN"),
	}

	var missing []string
	if cfg.BaseURL == "" {
		missing = append(missing, "BASE_URL")
	}
	if cfg.MitraToken == "" {
		missing = append(missing, "MITRA_TOKEN")
	}
	if cfg.SankhyaToken == "" {
		missing = append(missing, "SANKHYA_TOKEN")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("variáveis de ambiente obrigatórias ausentes: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}
