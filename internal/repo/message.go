package repo

import (
	"context"
	"database/sql"
	"slices"

	"github.com/frangte/mnchat/internal/domain"
)

type MessageRepo struct{ db *sql.DB }

func NewMessageRepo(db *sql.DB) *MessageRepo { return &MessageRepo{db: db} }

const messageColumns = `
	SELECT m.id, m.workspace_id, m.channel_id, m.thread_root_id, m.user_id, u.display_name, m.body, m.created_at
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

func (r *MessageRepo) Create(ctx context.Context, m domain.Message) (domain.Message, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body) VALUES (?, ?, ?, ?, ?)`,
		m.WorkspaceID, m.ChannelID, m.ThreadRootID, m.UserID, m.Body)
	if err != nil {
		return domain.Message{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Message{}, err
	}
	return scanMessage(r.db.QueryRowContext(ctx, messageColumns+` WHERE m.id = ?`, id))
}

func scanMessage(s scanner) (domain.Message, error) {
	var (
		m        domain.Message
		threadID sql.NullInt64
	)
	if err := s.Scan(&m.ID, &m.WorkspaceID, &m.ChannelID, &threadID, &m.UserID, &m.UserName, &m.Body, &m.CreatedAt); err != nil {
		return m, err
	}
	if threadID.Valid {
		m.ThreadRootID = &threadID.Int64
	}
	return m, nil
}
