// Package config — чтение env для всех ролей одного бинарника (serve/worker
// <stage>). Намеренно stdlib-only (os.Getenv) — конфиг-слой не должен тянуть
// сторонние парсеры ради нескольких переменных.
package config

import (
	"os"
	"strconv"
)

// Config — общие настройки; per-роль настройки (например, per-provider AI
// API-ключи) добавляются рядом отдельными Load-функциями по мере
// реализации конкретной adapter/outbound.
type Config struct {
	PostgresDSN   string
	RabbitMQURL   string
	MinIOEndpoint string
	Prefetch      int
	// AdminToken — bearer-токен для /admin/*; пусто ⟹ admin API открыт (демо).
	AdminToken string
}

func Load() Config {
	return Config{
		PostgresDSN:   os.Getenv("POSTGRES_DSN"),
		RabbitMQURL:   os.Getenv("RABBITMQ_URL"),
		MinIOEndpoint: os.Getenv("MINIO_ENDPOINT"),
		Prefetch:      envInt("PREFETCH_COUNT", 1),
		AdminToken:    os.Getenv("ADMIN_TOKEN"),
	}
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
