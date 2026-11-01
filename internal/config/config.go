// Package config loads runtime settings from environment variables.
package config

import (
	"log/slog"
	"os"
	"strings"
)

const devJWTSecret = "dev-secret-change-me"

type Config struct {
	HTTPAddr  string
	MySQLDSN  string
	JWTSecret string
	// WSOrigins are host patterns allowed to open cross-origin WebSockets.
	WSOrigins []string
	// SearchGRPCAddr is the search service. Nothing connects to it until the first search.
	SearchGRPCAddr string
}

func Load() Config {
	cfg := Config{
		HTTPAddr:  env("HTTP_ADDR", ":8080"),
		MySQLDSN:  env("MYSQL_DSN", "mnchat:mnchat@tcp(127.0.0.1:3306)/mnchat?parseTime=true&loc=UTC"),
		JWTSecret: env("JWT_SECRET", devJWTSecret),
		WSOrigins: strings.Split(env("WS_ORIGINS", "localhost:*,127.0.0.1:*"), ","),

		SearchGRPCAddr: env("SEARCH_GRPC_ADDR", "localhost:9190"),
	}
	if cfg.JWTSecret == devJWTSecret {
		slog.Warn("JWT_SECRET not set, using insecure development secret")
	}
	return cfg
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
