//go:build integration

package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests need the Compose databases with the demo seed: `make db` and `make api`
// once. Rows are written to the primary and read back through the replica, like
// production. "kestrel" appears only in these rows, so results can be checked exactly.
const (
	workspace1    = 1
	workspace2    = 2
	general       = 2 // public, workspace 1
	engineering   = 3 // public, workspace 1
	globexChannel = 4 // public, workspace 2
	leadership    = 6 // private, workspace 1
	bodyPrefix    = "t04-it "
)

type fixture struct {
	name      string
	workspace int64
	channel   int64
	body      string
	deleted   bool
}

var fixtures = []fixture{
	{name: "howto", workspace: workspace1, channel: general, body: "how to deploy the kestrel gateway"},
	{name: "deployed", workspace: workspace1, channel: general, body: "kestrel deployed with Go services"},
	{name: "rollback", workspace: workspace1, channel: engineering, body: "kestrel rollback plan"},
	{name: "snake", workspace: workspace1, channel: general, body: "kestrel snake_case helper"},
	{name: "globex", workspace: workspace2, channel: globexChannel, body: "how to deploy the kestrel gateway"},
	{name: "private", workspace: workspace1, channel: leadership, body: "kestrel boardroom details"},
	{name: "tombstone", workspace: workspace1, channel: general, body: "kestrel tombstoned", deleted: true},
}

var (
	replica *sql.DB
	store   *Store
	names   = map[int64]string{} // fixture name by message ID
)

func TestMain(m *testing.M) {
	code, err := runWithFixtures(m)
	if err != nil {
		log.Fatal(err)
	}
	os.Exit(code)
}

