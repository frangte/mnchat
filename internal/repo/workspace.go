package repo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/frangte/mnchat/internal/domain"
)

type WorkspaceRepo struct{ db *sql.DB }

func NewWorkspaceRepo(db *sql.DB) *WorkspaceRepo { return &WorkspaceRepo{db: db} }

func (r *WorkspaceRepo) ListForUser(ctx context.Context, userID int64) ([]domain.Workspace, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT w.id, w.org_id, o.name, w.name, wm.role
		FROM workspaces w
		JOIN orgs o ON o.id = w.org_id
		JOIN workspace_members wm ON wm.workspace_id = w.id
		WHERE wm.user_id = ?
		ORDER BY w.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Workspace
	for rows.Next() {
		var w domain.Workspace
		if err := rows.Scan(&w.ID, &w.OrgID, &w.OrgName, &w.Name, &w.Role); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// MemberRole returns the user's role, or ErrNotFound if they are not a member.
func (r *WorkspaceRepo) MemberRole(ctx context.Context, workspaceID, userID int64) (string, error) {
	var role string
	err := r.db.QueryRowContext(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`,
		workspaceID, userID,
	).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return role, err
}
