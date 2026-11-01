//go:build integration

package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frangte/mnchat/internal/search/semantic"
)

// T05 fixtures use their own prefix, so the T04 cleanup in TestMain and these tests
// never delete each other's rows. Bodies avoid every word the T04 tests search for.
const threadPrefix = "t05-store-it "

func openPrimary(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", env("SEARCH_TEST_PRIMARY_DSN", "mnchat:mnchat@tcp(127.0.0.1:3306)/mnchat?parseTime=true&loc=UTC"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Rows left by a killed run. Deleting a root cascades to its summary.
	if _, err := db.Exec(`DELETE FROM messages WHERE body LIKE ? AND thread_root_id IS NOT NULL`, threadPrefix+"%"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM messages WHERE body LIKE ?`, threadPrefix+"%"); err != nil {
		t.Fatal(err)
	}
	return db
}

// insertMessage writes a message on the primary and deletes it when the test ends.
// rootID 0 makes a top-level message.
func insertMessage(t *testing.T, primary *sql.DB, workspaceID, channelID, rootID, userID int64, body string) int64 {
	t.Helper()
	var root any
	if rootID != 0 {
		root = rootID
	}
	res, err := primary.Exec(`INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body) VALUES (?, ?, ?, ?, ?)`,
		workspaceID, channelID, root, userID, threadPrefix+body)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := primary.Exec(`DELETE FROM messages WHERE id = ?`, id); err != nil {
			t.Errorf("remove message %d: %v", id, err)
		}
	})
	return id
}

// insertSummary writes a thread_summaries row with STRING_TO_VECTOR, the way an
// operator or a migration would, and deletes it when the test ends.
func insertSummary(t *testing.T, primary *sql.DB, rootID, workspaceID, channelID int64, vec []float32) {
	t.Helper()
	_, err := primary.Exec(`INSERT INTO thread_summaries (thread_root_id, workspace_id, channel_id, last_msg_id, summary, model, embedding)
		VALUES (?, ?, ?, ?, 't05 fixture', 't05-it', STRING_TO_VECTOR(?))`,
		rootID, workspaceID, channelID, rootID, vectorLiteral(vec))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := primary.Exec(`DELETE FROM thread_summaries WHERE thread_root_id = ?`, rootID); err != nil {
			t.Errorf("remove summary %d: %v", rootID, err)
		}
	})
}

// vectorLiteral formats each value with the fewest digits that parse back to the
// same float32, so the round trip can be compared bit for bit.
func vectorLiteral(vec []float32) string {
	parts := make([]string, len(vec))
	for i, v := range vec {
		parts[i] = strconv.FormatFloat(float64(v), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func waitForRows(t *testing.T, query string, args []any, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var got int
		err := replica.QueryRow(query, args...).Scan(&got)
		if err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("replica has %d rows for %q, want %d: %v", got, query, want, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func testVector() []float32 {
	vec := make([]float32, 1024)
	for i := range vec {
		vec[i] = float32(i%7-3) * 0.25
	}
	// Values that aren't small multiples of a power of two, and the largest float32.
	// STRING_TO_VECTOR rejects subnormals such as 1e-45 with error 6138, so the
	// smallest value here is a normal one. Embeddings never hold subnormals.
	copy(vec, []float32{1, -2.5, 0.1, 1e-30, math.MaxFloat32, -1e-30, 1.0 / 3})
	return vec
}

// The Go driver hands a VECTOR column over as raw bytes: 4 per dimension,
// little-endian float32. Everything in semantic search depends on this.
func TestVectorRoundTripThroughTheReplica(t *testing.T) {
	primary := openPrimary(t)
	root := insertMessage(t, primary, workspace1, general, 0, 1, "vector fixture root")
	want := testVector()
	insertSummary(t, primary, root, workspace1, general, want)
	waitForRows(t, `SELECT COUNT(*) FROM search_threads_v1 WHERE thread_root_id = ?`, []any{root}, 1)

	var raw any
	if err := replica.QueryRow(`SELECT embedding FROM search_threads_v1 WHERE thread_root_id = ?`, root).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	b, ok := raw.([]byte)
	if !ok {
		t.Fatalf("driver returned %T, want []byte", raw)
	}
	if len(b) != 4*len(want) {
		t.Fatalf("got %d bytes, want %d", len(b), 4*len(want))
	}
	if got := hex.EncodeToString(b[:8]); got != "0000803f000020c0" {
		t.Fatalf("first two values are %s, want 1.0 then -2.5 as little-endian float32", got)
	}
	for i, w := range want {
		got := math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
		if math.Float32bits(got) != math.Float32bits(w) {
			t.Fatalf("dim %d = %v, want %v", i, got, w)
		}
	}

	// The same bytes through the code search runs.
	vectors, err := store.ThreadVectors(context.Background(), workspace1, []int64{general})
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(vectors, func(v ThreadVector) bool { return v.ThreadRootID == root })
	if idx < 0 {
		t.Fatalf("ThreadVectors is missing thread %d", root)
	}
	decoded, err := semantic.Decode(vectors[idx].Embedding)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(decoded, want) {
		t.Fatal("semantic.Decode of the stored vector differs from what was written")
	}
}

// insertReplies bulk-inserts n replies under rootID, from users 1-4 in turn, then
// one soft-deleted reply, and returns all their IDs in order. One statement means
// the deleted row has replicated once the visible ones show up.
func insertReplies(t *testing.T, primary *sql.DB, workspaceID, channelID, rootID int64, n int) []int64 {
	t.Helper()
	t.Cleanup(func() {
		if _, err := primary.Exec(`DELETE FROM messages WHERE thread_root_id = ?`, rootID); err != nil {
			t.Errorf("remove replies of %d: %v", rootID, err)
		}
	})
	values := make([]string, n+1)
	args := make([]any, 0, 6*(n+1))
	for i := range n + 1 {
		values[i] = "(?, ?, ?, ?, ?, ?)"
		var deletedAt any
		if i == n {
			deletedAt = time.Now().UTC()
		}
		args = append(args, workspaceID, channelID, rootID, 1+i%4, threadPrefix+"reply "+strconv.Itoa(i), deletedAt)
	}
	if _, err := primary.Exec(`INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body, deleted_at) VALUES `+
		strings.Join(values, ","), args...); err != nil {
		t.Fatal(err)
	}
	rows, err := primary.Query(`SELECT id FROM messages WHERE thread_root_id = ? ORDER BY id`, rootID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func messageIDs(msgs []ThreadMessage) []int64 {
	ids := make([]int64, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	return ids
}

// Seed thread: "Welcome to Acme HQ!" (1) in #general with replies 6, 7 and 8.
func TestThreadMessagesReadsTheSeededThread(t *testing.T) {
	ctx := context.Background()
	msgs, err := store.ThreadMessages(ctx, workspace1, 1, 200)
	if err != nil {
		t.Fatal(err)
	}
	// Later seed migrations may add replies, so only the start is fixed.
	if len(msgs) < 4 || !slices.Equal(messageIDs(msgs[:4]), []int64{1, 6, 7, 8}) {
		t.Fatalf("thread = %v, want it to start 1, 6, 7, 8", messageIDs(msgs))
	}
	names := []string{msgs[0].UserName, msgs[1].UserName, msgs[2].UserName, msgs[3].UserName}
	if !slices.Equal(names, []string{"Alice Chen", "Bob Martin", "Alice Chen", "Carol Diaz"}) {
		t.Fatalf("names = %v", names)
	}
	if msgs[0].ThreadRootID != 0 || msgs[1].ThreadRootID != 1 {
		t.Fatalf("thread_root_id = %d, %d; want 0 for the root and 1 for a reply", msgs[0].ThreadRootID, msgs[1].ThreadRootID)
	}
	if !strings.HasPrefix(msgs[0].Body, "Welcome to Acme HQ!") || msgs[1].Body != "Should we pin the onboarding doc here?" {
		t.Fatalf("bodies = %q, %q", msgs[0].Body, msgs[1].Body)
	}
}

func TestThreadMessagesNotFound(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		workspace int64
		root      int64
	}{
		{name: "thread in another workspace", workspace: workspace2, root: 1},
		{name: "no such message", workspace: workspace1, root: 1 << 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs, err := store.ThreadMessages(ctx, tt.workspace, tt.root, 200)
			if err != nil || msgs != nil {
				t.Fatalf("ThreadMessages = %v, %v; want nothing and no error", msgs, err)
			}
		})
	}

	// A reply comes back alone, marked as a reply, so the caller can reject it.
	msgs, err := store.ThreadMessages(ctx, workspace1, 6, 200)
	if err != nil || len(msgs) != 1 || msgs[0].ID != 6 || msgs[0].ThreadRootID != 1 {
		t.Fatalf("ThreadMessages(reply) = %+v, %v", msgs, err)
	}
}

// A long thread comes back as its root and newest replies, oldest first, with
// soft-deleted replies hidden and not counted.
func TestThreadMessagesKeepsTheRootAndNewestReplies(t *testing.T) {
	ctx := context.Background()
	primary := openPrimary(t)
	root := insertMessage(t, primary, workspace1, general, 0, 1, "long thread root")
	all := insertReplies(t, primary, workspace1, general, root, 205)
	replies := all[:len(all)-1] // the newest is soft-deleted
	waitForRows(t, `SELECT COUNT(*) FROM search_thread_messages_v1 WHERE thread_root_id = ?`, []any{root}, 205)

	msgs, err := store.ThreadMessages(ctx, workspace1, root, 200)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]int64{root}, replies[len(replies)-199:]...)
	if !slices.Equal(messageIDs(msgs), want) {
		t.Fatalf("got %d messages %v..., want the root and the newest 199 replies", len(msgs), messageIDs(msgs)[:3])
	}
	if msgs[1].Body != threadPrefix+"reply 6" || msgs[1].UserName != "Carol Diaz" {
		t.Fatalf("oldest kept reply = %+v", msgs[1])
	}

	msgs, err = store.ThreadMessages(ctx, workspace1, root, 1)
	if err != nil || !slices.Equal(messageIDs(msgs), []int64{root}) {
		t.Fatalf("limit 1 = %v, %v; want only the root", messageIDs(msgs), err)
	}
}

// Chat passes the caller's readable channels, so a thread anywhere else must not
// come back, even one in a listed channel ID that belongs to another workspace.
func TestThreadVectorsFilterByWorkspaceAndChannel(t *testing.T) {
	ctx := context.Background()
	primary := openPrimary(t)
	vec := testVector()
	roots := map[string]int64{}
	for _, f := range []struct {
		name      string
		workspace int64
		channel   int64
	}{
		{name: "general", workspace: workspace1, channel: general},
		{name: "engineering", workspace: workspace1, channel: engineering},
		{name: "private", workspace: workspace1, channel: leadership},
		{name: "globex", workspace: workspace2, channel: globexChannel},
	} {
		user := int64(1)
		if f.workspace == workspace2 {
			user = 5
		}
		roots[f.name] = insertMessage(t, primary, f.workspace, f.channel, 0, user, "vector filter "+f.name)
		insertSummary(t, primary, roots[f.name], f.workspace, f.channel, vec)
	}
	waitForRows(t, `SELECT COUNT(*) FROM search_threads_v1 WHERE thread_root_id IN (?, ?, ?, ?)`,
		[]any{roots["general"], roots["engineering"], roots["private"], roots["globex"]}, 4)

	tests := []struct {
		name      string
		workspace int64
		channels  []int64
		want      []string
	}{
		{name: "public channels", workspace: workspace1, channels: []int64{general, engineering}, want: []string{"engineering", "general"}},
		{name: "one channel", workspace: workspace1, channels: []int64{engineering}, want: []string{"engineering"}},
		{name: "private channel listed", workspace: workspace1, channels: []int64{leadership}, want: []string{"private"}},
		{name: "other workspace's channel listed", workspace: workspace1, channels: []int64{general, globexChannel}, want: []string{"general"}},
		{name: "workspace 2", workspace: workspace2, channels: []int64{general, globexChannel}, want: []string{"globex"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vectors, err := store.ThreadVectors(ctx, tt.workspace, tt.channels)
			if err != nil {
				t.Fatal(err)
			}
			// Other rows, such as real summaries, may exist, so only fixtures are checked.
			var got []string
			for _, v := range vectors {
				for name, id := range roots {
					if v.ThreadRootID == id {
						got = append(got, name)
					}
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("fixtures returned = %v, want %v", got, tt.want)
			}
		})
	}
}
