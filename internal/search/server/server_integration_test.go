//go:build integration

package server

import (
	"context"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/search/store"
)

// These tests read seed data only, so they can run alongside the store tests.
const (
	engineeringChannel = 3
	leadershipChannel  = 6
	releasePlanningID  = 4 // "Reminder: release planning tomorrow at 10:00."
)

func replicaAddr() string {
	if v := os.Getenv("SEARCH_TEST_REPLICA_ADDR"); v != "" {
		return v
	}
	return "127.0.0.1:3307"
}

func dsnFor(addr string) string {
	return "search_reader:search_reader@tcp(" + addr + ")/mnchat?parseTime=true&loc=UTC"
}

func startAgainst(t *testing.T, addr string) searchv1.SearchServiceClient {
	t.Helper()
	db, err := store.Open(context.Background(), dsnFor(addr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	conn, _, _ := startGRPC(t, newServer(store.New(db)))
	return searchv1.NewSearchServiceClient(conn)
}

func hitIDs(resp *searchv1.SearchMessagesResponse) []int64 {
	ids := make([]int64, 0, len(resp.GetHits()))
	for _, h := range resp.GetHits() {
		ids = append(ids, h.GetMessageId())
	}
	slices.Sort(ids)
	return ids
}

func TestSearchMessagesOverGRPC(t *testing.T) {
	client := startAgainst(t, replicaAddr())
	tests := []struct {
		name     string
		channels []int64
		query    string
		want     []int64
	}{
		{name: "two words", channels: []int64{engineeringChannel}, query: "release planning", want: []int64{releasePlanningID}},
		{name: "stopwords in the query", channels: []int64{engineeringChannel}, query: "what is the release planning", want: []int64{releasePlanningID}},
		{name: "operators in the query", channels: []int64{engineeringChannel}, query: `+release -planning (x)`, want: []int64{releasePlanningID}},
		{name: "private channel listed", channels: []int64{leadershipChannel}, query: "reorganization", want: []int64{9, 10}},
		{name: "private channel not listed", channels: []int64{engineeringChannel}, query: "reorganization", want: []int64{}},
		{name: "no channels", query: "reorganization", want: []int64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.SearchMessages(context.Background(), &searchv1.SearchMessagesRequest{
				WorkspaceId: 1, ChannelIds: tt.channels, Query: tt.query,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := hitIDs(resp); !slices.Equal(got, tt.want) {
				t.Fatalf("hits = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnreachableReplica(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	lis.Close()

	db, err := store.Open(context.Background(), dsnFor(addr))
	if err != nil {
		t.Fatalf("startup must survive a dead replica: %v", err)
	}
	defer db.Close()
	conn, _, _ := startGRPC(t, newServer(store.New(db)))
	client := searchv1.NewSearchServiceClient(conn)
	ctx := context.Background()

	_, err = client.SearchMessages(ctx, &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: "deploy"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v, want UNAVAILABLE", err)
	}

	// Answers that need no data still work, and the process stays healthy.
	if _, err := client.SearchMessages(ctx, &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{1}, Query: "how to"}); err != nil {
		t.Fatalf("query with no searchable words: %v", err)
	}
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("Check = %v, %v; want SERVING", resp, err)
	}
}

// outageProxy sits in front of the shared replica, so an outage can be simulated
// without stopping it. While down it drops every connection.
type outageProxy struct {
	lis    net.Listener
	target string

	mu    sync.Mutex
	down  bool
	conns map[net.Conn]struct{}
}

func startOutageProxy(t *testing.T, target string) *outageProxy {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &outageProxy{lis: lis, target: target, conns: map[net.Conn]struct{}{}}
	t.Cleanup(func() {
		lis.Close()
		p.setDown(true)
	})
	go p.serve()
	return p
}

func (p *outageProxy) serve() {
	for {
		client, err := p.lis.Accept()
		if err != nil {
			return
		}
		upstream, err := net.Dial("tcp", p.target)
		if err != nil || !p.track(client, upstream) {
			client.Close()
			if upstream != nil {
				upstream.Close()
			}
			continue
		}
		go pipe(client, upstream)
		go pipe(upstream, client)
	}
}

func pipe(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	dst.Close()
	src.Close()
}

func (p *outageProxy) track(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return false
	}
	for _, c := range conns {
		p.conns[c] = struct{}{}
	}
	return true
}

func (p *outageProxy) setDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
	if down {
		for c := range p.conns {
			c.Close()
		}
		clear(p.conns)
	}
}

// A replica restart breaks pooled connections. search must report the outage and
// then recover by itself, with no restart.
func TestRecoversWhenReplicaReturns(t *testing.T) {
	proxy := startOutageProxy(t, replicaAddr())
	client := startAgainst(t, proxy.lis.Addr().String())
	req := &searchv1.SearchMessagesRequest{WorkspaceId: 1, ChannelIds: []int64{engineeringChannel}, Query: "release"}
	ctx := context.Background()

	if _, err := client.SearchMessages(ctx, req); err != nil {
		t.Fatalf("before the outage: %v", err)
	}

	proxy.setDown(true)
	if _, err := client.SearchMessages(ctx, req); status.Code(err) != codes.Unavailable {
		t.Fatalf("during the outage: err = %v, want UNAVAILABLE", err)
	}

	proxy.setDown(false)
	resp, err := client.SearchMessages(ctx, req)
	if err != nil {
		t.Fatalf("after the outage: %v", err)
	}
	if got := hitIDs(resp); !slices.Contains(got, releasePlanningID) {
		t.Fatalf("after the outage: hits = %v, want message %d", got, releasePlanningID)
	}
}
