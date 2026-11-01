package repo

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/frangte/mnchat/internal/domain"
)

type MessageRepo struct{ db *sql.DB }

func NewMessageRepo(db *sql.DB) *MessageRepo { return &MessageRepo{db: db} }

const messageColumns = `
	SELECT m.id, m.workspace_id, m.channel_id, m.thread_root_id, m.user_id, u.display_name, m.body, m.reply_count, m.created_at
	FROM messages m
	JOIN users u ON u.id = m.user_id`

// List returns up to limit top-level messages older than beforeID (0 = newest), oldest first.
func (r *MessageRepo) List(ctx context.Context, workspaceID, channelID, beforeID int64, limit int) ([]domain.Message, error) {
	rows, err := r.db.QueryContext(ctx, messageColumns+`
		WHERE m.workspace_id = ? AND m.channel_id = ?
		  AND m.thread_root_id IS NULL AND m.deleted_at IS NULL
		  AND (? = 0 OR m.id < ?)
		ORDER BY m.id DESC
		LIMIT ?`, workspaceID, channelID, beforeID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	return collectNewestFirst(rows)
}

// ListReplies returns the newest limit replies to rootID, oldest first.
func (r *MessageRepo) ListReplies(ctx context.Context, workspaceID, channelID, rootID int64, limit int) ([]domain.Message, error) {
	rows, err := r.db.QueryContext(ctx, messageColumns+`
		WHERE m.workspace_id = ? AND m.channel_id = ?
		  AND m.thread_root_id = ? AND m.deleted_at IS NULL
		ORDER BY m.id DESC
		LIMIT ?`, workspaceID, channelID, rootID, limit)
	if err != nil {
		return nil, err
	}
	return collectNewestFirst(rows)
}

// Get returns ErrNotFound for messages that are missing, deleted or in another workspace.
func (r *MessageRepo) Get(ctx context.Context, workspaceID, id int64) (domain.Message, error) {
	row := r.db.QueryRowContext(ctx, messageColumns+`
		WHERE m.workspace_id = ? AND m.id = ? AND m.deleted_at IS NULL`, workspaceID, id)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return m, domain.ErrNotFound
	}
	return m, err
}

// GetMany returns the messages it finds, oldest first, leaving out any that are
// missing, deleted or in another workspace.
func (r *MessageRepo) GetMany(ctx context.Context, workspaceID int64, ids []int64) ([]domain.Message, error) {
	if len(ids) == 0 {
		return []domain.Message{}, nil // IN () is a syntax error
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, workspaceID)
	for _, id := range ids {
		args = append(args, id)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := r.db.QueryContext(ctx, messageColumns+`
		WHERE m.workspace_id = ? AND m.id IN (`+placeholders+`) AND m.deleted_at IS NULL
		ORDER BY m.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	return collectNewestFirst(rows)
}

// Create inserts the message and, for a reply, bumps its root's reply_count.
func (r *MessageRepo) Create(ctx context.Context, m domain.Message) (domain.Message, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Message{}, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body) VALUES (?, ?, ?, ?, ?)`,
		m.WorkspaceID, m.ChannelID, m.ThreadRootID, m.UserID, m.Body)
	if err != nil {
		return domain.Message{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Message{}, err
	}
	if m.ThreadRootID != nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE messages SET reply_count = reply_count + 1 WHERE id = ?`, *m.ThreadRootID); err != nil {
			return domain.Message{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Message{}, err
	}
	return scanMessage(r.db.QueryRowContext(ctx, messageColumns+` WHERE m.id = ?`, id))
}

// collectNewestFirst drains rows ordered by id DESC and returns them oldest first.
func collectNewestFirst(rows *sql.Rows) ([]domain.Message, error) {
	defer rows.Close()

	out := []domain.Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Reverse(out)
	return out, nil
}

func scanMessage(s scanner) (domain.Message, error) {
	var (
		m        domain.Message
		threadID sql.NullInt64
	)
	if err := s.Scan(&m.ID, &m.WorkspaceID, &m.ChannelID, &threadID, &m.UserID, &m.UserName, &m.Body, &m.ReplyCount, &m.CreatedAt); err != nil {
		return m, err
	}
	if threadID.Valid {
		m.ThreadRootID = &threadID.Int64
	}
	return m, nil
}
