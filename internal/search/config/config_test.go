package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{
		"SEARCH_GRPC_LISTEN", "SEARCH_METRICS_ADDR", "SEARCH_MYSQL_DSN",
		"AI_PROVIDER", "OLLAMA_URL", "OLLAMA_CHAT_MODEL", "OLLAMA_EMBED_MODEL",
	} {
		t.Setenv(key, "")
	}

	got := Load()
	want := Config{
		GRPCListen:       ":9190",
		MetricsAddr:      ":9191",
		MySQLDSN:         "search_reader:search_reader@tcp(127.0.0.1:3307)/mnchat?parseTime=true&loc=UTC",
		AIProvider:       "ollama",
		OllamaURL:        "http://localhost:11434",
		OllamaChatModel:  "gemma3:4b",
		OllamaEmbedModel: "bge-m3",
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadReadsEnvironment(t *testing.T) {
	t.Setenv("SEARCH_GRPC_LISTEN", ":1")
	t.Setenv("SEARCH_METRICS_ADDR", ":2")
	t.Setenv("SEARCH_MYSQL_DSN", "dsn")

	got := Load()
	if got.GRPCListen != ":1" || got.MetricsAddr != ":2" || got.MySQLDSN != "dsn" {
		t.Fatalf("Load() = %+v, want values from the environment", got)
	}
}
