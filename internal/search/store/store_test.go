package store

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBooleanQuery(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "one word", in: "deploy", want: "+deploy*"},
		{name: "two words both required", in: "release planning", want: "+release* +planning*"},
		{name: "minimum token size", in: "go", want: "+go*"},
		{name: "lowercased", in: "Deploy GO", want: "+deploy* +go*"},
		{name: "digits", in: "10:00 2fa", want: "+10* +00* +2fa*"},

		{name: "stopwords dropped", in: "how to deploy", want: "+deploy*"},
		{name: "stopwords dropped in any case", in: "HOW To Deploy", want: "+deploy*"},
		{name: "only stopwords", in: "how to", want: ""},
		{name: "stopwords in a host name", in: "www.example.com", want: "+example*"},

		{name: "single characters dropped", in: "a b deploy c", want: "+deploy*"},
		{name: "only single characters", in: "x y z", want: ""},
		{name: "multibyte token counts characters", in: "é", want: ""},

		{name: "duplicates collapse", in: "deploy Deploy deploy", want: "+deploy*"},
		{name: "keeps eight terms", in: "aa bb cc dd ee ff gg hh ii jj", want: "+aa* +bb* +cc* +dd* +ee* +ff* +gg* +hh*"},
		{name: "stopwords and duplicates use no term slots", in: "the aa aa to bb in cc on dd at ee of ff by gg as hh ii", want: "+aa* +bb* +cc* +dd* +ee* +ff* +gg* +hh*"},

		{name: "underscore stays in the word", in: "snake_case", want: "+snake_case*"},
		{name: "apostrophe splits", in: "don't", want: "+don*"},

		{name: "longest indexable word", in: strings.Repeat("a", 84), want: "+" + strings.Repeat("a", 84) + "*"},
		{name: "unindexable word means no match", in: "deploy " + strings.Repeat("a", 85), want: ""},

		{name: "required operator", in: "+go", want: "+go*"},
		{name: "exclude operator", in: "go -deploy", want: "+go* +deploy*"},
		{name: "quoted phrase", in: `"go deploy"`, want: "+go* +deploy*"},
		{name: "unbalanced quote", in: `"go`, want: "+go*"},
		{name: "wildcard", in: "dep*loy*", want: "+dep* +loy*"},
		{name: "grouping and ranking", in: "(>go <deploy) ~rollback", want: "+go* +deploy* +rollback*"},
		{name: "distance operator", in: `"go deploy"@4`, want: "+go* +deploy*"},
		{name: "only operators", in: `+-><()~*"@`, want: ""},
		{name: "percent and backslash", in: `100% \go`, want: "+100* +go*"},
		{name: "sql", in: `'; DROP TABLE messages; --`, want: "+drop* +table* +messages*"},
		{name: "empty", in: "", want: ""},
		{name: "invalid utf-8", in: "go\xff\xfedeploy", want: "+go* +deploy*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := booleanQuery(tt.in); got != tt.want {
				t.Fatalf("booleanQuery(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Whatever the user types, a term must stay out of the boolean operators.
func FuzzBooleanQueryOnlyEmitsSafeTerms(f *testing.F) {
	f.Add(`+-><()~*"@`)
	f.Add(`'; DROP TABLE messages; --`)
	f.Fuzz(func(t *testing.T, in string) {
		query := booleanQuery(in)
		if query == "" {
			return
		}
		terms := strings.Split(query, " ")
		if len(terms) > maxQueryTerms {
			t.Fatalf("booleanQuery(%q) has %d terms", in, len(terms))
		}
		for _, term := range terms {
			if !strings.HasPrefix(term, "+") || !strings.HasSuffix(term, "*") {
				t.Fatalf("booleanQuery(%q) has malformed term %q", in, term)
			}
			inner := strings.TrimSuffix(strings.TrimPrefix(term, "+"), "*")
			if strings.ContainsFunc(inner, isSeparator) {
				t.Fatalf("booleanQuery(%q) has unsafe term %q", in, term)
			}
		}
	})
}

func TestMessageSearchBindsEveryPlaceholderInOrder(t *testing.T) {
	stmt, args := messageSearch(7, []int64{1, 2, 3}, "+deploy*", 20)

	if got := strings.Count(stmt, "?"); got != len(args) {
		t.Fatalf("statement has %d placeholders, %d args", got, len(args))
	}
	if got := strings.Count(stmt, "AGAINST(? IN BOOLEAN MODE)"); got != 2 {
		t.Fatalf("statement has %d AGAINST clauses, want 2", got)
	}
	want := []any{"+deploy*", int64(7), int64(1), int64(2), int64(3), "+deploy*", 20}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if !strings.Contains(stmt, "channel_id IN (?,?,?)") {
		t.Fatalf("statement has the wrong channel list: %s", stmt)
	}
}

func TestMessageSearchKeepsUserTextOutOfTheStatement(t *testing.T) {
	const marker = "+marker*"
	stmt, _ := messageSearch(1, []int64{1}, marker, 1)
	if strings.Contains(stmt, marker) {
		t.Fatalf("statement contains the query text: %s", stmt)
	}
}

func TestOpenFailsOnlyOnMalformedDSN(t *testing.T) {
	if _, err := Open(context.Background(), "not a dsn"); err == nil {
		t.Fatal("Open with a malformed DSN returned nil error")
	}

	// Nothing listens on the port once the listener has closed.
	addr := closedAddr(t)
	db, err := Open(context.Background(), "u:p@tcp("+addr+")/mnchat")
	if err != nil {
		t.Fatalf("Open with an unreachable replica: %v", err)
	}
	defer db.Close()
	if got := db.Stats().MaxOpenConnections; got != maxOpenConns {
		t.Fatalf("MaxOpenConnections = %d, want %d", got, maxOpenConns)
	}
}

func TestSearchMessagesSkipsTheDatabaseWhenNothingCanMatch(t *testing.T) {
	db, err := Open(context.Background(), "u:p@tcp("+closedAddr(t)+")/mnchat")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)

	tests := []struct {
		name     string
		channels []int64
		text     string
	}{
		{name: "no channels", text: "deploy"},
		{name: "nothing searchable", channels: []int64{1}, text: "how to"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits, err := s.SearchMessages(context.Background(), 1, tt.channels, tt.text, 20)
			if err != nil || len(hits) != 0 {
				t.Fatalf("SearchMessages = %v, %v; want no hits and no error", hits, err)
			}
		})
	}
}

func TestSearchMessagesReportsAnUnreachableReplica(t *testing.T) {
	db, err := Open(context.Background(), "u:p@tcp("+closedAddr(t)+")/mnchat")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := New(db).SearchMessages(context.Background(), 1, []int64{1}, "deploy", 20); err == nil {
		t.Fatal("SearchMessages against a closed port returned nil error")
	}
}

// A silent peer stands in for a dead replica: it accepts the connection and never
// sends the MySQL handshake. The request must end at its deadline.
func TestSearchMessagesStopsAtTheDeadlineWhenTheReplicaHangs(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Open's probe shouldn't spend its full timeout on the silent peer
	db, err := Open(ctx, "u:p@tcp("+lis.Addr().String()+")/mnchat")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = New(db).SearchMessages(ctx, 1, []int64{1}, "deploy", 20)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %v, want about the 200ms deadline", elapsed)
	}
}

func closedAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	if err := lis.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}
