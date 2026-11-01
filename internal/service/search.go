package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/domain"
)

const (
	maxSearchQueryRunes = 200
	defaultSearchLimit  = 20
	maxSearchLimit      = 50
)

// SearchClient calls the search service. It returns domain errors, so a dead search
// reaches the handler as ErrUnavailable.
type SearchClient interface {
	SearchMessages(ctx context.Context, req *searchv1.SearchMessagesRequest) (*searchv1.SearchMessagesResponse, error)
}

// MessageLoader loads search hits from the primary.
type MessageLoader interface {
	GetMany(ctx context.Context, workspaceID int64, ids []int64) ([]domain.Message, error)
}

// MessageHit is one search result.
type MessageHit struct {
	Message domain.Message `json:"message"`
	Score   float64        `json:"score"`
}

// SearchService keeps permissions in chat. It tells search which channels the user can
// read, then checks every hit again, so search needs no copy of the rules.
type SearchService struct {
	workspaces WorkspaceStore
	channels   ChannelStore
	messages   MessageLoader
	client     SearchClient
}

func NewSearchService(w WorkspaceStore, c ChannelStore, m MessageLoader, client SearchClient) *SearchService {
	return &SearchService{workspaces: w, channels: c, messages: m, client: client}
}

// Messages runs full-text search and returns hits in search's order.
func (s *SearchService) Messages(ctx context.Context, workspaceID, userID int64, rawQuery string, limit int) ([]MessageHit, error) {
	if _, err := s.workspaces.MemberRole(ctx, workspaceID, userID); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(rawQuery)
	if query == "" || utf8.RuneCountInString(query) > maxSearchQueryRunes {
		return nil, fmt.Errorf("%w: q must be 1-%d characters", domain.ErrInvalid, maxSearchQueryRunes)
	}
	channels, err := s.channels.ListVisible(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return []MessageHit{}, nil // an empty list must never reach search
	}
	channelIDs := make([]int64, 0, len(channels))
	readable := make(map[int64]bool, len(channels))
	for _, ch := range channels {
		channelIDs = append(channelIDs, ch.ID)
		readable[ch.ID] = true
	}

	limit = searchLimit(limit)
	res, err := s.client.SearchMessages(ctx, &searchv1.SearchMessagesRequest{
		WorkspaceId: workspaceID,
		ChannelIds:  channelIDs,
		Query:       query,
		Limit:       int32(limit),
	})
	if err != nil {
		return nil, err
	}

	// Search is another service, so its response can't size this query or decide access.
	hits := res.GetHits()
	hits = hits[:min(len(hits), limit)]
	ids := make([]int64, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.GetMessageId())
	}
	messages, err := s.messages.GetMany(ctx, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]domain.Message, len(messages))
	for _, m := range messages {
		byID[m.ID] = m
	}

	out := make([]MessageHit, 0, len(hits))
	for _, hit := range hits {
		m, ok := byID[hit.GetMessageId()]
		if !ok || !readable[m.ChannelID] {
			continue
		}
		out = append(out, MessageHit{Message: m, Score: hit.GetScore()})
	}
	return out, nil
}

func searchLimit(limit int) int {
	if limit <= 0 {
		return defaultSearchLimit
	}
	return min(limit, maxSearchLimit)
}
