package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/frangte/mnchat/internal/domain"
)

// SummaryRepo stores thread summaries on the primary. api is the only writer of
// thread_summaries, so the replica stays read-only and gets the rows by replication.
type SummaryRepo struct{ db *sql.DB }

func NewSummaryRepo(db *sql.DB) *SummaryRepo { return &SummaryRepo{db: db} }

// summaryColumns leaves out the embedding: 4 KB a row that chat never reads.
const summaryColumns = `
	SELECT thread_root_id, workspace_id, channel_id, summary, last_msg_id, model, updated_at
	FROM thread_summaries`

// Get returns ErrNotFound when the thread has no summary in this workspace.
func (r *SummaryRepo) Get(ctx context.Context, workspaceID, rootID int64) (domain.ThreadSummary, error) {
	row := r.db.QueryRowContext(ctx, summaryColumns+`
		WHERE workspace_id = ? AND thread_root_id = ?`, workspaceID, rootID)
	s, err := scanSummary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return s, domain.ErrNotFound
	}
	return s, err
}

// GetMany returns the summaries it finds, in no particular order, leaving out
// threads without one and threads in another workspace.
func (r *SummaryRepo) GetMany(ctx context.Context, workspaceID int64, rootIDs []int64) ([]domain.ThreadSummary, error) {
	if len(rootIDs) == 0 {
		return []domain.ThreadSummary{}, nil // IN () is a syntax error
	}
	args, placeholders := inArgs(workspaceID, rootIDs)
	rows, err := r.db.QueryContext(ctx, summaryColumns+`
		WHERE workspace_id = ? AND thread_root_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.ThreadSummary{}
	for rows.Next() {
		s, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Upsert stores s and its embedding, replacing the thread's summary. One that covers
// fewer replies than the stored one never replaces it: two requests can finish out of
// order, and the slower one may have read an older replica. Assignments run left to
// right, so last_msg_id has to come last.
func (r *SummaryRepo) Upsert(ctx context.Context, s domain.ThreadSummary, embedding []float32) error {
	vector, err := vectorLiteral(embedding)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO thread_summaries (thread_root_id, workspace_id, channel_id, last_msg_id, summary, model, embedding)
		VALUES (?, ?, ?, ?, ?, ?, STRING_TO_VECTOR(?)) AS new
		ON DUPLICATE KEY UPDATE
			summary     = IF(new.last_msg_id >= thread_summaries.last_msg_id, new.summary, thread_summaries.summary),
			model       = IF(new.last_msg_id >= thread_summaries.last_msg_id, new.model, thread_summaries.model),
			embedding   = IF(new.last_msg_id >= thread_summaries.last_msg_id, new.embedding, thread_summaries.embedding),
			last_msg_id = GREATEST(new.last_msg_id, thread_summaries.last_msg_id)`,
		s.ThreadRootID, s.WorkspaceID, s.ChannelID, s.LastMsgID, s.Summary, s.Model, vector)
	return err
}

// vectorLiteral formats v for STRING_TO_VECTOR as "[0.1,0.2]". Precision -1 with
// bitSize 32 is the shortest text that parses back to the same float32.
func vectorLiteral(v []float32) (string, error) {
	if len(v) != domain.EmbeddingDims {
		return "", fmt.Errorf("embedding has %d dimensions, want %d", len(v), domain.EmbeddingDims)
	}
	b := make([]byte, 0, len(v)*12)
	b = append(b, '[')
	for i, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return "", fmt.Errorf("embedding value %d is %v", i, f)
		}
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendFloat(b, float64(f), 'g', -1, 32)
	}
	return string(append(b, ']')), nil
}

func scanSummary(s scanner) (domain.ThreadSummary, error) {
	var t domain.ThreadSummary
	err := s.Scan(&t.ThreadRootID, &t.WorkspaceID, &t.ChannelID, &t.Summary, &t.LastMsgID, &t.Model, &t.UpdatedAt)
	return t, err
}
