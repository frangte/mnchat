package repo

import (
	"context"
	"database/sql"
	"os"
	"slices"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// testDSNEnv points the filter test at a seeded MySQL. It is unset in make test, so
// that run needs no database.
const testDSNEnv = "MNCHAT_TEST_MYSQL_DSN"

func TestGetManyWithNoIDsNeverQueries(t *testing.T) {
	db, err := sql.Open("mysql", "mnchat:mnchat@tcp(127.0.0.1:1)/mnchat") // nothing listens here
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, ids := range [][]int64{nil, {}} {
		got, err := NewMessageRepo(db).GetMany(context.Background(), 1, ids)
		if err != nil {
			t.Fatalf("GetMany(%v): %v", ids, err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("GetMany(%v) = %#v, want empty and non-nil", ids, got)
		}
	}
}

func TestGetManyFilters(t *testing.T) {
	dsn := os.Getenv(testDSNEnv)
	if dsn == "" {
		t.Skipf("%s is not set", testDSNEnv)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() }) // registered first, so it runs after the row cleanups
	ctx := context.Background()

	// Seed data: channel 1 and user 1 are in workspace 1, channel 4 and user 5 in workspace 2.
	insert := func(workspaceID, channelID, userID int64, deletedAt any) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx,
			`INSERT INTO messages (workspace_id, channel_id, user_id, body, deleted_at) VALUES (?, ?, ?, 't06-getmany', ?)`,
			workspaceID, channelID, userID, deletedAt)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("insert id: %v", err)
		}
		t.Cleanup(func() {
			if _, err := db.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, id); err != nil {
				t.Errorf("clean up message %d: %v", id, err)
			}
		})
		return id
	}
	first := insert(1, 1, 1, nil)
	second := insert(1, 1, 1, nil)
	deleted := insert(1, 1, 1, time.Now())
	otherWorkspace := insert(2, 4, 5, nil)
	const missing = int64(1 << 40)

	got, err := NewMessageRepo(db).GetMany(ctx, 1, []int64{second, deleted, otherWorkspace, missing, first})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}

	ids := make([]int64, 0, len(got))
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if want := []int64{first, second}; !slices.Equal(ids, want) {
		t.Fatalf("ids = %v, want %v (deleted %d, other workspace %d)", ids, want, deleted, otherWorkspace)
	}
	if got[0].UserName == "" || got[0].CreatedAt.IsZero() {
		t.Fatalf("message came back incomplete: %+v", got[0])
	}
}
