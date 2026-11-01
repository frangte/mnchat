package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/domain"
)

const (
	maxSearchQueryRunes = 200
	defaultSearchLimit  = 20
	maxSearchLimit      = 50
	defaultThreadLimit  = 5
	maxThreadLimit      = 20
)

// SearchClient calls the search service. It returns domain errors, so a dead search
// reaches the handler as ErrUnavailable.
type SearchClient interface {
	SearchMessages(ctx context.Context, req *searchv1.SearchMessagesRequest) (*searchv1.SearchMessagesResponse, error)
	SearchThreads(ctx context.Context, req *searchv1.SearchThreadsRequest) (*searchv1.SearchThreadsResponse, error)
}

// MessageLoader loads search hits from the primary.
type MessageLoader interface {
	GetMany(ctx context.Context, workspaceID int64, ids []int64) ([]domain.Message, error)
	// LatestReplyIDs maps each root to its newest live reply, leaving out roots with none.
	LatestReplyIDs(ctx context.Context, workspaceID int64, rootIDs []int64) (map[int64]int64, error)
}

// SummaryLoader loads stored summaries from the primary.
type SummaryLoader interface {
	GetMany(ctx context.Context, workspaceID int64, rootIDs []int64) ([]domain.ThreadSummary, error)
}

// MessageHit is one search result.
type MessageHit struct {
	Message domain.Message `json:"message"`
	Score   float64        `json:"score"`
}

// ThreadHit is one thread found by meaning.
type ThreadHit struct {
	Root    domain.Message `json:"root"`
	Summary string         `json:"summary"`
	Stale   bool           `json:"stale"`
	Score   float64        `json:"score"`
}

// SearchService keeps permissions in chat. It tells search which channels the user can
// read, then checks every hit again, so search needs no copy of the rules.
type SearchService struct {
	workspaces WorkspaceStore
	channels   ChannelStore
	messages   MessageLoader
	summaries  SummaryLoader
	client     SearchClient
}

func NewSearchService(w WorkspaceStore, c ChannelStore, m MessageLoader, s SummaryLoader, client SearchClient) *SearchService {
	return &SearchService{workspaces: w, channels: c, messages: m, summaries: s, client: client}
}

// searchScope is what both modes check before calling search.
type searchScope struct {
	query      string
	channelIDs []int64
	readable   map[int64]bool
}

// scope checks membership and the query, then lists the channels the caller can
// read. An empty channelIDs means there is nothing to search.
func (s *SearchService) scope(ctx context.Context, workspaceID, userID int64, rawQuery string) (searchScope, error) {
	if _, err := s.workspaces.MemberRole(ctx, workspaceID, userID); err != nil {
		return searchScope{}, err
	}
	query := strings.TrimSpace(rawQuery)
	if query == "" || utf8.RuneCountInString(query) > maxSearchQueryRunes {
		return searchScope{}, fmt.Errorf("%w: q must be 1-%d characters", domain.ErrInvalid, maxSearchQueryRunes)
	}
	channels, err := s.channels.ListVisible(ctx, workspaceID, userID)
	if err != nil {
		return searchScope{}, err
	}
	sc := searchScope{
		query:      query,
		channelIDs: make([]int64, 0, len(channels)),
		readable:   make(map[int64]bool, len(channels)),
	}
	for _, ch := range channels {
		sc.channelIDs = append(sc.channelIDs, ch.ID)
		sc.readable[ch.ID] = true
	}
	return sc, nil
}

// Messages runs full-text search and returns hits in search's order.
func (s *SearchService) Messages(ctx context.Context, workspaceID, userID int64, rawQuery string, limit int) ([]MessageHit, error) {
	sc, err := s.scope(ctx, workspaceID, userID, rawQuery)
	if err != nil {
		return nil, err
	}
	if len(sc.channelIDs) == 0 {
		return []MessageHit{}, nil // an empty list must never reach search
	}

	limit = clampLimit(limit, defaultSearchLimit, maxSearchLimit)
	res, err := s.client.SearchMessages(ctx, &searchv1.SearchMessagesRequest{
		WorkspaceId: workspaceID,
		ChannelIds:  sc.channelIDs,
		Query:       sc.query,
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
		if !ok || !sc.readable[m.ChannelID] {
			continue
		}
		out = append(out, MessageHit{Message: m, Score: hit.GetScore()})
	}
	return out, nil
}

// Threads ranks summarized threads by meaning and returns them in search's order.
// Only threads with a stored summary can match, since search ranks the summaries.
func (s *SearchService) Threads(ctx context.Context, workspaceID, userID int64, rawQuery string, limit int) ([]ThreadHit, error) {
	sc, err := s.scope(ctx, workspaceID, userID, rawQuery)
	if err != nil {
		return nil, err
	}
	if len(sc.channelIDs) == 0 {
		return []ThreadHit{}, nil // an empty list must never reach search
	}

	limit = clampLimit(limit, defaultThreadLimit, maxThreadLimit)
	res, err := s.client.SearchThreads(ctx, &searchv1.SearchThreadsRequest{
		WorkspaceId: workspaceID,
		ChannelIds:  sc.channelIDs,
		Query:       sc.query,
		Limit:       int32(limit),
	})
	if err != nil {
		return nil, err
	}

	// Search is another service, so its response can't size these queries or decide
	// access. Roots come from the primary, and only readable ones go any further.
	hits := res.GetHits()
	hits = hits[:min(len(hits), limit)]
	ids := make([]int64, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.GetThreadRootId())
	}
	messages, err := s.messages.GetMany(ctx, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	roots := make(map[int64]domain.Message, len(messages))
	for _, m := range messages {
		if sc.readable[m.ChannelID] && m.ThreadRootID == nil {
			roots[m.ID] = m
		}
	}
	if len(roots) == 0 {
		return []ThreadHit{}, nil
	}

	readableIDs := make([]int64, 0, len(roots))
	for _, id := range ids {
		if _, ok := roots[id]; ok && !slices.Contains(readableIDs, id) {
			readableIDs = append(readableIDs, id)
		}
	}
	stored, err := s.summaries.GetMany(ctx, workspaceID, readableIDs)
	if err != nil {
		return nil, err
	}
	summaries := make(map[int64]domain.ThreadSummary, len(stored))
	for _, sum := range stored {
		summaries[sum.ThreadRootID] = sum
	}
	latest, err := s.messages.LatestReplyIDs(ctx, workspaceID, readableIDs)
	if err != nil {
		return nil, err
	}

	out := make([]ThreadHit, 0, len(hits))
	seen := make(map[int64]bool, len(hits))
	for _, hit := range hits {
		id := hit.GetThreadRootId()
		root, ok := roots[id]
		sum, summarized := summaries[id]
		if !ok || !summarized || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ThreadHit{
			Root:    root,
			Summary: sum.Summary,
			Stale:   latest[id] > sum.LastMsgID,
			Score:   hit.GetScore(),
		})
	}
	return out, nil
}

func clampLimit(limit, defaultLimit, maxLimit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	return min(limit, maxLimit)
}
