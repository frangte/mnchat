//go:build integration

package repo

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/frangte/mnchat/internal/domain"
)

// TestSummaryUpsertInATransaction runs SummaryRepo's real SQL against the database in
// testDSNEnv, inside one transaction that it rolls back. Rolled-back work never reaches
// the binlog, so it is safe on a shared primary and leaves nothing on the replica:
//
//	MNCHAT_TEST_MYSQL_DSN='mnchat:mnchat@tcp(127.0.0.1:3306)/mnchat?parseTime=true&loc=UTC' \
//	  go test -tags integration -run InATransaction ./internal/repo/
func TestSummaryUpsertInATransaction(t *testing.T) {
	dsn := os.Getenv(testDSNEnv)
	if dsn == "" {
		t.Skipf("%s is not set", testDSNEnv)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	// Seed data: channel 1 and user 2 are in workspace 1.
	res, err := tx.ExecContext(ctx, `INSERT INTO messages (workspace_id, channel_id, user_id, body) VALUES (1, 1, 2, 't07-integration')`)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	root, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("root id: %v", err)
	}
	summaries := &SummaryRepo{db: tx}

	stored := func() ([]byte, int) {
		t.Helper()
		var raw []byte
		var dims int
		if err := tx.QueryRowContext(ctx,
			`SELECT embedding, VECTOR_DIM(embedding) FROM thread_summaries WHERE thread_root_id = ?`, root).Scan(&raw, &dims); err != nil {
			t.Fatalf("read embedding: %v", err)
		}
		return raw, dims
	}
	upsert := func(summary string, lastMsgID int64, v []float32) {
		t.Helper()
		s := domain.ThreadSummary{ThreadRootID: root, WorkspaceID: 1, ChannelID: 1, Summary: summary, LastMsgID: lastMsgID, Model: "gemma3:4b"}
		if err := summaries.Upsert(ctx, s, v); err != nil {
			t.Fatalf("Upsert %q: %v", summary, err)
		}
	}

	if _, err := summaries.Get(ctx, 1, root); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get before upsert err = %v, want ErrNotFound", err)
	}

	// Subnormals are what STRING_TO_VECTOR rejects with error 6138.
	first := awkwardEmbedding()
	upsert("first", root+2, first)
	want, _ := vectorBytes(first)
	if raw, dims := stored(); !bytes.Equal(raw, want) || dims != domain.EmbeddingDims {
		t.Fatalf("stored %d bytes and %d dims, want the %d bytes written, bit for bit", len(raw), dims, len(want))
	}

	upsert("older", root+1, embedding(domain.EmbeddingDims))
	got, err := summaries.Get(ctx, 1, root)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Summary != "first" || got.LastMsgID != root+2 || got.ChannelID != 1 || got.Model != "gemma3:4b" || got.UpdatedAt.IsZero() {
		t.Fatalf("Get = %+v, want the first summary: an older one must not replace it", got)
	}
	if raw, _ := stored(); !bytes.Equal(raw, want) {
		t.Fatal("an older summary replaced the embedding")
	}

	newer := embedding(domain.EmbeddingDims)
	upsert("newer", root+3, newer)
	wantNewer, _ := vectorBytes(newer)
	if got, err := summaries.Get(ctx, 1, root); err != nil || got.Summary != "newer" || got.LastMsgID != root+3 {
		t.Fatalf("Get = %+v, %v; want the newer summary", got, err)
	}
	if raw, _ := stored(); !bytes.Equal(raw, wantNewer) {
		t.Fatal("a newer summary didn't replace the embedding")
	}

	if _, err := summaries.Get(ctx, 2, root); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get in another workspace err = %v, want ErrNotFound", err)
	}
	many, err := summaries.GetMany(ctx, 1, []int64{root, 1 << 40})
	if err != nil || len(many) != 1 || many[0].ThreadRootID != root || many[0].Summary != "newer" {
		t.Fatalf("GetMany = %+v, %v; want only %d", many, err, root)
	}
	if many, err := summaries.GetMany(ctx, 2, []int64{root}); err != nil || len(many) != 0 {
		t.Fatalf("GetMany in another workspace = %+v, %v; want none", many, err)
	}
}
