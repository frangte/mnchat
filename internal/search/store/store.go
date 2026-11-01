// Package store reads the MySQL replica, only through the search_*_v1 views.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	maxOpenConns    = 10
	maxIdleConns    = 5
	connMaxLifetime = 5 * time.Minute
	connMaxIdleTime = time.Minute

	// Socket limits, for a replica that accepts connections and then goes quiet.
	// A DSN can override them. Requests have a shorter context deadline as well.
	dialTimeout  = 2 * time.Second
	readTimeout  = 5 * time.Second
	writeTimeout = 5 * time.Second

	startupPingTimeout = 2 * time.Second
)

// Open builds the connection pool. It fails only on a malformed DSN: an unreachable
// replica is logged, and requests report it, so search can start in any order.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse mysql dsn: %w", err)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = dialTimeout
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = readTimeout
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = writeTimeout
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, startupPingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		slog.Warn("replica unreachable at startup, serving anyway", "err", err)
	}
	return db, nil
}

// MessageHit is one full-text match. Chat loads the message from the primary.
type MessageHit struct {
	ID    int64
	Score float64
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// SearchMessages returns the best matches in channelIDs, best first. Text with
// nothing searchable and an empty channelIDs both return no hits without a query,
// so neither depends on the replica.
func (s *Store) SearchMessages(ctx context.Context, workspaceID int64, channelIDs []int64, text string, limit int) ([]MessageHit, error) {
	query := booleanQuery(text)
	if query == "" || len(channelIDs) == 0 {
		return nil, nil
	}

	stmt, args := messageSearch(workspaceID, channelIDs, query, limit)
	rows, err := s.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []MessageHit
	for rows.Next() {
		var h MessageHit
		if err := rows.Scan(&h.ID, &h.Score); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// messageSearch builds the statement and its arguments. The boolean query binds
// twice, because MATCH appears once to filter and once to score. channelIDs must
// not be empty, since "IN ()" is a syntax error.
func messageSearch(workspaceID int64, channelIDs []int64, query string, limit int) (string, []any) {
	stmt := `
		SELECT id, MATCH(body) AGAINST(? IN BOOLEAN MODE) AS score
		FROM search_messages_v1
		WHERE workspace_id = ? AND channel_id IN (` + placeholders(len(channelIDs)) + `)
		  AND MATCH(body) AGAINST(? IN BOOLEAN MODE)
		ORDER BY score DESC, id DESC
		LIMIT ?`

	args := make([]any, 0, len(channelIDs)+4)
	args = append(args, query, workspaceID)
	for _, id := range channelIDs {
		args = append(args, id)
	}
	return stmt, append(args, query, limit)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
