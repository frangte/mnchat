// Package store reads the MySQL replica, only through the search_*_v1 views.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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

// ThreadMessage is one message of a thread, with its author's display name.
type ThreadMessage struct {
	ID int64
	// ThreadRootID is 0 for a top-level message.
	ThreadRootID int64
	UserName     string
	Body         string
}

// ThreadMessages returns the thread under rootID, root first and then its newest
// replies in ID order, at most limit messages in all. It returns nothing when the
// root isn't on the replica, because it hasn't replicated yet or was deleted.
//
// It reads the root by primary key, then the replies in the root's channel. The
// single "id = ? OR thread_root_id = ?" query scans every message, since no index
// starts with thread_root_id, while (channel_id, thread_root_id) is indexed and
// lets LIMIT stop at the newest replies.
func (s *Store) ThreadMessages(ctx context.Context, workspaceID, rootID int64, limit int) ([]ThreadMessage, error) {
	var (
		root      ThreadMessage
		channelID int64
		parent    sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, channel_id, thread_root_id, user_name, body
		FROM search_thread_messages_v1
		WHERE workspace_id = ? AND id = ?`, workspaceID, rootID,
	).Scan(&root.ID, &channelID, &parent, &root.UserName, &root.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	root.ThreadRootID = parent.Int64
	// A reply has no replies of its own, and the caller rejects it anyway.
	if parent.Valid || limit <= 1 {
		return []ThreadMessage{root}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_name, body
		FROM search_thread_messages_v1
		WHERE workspace_id = ? AND channel_id = ? AND thread_root_id = ?
		ORDER BY id DESC
		LIMIT ?`, workspaceID, channelID, rootID, limit-1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var replies []ThreadMessage
	for rows.Next() {
		m := ThreadMessage{ThreadRootID: rootID}
		if err := rows.Scan(&m.ID, &m.UserName, &m.Body); err != nil {
			return nil, err
		}
		replies = append(replies, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Reverse(replies)
	return append([]ThreadMessage{root}, replies...), nil
}

// ThreadVector is one summarized thread's embedding as the VECTOR column stores
// it: 4 bytes per dimension, little-endian float32.
type ThreadVector struct {
	ThreadRootID int64
	Embedding    []byte
}

// ThreadVectors returns the embedding of every summarized thread in channelIDs.
// An empty channelIDs returns nothing without a query.
func (s *Store) ThreadVectors(ctx context.Context, workspaceID int64, channelIDs []int64) ([]ThreadVector, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}
	stmt, args := threadVectors(workspaceID, channelIDs)
	rows, err := s.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ThreadVector
	for rows.Next() {
		var v ThreadVector
		if err := rows.Scan(&v.ThreadRootID, &v.Embedding); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// threadVectors builds the statement and its arguments. channelIDs must not be empty.
func threadVectors(workspaceID int64, channelIDs []int64) (string, []any) {
	stmt := `
		SELECT thread_root_id, embedding
		FROM search_threads_v1
		WHERE workspace_id = ? AND channel_id IN (` + placeholders(len(channelIDs)) + `)`

	args := make([]any, 0, len(channelIDs)+1)
	args = append(args, workspaceID)
	for _, id := range channelIDs {
		args = append(args, id)
	}
	return stmt, args
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
