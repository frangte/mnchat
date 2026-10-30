package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"

	"github.com/frangte/mnchat/internal/domain"
)

const mysqlErrDuplicateEntry = 1062

type ChannelRepo struct{ db *sql.DB }

func NewChannelRepo(db *sql.DB) *ChannelRepo { return &ChannelRepo{db: db} }

// visibleChannelsSQL selects channels the user can read: public ones, plus any they are a member of.
const visibleChannelsSQL = `
	SELECT c.id, c.workspace_id, c.type, COALESCE(c.name, ''), c.created_at, cm.user_id IS NOT NULL
	FROM channels c
	LEFT JOIN channel_members cm ON cm.channel_id = c.id AND cm.user_id = ?
	WHERE c.workspace_id = ? AND c.archived_at IS NULL
	  AND (c.type = 'public' OR cm.user_id IS NOT NULL)`

func (r *ChannelRepo) ListVisible(ctx context.Context, workspaceID, userID int64) ([]domain.Channel, error) {
	rows, err := r.db.QueryContext(ctx, visibleChannelsSQL+` ORDER BY c.name`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetVisible returns ErrNotFound both for missing channels and ones the user may not read.
func (r *ChannelRepo) GetVisible(ctx context.Context, workspaceID, channelID, userID int64) (domain.Channel, error) {
	row := r.db.QueryRowContext(ctx, visibleChannelsSQL+` AND c.id = ?`, userID, workspaceID, channelID)
	c, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return c, domain.ErrNotFound
	}
	return c, err
}

// Create inserts the channel and adds the creator as its first member.
func (r *ChannelRepo) Create(ctx context.Context, workspaceID, creatorID int64, typ domain.ChannelType, name string) (domain.Channel, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Channel{}, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO channels (workspace_id, type, name, created_by) VALUES (?, ?, ?, ?)`,
		workspaceID, typ, name, creatorID)
	if err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == mysqlErrDuplicateEntry {
			return domain.Channel{}, fmt.Errorf("%w: channel #%s already exists", domain.ErrConflict, name)
		}
		return domain.Channel{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Channel{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO channel_members (channel_id, user_id) VALUES (?, ?)`, id, creatorID); err != nil {
		return domain.Channel{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Channel{}, err
	}
	return r.GetVisible(ctx, workspaceID, id, creatorID)
}

func (r *ChannelRepo) AddMember(ctx context.Context, channelID, userID int64) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT IGNORE INTO channel_members (channel_id, user_id) VALUES (?, ?)`, channelID, userID)
	return err
}

type scanner interface{ Scan(dest ...any) error }

func scanChannel(s scanner) (domain.Channel, error) {
	var c domain.Channel
	err := s.Scan(&c.ID, &c.WorkspaceID, &c.Type, &c.Name, &c.CreatedAt, &c.IsMember)
	return c, err
}
