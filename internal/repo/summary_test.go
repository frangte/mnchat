package repo

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"os"
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

// awkwardEmbedding has the values a text format can lose: subnormals, which
// STRING_TO_VECTOR rejects, negative zero, and the largest float32.
func awkwardEmbedding() []float32 {
	v := embedding(domain.EmbeddingDims)
	v[0] = math.SmallestNonzeroFloat32
	v[1] = 1e-40
	v[2] = float32(math.Copysign(0, -1))
	v[3] = math.MaxFloat32
	return v
}

func TestVectorBytesRoundTrips(t *testing.T) {
	v := awkwardEmbedding()

	b, err := vectorBytes(v)
	if err != nil {
		t.Fatalf("vectorBytes: %v", err)
	}
	if len(b) != 4*domain.EmbeddingDims {
		t.Fatalf("got %d bytes, want %d", len(b), 4*domain.EmbeddingDims)
	}
	for i, want := range v {
		got := binary.LittleEndian.Uint32(b[i*4:])
		if got != math.Float32bits(want) {
			t.Fatalf("value %d: bits %#x, want %#x (%v)", i, got, math.Float32bits(want), want)
		}
	}
}

func TestVectorBytesRejectsBadEmbeddings(t *testing.T) {
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
		if _, err := vectorBytes(v); err == nil {
			t.Errorf("%s: vectorBytes accepted it", name)
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

// TestLatestReplyIDs writes messages to the database in testDSNEnv, like
// TestGetManyFilters, and removes them after. Replication copies them to the replica
// meanwhile, so don't point it at a stack someone is demoing. MessageRepo opens its own
// transactions, so this one can't run inside a rolled-back transaction the way
// TestSummaryUpsertInATransaction does.
func TestLatestReplyIDs(t *testing.T) {
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
			`INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body, deleted_at) VALUES (1, 1, ?, 2, 't07-latest-reply', ?)`,
			rootID, deletedAt)
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
	root := insert(nil, nil)
	reply := insert(root, nil)
	insert(root, time.Now()) // a deleted reply is never the latest
	bare := insert(nil, nil)

	messages := NewMessageRepo(db)
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
}