func runWithFixtures(m *testing.M) (int, error) {
	ctx := context.Background()
	primary, err := sql.Open("mysql", env("SEARCH_TEST_PRIMARY_DSN", "mnchat:mnchat@tcp(127.0.0.1:3306)/mnchat?parseTime=true&loc=UTC"))
	if err != nil {
		return 0, err
	}
	defer primary.Close()
	replica, err = Open(ctx, env("SEARCH_MYSQL_DSN", "search_reader:search_reader@tcp(127.0.0.1:3307)/mnchat?parseTime=true&loc=UTC"))
	if err != nil {
		return 0, err
	}
	defer replica.Close()
	store = New(replica)

	// Rows left by a killed run would otherwise break the exact checks below.
	if _, err := primary.ExecContext(ctx, `DELETE FROM messages WHERE body LIKE ?`, bodyPrefix+"%"); err != nil {
		return 0, fmt.Errorf("remove stale fixtures: %w", err)
	}
	defer func() {
		ids := slices.Collect(maps.Keys(names))
		if len(ids) == 0 {
			return
		}
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		list := placeholders(len(ids))
		if _, err := primary.ExecContext(ctx, `DELETE FROM messages WHERE id IN (`+list+`)`, args...); err != nil {
			log.Printf("remove fixtures: %v", err)
			return
		}
		if err := waitForReplica(ctx, list, args, 0); err != nil {
			log.Printf("fixture deletion did not replicate: %v", err)
		}
	}()

	for _, f := range fixtures {
		stmt := `INSERT INTO messages (workspace_id, channel_id, user_id, body) VALUES (?, ?, 1, ?)`
		if f.deleted {
			stmt = `INSERT INTO messages (workspace_id, channel_id, user_id, body, deleted_at) VALUES (?, ?, 1, ?, NOW(3))`
		}
		res, err := primary.ExecContext(ctx, stmt, f.workspace, f.channel, bodyPrefix+f.body)
		if err != nil {
			return 0, fmt.Errorf("insert %s: %w", f.name, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		names[id] = f.name
	}

	// The view hides the deleted row, so one fixture stays out of this count.
	ids := slices.Collect(maps.Keys(names))
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if err := waitForReplica(ctx, placeholders(len(ids)), args, len(ids)-1); err != nil {
		return 0, fmt.Errorf("fixtures did not replicate: %w", err)
	}
	return m.Run(), nil
}

func waitForReplica(ctx context.Context, list string, ids []any, want int) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		var got int
		err := replica.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_messages_v1 WHERE id IN (`+list+`)`, ids...).Scan(&got)
		if err == nil && got == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("replica has %d rows, want %d: %v", got, want, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func search(t *testing.T, workspaceID int64, channelIDs []int64, text string) []string {
	t.Helper()
	hits, err := store.SearchMessages(context.Background(), workspaceID, channelIDs, text, 50)
	if err != nil {
		t.Fatalf("SearchMessages(%q): %v", text, err)
	}
	return hitNames(hits)
}

// hitNames names fixture hits and keeps other rows as their IDs, sorted for comparison.
func hitNames(hits []MessageHit) []string {
	got := make([]string, 0, len(hits))
	for _, h := range hits {
		name, ok := names[h.ID]
		if !ok {
			name = strconv.FormatInt(h.ID, 10)
		}
		got = append(got, name)
	}
	slices.Sort(got)
	return got
}

var publicChannels = []int64{1, general, engineering}

func TestFindsText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "word", text: "rollback", want: []string{"rollback"}},
		{name: "case does not matter", text: "ROLLBACK", want: []string{"rollback"}},
		{name: "prefix", text: "roll", want: []string{"rollback"}},
		{name: "both words required", text: "kestrel rollback", want: []string{"rollback"}},
		{name: "second word missing from every row", text: "kestrel zebrafish", want: []string{}},
		{name: "words in different rows", text: "rollback gateway", want: []string{}},
		{name: "underscore word", text: "snake_case", want: []string{"snake"}},
		{name: "word over the index limit stays required", text: "kestrel " + strings.Repeat("b", 85), want: []string{}},
		{name: "soft-deleted rows stay hidden", text: "tombstoned", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := search(t, workspace1, publicChannels, tt.text); !slices.Equal(got, tt.want) {
				t.Fatalf("search(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// "go" needs --innodb-ft-min-token-size=2, and "deploy" must match "deployed". Seed
// rows may match as well, so these check only the fixtures.
func TestFindsShortWordsAndPrefixes(t *testing.T) {
	if got := search(t, workspace1, publicChannels, "go"); !slices.Contains(got, "deployed") {
		t.Errorf("search(go) = %v, want the row saying Go", got)
	}
	got := search(t, workspace1, publicChannels, "deploy")
	if !slices.Contains(got, "deployed") || !slices.Contains(got, "howto") {
		t.Errorf("search(deploy) = %v, want both deploy and deployed", got)
	}
}

// This is the MySQL behaviour booleanQuery works around. If it fails, the stopword
// handling is no longer needed.
func TestRequiredStopwordEmptiesARawQuery(t *testing.T) {
	var got int
	err := replica.QueryRow(`SELECT COUNT(*) FROM search_messages_v1
		WHERE MATCH(body) AGAINST('+how* +to* +kestrel*' IN BOOLEAN MODE)`).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("raw query found %d rows, want 0", got)
	}
}

func TestStopwords(t *testing.T) {
	tests := []struct {
		text string
		want []string
	}{
		{text: "how to deploy kestrel", want: []string{"deployed", "howto"}},
		{text: "how to deploy the kestrel gateway", want: []string{"howto"}},
		{text: "HOW TO DEPLOY THE KESTREL GATEWAY", want: []string{"howto"}},
		{text: "what is the kestrel plan", want: []string{"rollback"}},
		{text: "how to", want: []string{}},
		{text: "the", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			if got := search(t, workspace1, publicChannels, tt.text); !slices.Equal(got, tt.want) {
				t.Fatalf("search(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestEveryStopwordIsSafeInAQuery(t *testing.T) {
	want := []string{"rollback"}
	for word := range stopwords {
		text := word + " kestrel rollback"
		if got := search(t, workspace1, publicChannels, text); !slices.Equal(got, want) {
			t.Errorf("search(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestStopwordListMatchesServer(t *testing.T) {
	root, err := sql.Open("mysql", env("SEARCH_TEST_ROOT_DSN", "root:root@tcp(127.0.0.1:3306)/"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	rows, err := root.Query(`SELECT value FROM information_schema.INNODB_FT_DEFAULT_STOPWORD`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	server := map[string]struct{}{}
	for rows.Next() {
		var word string
		if err := rows.Scan(&word); err != nil {
			t.Fatal(err)
		}
		server[word] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(server, stopwords) {
		t.Fatalf("server stopwords = %v, list here = %v", slices.Sorted(maps.Keys(server)), slices.Sorted(maps.Keys(stopwords)))
	}
}

// Every case would behave differently, or fail, if the operator reached MySQL.
func TestBooleanOperatorsHaveNoEffect(t *testing.T) {
	all := []string{"deployed", "howto", "rollback", "snake"}
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "exclude keeps the word required", text: "kestrel -deploy", want: []string{"deployed", "howto"}},
		{name: "phrase out of order still matches", text: `"rollback kestrel"`, want: []string{"rollback"}},
		{name: "unbalanced quote", text: `"kestrel rollback`, want: []string{"rollback"}},
		{name: "distance", text: `"kestrel rollback"@3`, want: []string{"rollback"}},
		{name: "tilde stays required", text: "kestrel ~rollback", want: []string{"rollback"}},
		{name: "ranking", text: ">kestrel <rollback", want: []string{"rollback"}},
		{name: "group", text: "(kestrel rollback)", want: []string{"rollback"}},
		{name: "unbalanced group", text: "((kestrel", want: all},
		{name: "stray close", text: "kestrel))", want: all},
		{name: "doubled operators", text: "+-kestrel --rollback ~~", want: []string{"rollback"}},
		{name: "wildcard everywhere", text: "*kestrel* *rollback*", want: []string{"rollback"}},
		{name: "every operator between words", text: `kestrel +-><()~*"@ rollback`, want: []string{"rollback"}},
		{name: "every operator alone", text: `+-><()~*"@`, want: []string{}},
		{name: "underscore is no wildcard", text: "kes_rel", want: []string{}},
		{name: "percent is no wildcard", text: "kes%", want: all},
		{name: "backslash", text: `kestrel\ rollback`, want: []string{"rollback"}},
		{name: "quote", text: `kestrel' rollback`, want: []string{"rollback"}},
		{name: "nul byte", text: "kestrel\x00rollback", want: []string{"rollback"}},
		{name: "sql", text: `kestrel'; DROP TABLE messages; --`, want: []string{}},
		{name: "union", text: `kestrel' UNION SELECT 1,1 -- `, want: []string{}},
		{name: "non-latin text", text: "配備", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := search(t, workspace1, publicChannels, tt.text); !slices.Equal(got, tt.want) {
				t.Fatalf("search(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}

	// The search above still sees the table.
	if got := search(t, workspace1, publicChannels, "rollback"); !slices.Equal(got, []string{"rollback"}) {
		t.Fatalf("after the injection attempts, search = %v", got)
	}
}

func TestWorkspaceIsolation(t *testing.T) {
	tests := []struct {
		name      string
		workspace int64
		channels  []int64
		want      []string
	}{
		{name: "same text, workspace 1", workspace: workspace1, channels: publicChannels, want: []string{"deployed", "howto"}},
		{name: "same text, workspace 2", workspace: workspace2, channels: []int64{globexChannel}, want: []string{"globex"}},
		{name: "workspace 1 asking for a workspace 2 channel", workspace: workspace1, channels: []int64{general, globexChannel}, want: []string{"deployed", "howto"}},
		{name: "workspace 2 asking for a workspace 1 channel", workspace: workspace2, channels: []int64{general, globexChannel}, want: []string{"globex"}},
		{name: "only the other workspace's channel", workspace: workspace1, channels: []int64{globexChannel}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := search(t, tt.workspace, tt.channels, "kestrel deploy"); !slices.Equal(got, tt.want) {
				t.Fatalf("search = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChannelFilter(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		channels []int64
		want     []string
	}{
		{name: "private channel not listed", text: "boardroom", channels: publicChannels, want: []string{}},
		{name: "private channel listed", text: "boardroom", channels: []int64{leadership}, want: []string{"private"}},
		{name: "seeded private messages not listed", text: "reorganization", channels: publicChannels, want: []string{}},
		{name: "seeded private messages listed", text: "reorganization", channels: []int64{leadership}, want: []string{"9", "10"}},
		{name: "other public channel not listed", text: "kestrel rollback", channels: []int64{general}, want: []string{}},
		{name: "no channels", text: "kestrel", channels: nil, want: []string{}},
		{name: "empty channels", text: "kestrel", channels: []int64{}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := slices.Sorted(slices.Values(tt.want))
			if got := search(t, workspace1, tt.channels, tt.text); !slices.Equal(got, want) {
				t.Fatalf("search = %v, want %v", got, want)
			}
		})
	}
}

func TestResultsAreRankedAndLimited(t *testing.T) {
	ctx := context.Background()
	channels := []int64{general, engineering, leadership}

	hits, err := store.SearchMessages(ctx, workspace1, channels, "kestrel", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 5 {
		t.Fatalf("got %d hits, want 5: %v", len(hits), hits)
	}
	for i, h := range hits {
		if h.Score <= 0 {
			t.Errorf("hit %d has score %v, want it positive", h.ID, h.Score)
		}
		if i == 0 {
			continue
		}
		prev := hits[i-1]
		if h.Score > prev.Score || (h.Score == prev.Score && h.ID > prev.ID) {
			t.Errorf("hit %+v is out of order after %+v", h, prev)
		}
	}

	limited, err := store.SearchMessages(ctx, workspace1, channels, "kestrel", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(limited, hits[:2]) {
		t.Fatalf("limited = %v, want the first two of %v", limited, hits)
	}
}
