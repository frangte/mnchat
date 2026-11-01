// Package server implements the SearchService gRPC API. Chat owns permissions, so
// requests carry the channels a user may read and responses carry only IDs.
package server

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/search/store"
)

const (
	defaultMessageLimit = 20
	maxMessageLimit     = 50

	// messageSearchTimeout covers callers with no deadline of their own. Chat's
	// 800 ms deadline is shorter and applies first.
	messageSearchTimeout = 2 * time.Second
)

type Store interface {
	SearchMessages(ctx context.Context, workspaceID int64, channelIDs []int64, text string, limit int) ([]store.MessageHit, error)
}

// Server implements searchv1.SearchServiceServer. RPCs it doesn't define yet
// return UNIMPLEMENTED through the embedded type.
type Server struct {
	searchv1.UnimplementedSearchServiceServer
	store                Store
	messageSearchTimeout time.Duration
}

func New(s Store) *Server {
	return &Server{store: s, messageSearchTimeout: messageSearchTimeout}
}

func (s *Server) SearchMessages(ctx context.Context, req *searchv1.SearchMessagesRequest) (*searchv1.SearchMessagesResponse, error) {
	if req.GetWorkspaceId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "workspace_id must be positive")
	}
	text := strings.TrimSpace(req.GetQuery())
	if text == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	// Chat sends the caller's readable channels, so none means nothing to search.
	if len(req.GetChannelIds()) == 0 {
		return &searchv1.SearchMessagesResponse{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.messageSearchTimeout)
	defer cancel()
	hits, err := s.store.SearchMessages(ctx, req.GetWorkspaceId(), req.GetChannelIds(), text,
		clampLimit(req.GetLimit(), defaultMessageLimit, maxMessageLimit))
	if err != nil {
		return nil, storeError(ctx, "SearchMessages", err)
	}

	resp := &searchv1.SearchMessagesResponse{Hits: make([]*searchv1.MessageHit, 0, len(hits))}
	for _, h := range hits {
		resp.Hits = append(resp.Hits, &searchv1.MessageHit{MessageId: h.ID, Score: h.Score})
	}
	return resp, nil
}

func clampLimit(limit int32, fallback, maxLimit int) int {
	switch {
	case limit <= 0:
		return fallback
	case int(limit) > maxLimit:
		return maxLimit
	default:
		return int(limit)
	}
}

// storeError logs the cause and hides it from callers. An expired or cancelled
// ctx decides the code, because the driver can report one as a broken connection.
func storeError(ctx context.Context, rpc string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		slog.Warn("store call stopped", "rpc", rpc, "err", ctxErr)
		return status.FromContextError(ctxErr).Err()
	}
	slog.Error("store call failed", "rpc", rpc, "err", err)
	return status.Error(codes.Unavailable, "search store unavailable")
}
