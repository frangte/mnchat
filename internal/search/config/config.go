// Package config loads the search binary's settings from environment variables.
// It stays separate from internal/config so search shares no code with chat.
package config

import "os"

type Config struct {
	GRPCListen  string
	MetricsAddr string
	MySQLDSN    string
	// AIProvider is "ollama", or "fake" for tests.
	AIProvider       string
	OllamaURL        string
	OllamaChatModel  string
	OllamaEmbedModel string
}

func Load() Config {
	return Config{
		GRPCListen:       env("SEARCH_GRPC_LISTEN", ":9190"),
		MetricsAddr:      env("SEARCH_METRICS_ADDR", ":9191"),
		MySQLDSN:         env("SEARCH_MYSQL_DSN", "search_reader:search_reader@tcp(127.0.0.1:3307)/mnchat?parseTime=true&loc=UTC"),
		AIProvider:       env("AI_PROVIDER", "ollama"),
		OllamaURL:        env("OLLAMA_URL", "http://localhost:11434"),
		OllamaChatModel:  env("OLLAMA_CHAT_MODEL", "gemma3:4b"),
		OllamaEmbedModel: env("OLLAMA_EMBED_MODEL", "bge-m3"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
