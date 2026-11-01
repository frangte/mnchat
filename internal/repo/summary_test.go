package repo

import (
	"context"
	"database/sql"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frangte/mnchat/internal/domain"
)

func embedding(dims int) []float32 {
	v := make([]float32, dims)
	for i := range v {
		v[i] = float32(math.Sin(float64(i))) / 3
	}
	return v
}

func TestVectorLiteralRoundTrips(t *testing.T) {
	v := embedding(domain.EmbeddingDims)
	v[0], v[1], v[2] = 1e-7, -0.1, 3

	got, err := vectorLiteral(v)
	if err != nil {
		t.Fatalf("vectorLiteral: %v", err)
	}
	if !strings.HasPrefix(got, "[1e-07,-0.1,3,") || !strings.HasSuffix(got, "]") {
		t.Fatalf("literal starts %q, want [1e-07,-0.1,3,...]", got[:min(len(got), 20)])
	}
	parts := strings.Split(strings.Trim(got, "[]"), ",")
	if len(parts) != domain.EmbeddingDims {
		t.Fatalf("literal has %d values, want %d", len(parts), domain.EmbeddingDims)
	}
	for i, p := range parts {
		f, err := strconv.ParseFloat(p, 32)
		if err != nil || float32(f) != v[i] {
			t.Fatalf("value %d: %q parses to %v (%v), want %v", i, p, f, err, v[i])
		}
	}
}

func TestVectorLiteralRejectsBadEmbeddings(t *testing.T) {
	nan, inf := embedding(domain.EmbeddingDims), embedding(domain.EmbeddingDims)
	nan[3] = float32(math.NaN())
	inf[9] = float32(math.Inf(-1))
	for name, v := range map[string][]float32{
		"none":   nil,
		"1023":   embedding(1023),
		"1025":   embedding(1025),
		"NaN":    nan,
		"-Inf":   inf,
		"tiny":   {0.1, 0.2},
		"single": {1},
	} {
		if _, err := vectorLiteral(v); err == nil {
			t.Errorf("%s: vectorLiteral accepted it", name)
		}
	}
}

// closedDB is a pool whose server never answers, so any query fails with a dial error.
func closedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", "mnchat:mnchat@tcp(127.0.0.1:1)/mnchat?timeout=1s") // nothing listens here
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUpsertRejectsABadEmbeddingBeforeWriting(t *testing.T) {
	err := NewSummaryRepo(closedDB(t)).Upsert(context.Background(), domain.ThreadSummary{ThreadRootID: 1}, embedding(512))
	if err == nil || !strings.Contains(err.Error(), "512 dimensions") {
		t.Fatalf("err = %v, want the dimension check, not a dial error", err)
	}
}

func TestEmptyListsNeverQuery(t *testing.T) {
	db := closedDB(t)
	for _, ids := range [][]int64{nil, {}} {
		summaries, err := NewSummaryRepo(db).GetMany(context.Background(), 1, ids)
		if err != nil || summaries == nil || len(summaries) != 0 {
			t.Fatalf("GetMany(%v) = %#v, %v; want empty and non-nil", ids, summaries, err)
		}
		latest, err := NewMessageRepo(db).LatestReplyIDs(context.Background(), 1, ids)
		if err != nil || latest == nil || len(latest) != 0 {
			t.Fatalf("LatestReplyIDs(%v) = %#v, %v; want empty and non-nil", ids, latest, err)
		}
	}
}

// TestSummaryRoundTrip writes to the database in testDSNEnv and removes its rows after.
// Replication copies them to the replica meanwhile, so don't point it at a stack that
// someone is demoing.
func TestSummaryRoundTrip(t *testing.T) {
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

	// Seed data: channel 1 and users 1 and 2 are in workspace 1.
	insert := func(rootID any, deletedAt any) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx,
			`INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body, deleted_at) VALUES (1, 1, ?, 2, 't07-summary', ?)`,
			rootID, deletedAt)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("insert id: %v", err)
		}
		// Deleting the root also deletes its summary, through the foreign key.
		t.Cleanup(func() {
			if _, err := db.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, id); err != nil {
				t.Errorf("clean up message %d: %v", id, err)
			}
		})
		return id
	}
	root := insert(nil, nil)
	reply := insert(root, nil)
	insert(root, time.Now()) // a deleted reply is never the latest
	bare := insert(nil, nil)

	messages, summaries := NewMessageRepo(db), NewSummaryRepo(db)
	if got, err := messages.LatestReplyID(ctx, 1, root); err != nil || got != reply {
		t.Fatalf("LatestReplyID = %d, %v; want %d", got, err, reply)
	}
	if got, err := messages.LatestReplyID(ctx, 2, root); err != nil || got != 0 {
		t.Fatalf("LatestReplyID in another workspace = %d, %v; want 0", got, err)
	}
	latest, err := messages.LatestReplyIDs(ctx, 1, []int64{root, bare})
	if err != nil || len(latest) != 1 || latest[root] != reply {
		t.Fatalf("LatestReplyIDs = %v, %v; want only %d: %d", latest, err, root, reply)
	}

	if _, err := summaries.Get(ctx, 1, root); err != domain.ErrNotFound {
		t.Fatalf("Get before upsert err = %v, want ErrNotFound", err)
	}
	s := domain.ThreadSummary{ThreadRootID: root, WorkspaceID: 1, ChannelID: 1, Summary: "first", LastMsgID: reply, Model: "gemma3:4b"}
	if err := summaries.Upsert(ctx, s, embedding(domain.EmbeddingDims)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	older := s
	older.Summary, older.LastMsgID = "older", reply-1
	if err := summaries.Upsert(ctx, older, embedding(domain.EmbeddingDims)); err != nil {
		t.Fatalf("Upsert older: %v", err)
	}

	got, err := summaries.Get(ctx, 1, root)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Summary != "first" || got.LastMsgID != reply || got.ChannelID != 1 || got.UpdatedAt.IsZero() {
		t.Fatalf("Get = %+v, want the first summary: an older one must not replace it", got)
	}
	if _, err := summaries.Get(ctx, 2, root); err != domain.ErrNotFound {
		t.Fatalf("Get in another workspace err = %v, want ErrNotFound", err)
	}
	var dims int
	if err := db.QueryRowContext(ctx, `SELECT VECTOR_DIM(embedding) FROM thread_summaries WHERE thread_root_id = ?`, root).Scan(&dims); err != nil || dims != domain.EmbeddingDims {
		t.Fatalf("stored %d dimensions (%v), want %d", dims, err, domain.EmbeddingDims)
	}

	many, err := summaries.GetMany(ctx, 1, []int64{root, bare})
	if err != nil || len(many) != 1 || many[0].ThreadRootID != root {
		t.Fatalf("GetMany = %+v, %v; want only %d", many, err, root)
	}
}
