package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/domain"
)

// minSummaryReplies matches the web's Summary button, which appears at 3 replies.
const minSummaryReplies = 3

// Summarizer asks search to summarize and embed a thread. It returns domain errors,
// so a dead search reaches the handler as ErrUnavailable.
type Summarizer interface {
	SummarizeThread(ctx context.Context, req *searchv1.SummarizeThreadRequest) (*searchv1.SummarizeThreadResponse, error)
}

// ThreadReader loads a thread root and finds its newest reply, both on the primary.
type ThreadReader interface {
	Get(ctx context.Context, workspaceID, id int64) (domain.Message, error)
	LatestReplyID(ctx context.Context, workspaceID, rootID int64) (int64, error)
}

// SummaryStore keeps summaries on the primary. Get returns ErrNotFound when a thread
// has none.
type SummaryStore interface {
	Get(ctx context.Context, workspaceID, rootID int64) (domain.ThreadSummary, error)
	Upsert(ctx context.Context, s domain.ThreadSummary, embedding []float32) error
}

// SummaryService serves thread summaries. A stored summary is served from the primary
// without search, so it keeps working while search or Ollama is down.
type SummaryService struct {
	workspaces WorkspaceStore
	channels   ChannelStore
	messages   ThreadReader
	summaries  SummaryStore
	client     Summarizer
}

func NewSummaryService(w WorkspaceStore, c ChannelStore, m ThreadReader, s SummaryStore, client Summarizer) *SummaryService {
	return &SummaryService{workspaces: w, channels: c, messages: m, summaries: s, client: client}
}

// Summary returns the stored summary, or nil when nobody has generated one yet.
func (s *SummaryService) Summary(ctx context.Context, workspaceID, rootID, userID int64) (*domain.ThreadSummary, error) {
	root, err := s.threadRoot(ctx, workspaceID, rootID, userID)
	if err != nil {
		return nil, err
	}
	stored, err := s.summaries.Get(ctx, workspaceID, root.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.markStale(ctx, workspaceID, root.ID, &stored); err != nil {
		return nil, err
	}
	return &stored, nil
}

// Summarize returns the stored summary if it covers the newest reply, and otherwise
// asks search for a new one and stores it.
func (s *SummaryService) Summarize(ctx context.Context, workspaceID, rootID, userID int64) (*domain.ThreadSummary, error) {
	root, err := s.threadRoot(ctx, workspaceID, rootID, userID)
	if err != nil {
		return nil, err
	}
	if root.ReplyCount < minSummaryReplies {
		return nil, fmt.Errorf("%w: a thread needs at least %d replies to summarize", domain.ErrInvalid, minSummaryReplies)
	}

	latest, err := s.messages.LatestReplyID(ctx, workspaceID, root.ID)
	if err != nil {
		return nil, err
	}
	stored, err := s.summaries.Get(ctx, workspaceID, root.ID)
	switch {
	case err == nil && stored.LastMsgID >= latest:
		stored.Stale = false
		return &stored, nil
	case err != nil && !errors.Is(err, domain.ErrNotFound):
		return nil, err
	}

	// No transaction is open here: this call can take most of a minute.
	res, err := s.client.SummarizeThread(ctx, &searchv1.SummarizeThreadRequest{
		WorkspaceId:  workspaceID,
		ThreadRootId: root.ID,
	})
	if err != nil {
		return nil, err
	}
	if err := checkSummary(res); err != nil {
		slog.Warn("search returned an unusable summary", "thread_root_id", root.ID, "err", err)
		return nil, fmt.Errorf("%w: search returned an unusable summary", domain.ErrUnavailable)
	}
	if err := s.summaries.Upsert(ctx, domain.ThreadSummary{
		ThreadRootID: root.ID,
		WorkspaceID:  workspaceID,
		ChannelID:    root.ChannelID,
		Summary:      res.GetSummary(),
		LastMsgID:    res.GetLastMsgId(),
		Model:        res.GetModel(),
	}, res.GetEmbedding()); err != nil {
		return nil, err
	}

	// Read back what is stored: the database sets updated_at, and a concurrent
	// request may have stored a summary that covers more.
	stored, err = s.summaries.Get(ctx, workspaceID, root.ID)
	if err != nil {
		return nil, err
	}
	// Only now: search read a replica that can lag, and replies keep arriving while
	// the model runs, so a summary can be stale the moment it is stored.
	if err := s.markStale(ctx, workspaceID, root.ID, &stored); err != nil {
		return nil, err
	}
	return &stored, nil
}

// threadRoot is the access check shared by GET and POST. A missing root, a hidden
// channel and another workspace all look the same, and all come before anything
// can call search.
func (s *SummaryService) threadRoot(ctx context.Context, workspaceID, rootID, userID int64) (domain.Message, error) {
	if _, err := s.workspaces.MemberRole(ctx, workspaceID, userID); err != nil {
		return domain.Message{}, err
	}
	root, err := s.messages.Get(ctx, workspaceID, rootID)
	if err != nil {
		return domain.Message{}, err
	}
	if _, err := s.channels.GetVisible(ctx, workspaceID, root.ChannelID, userID); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			slog.Error("check channel visibility", "channel_id", root.ChannelID, "err", err)
		}
		return domain.Message{}, domain.ErrNotFound
	}
	if root.ThreadRootID != nil {
		return domain.Message{}, fmt.Errorf("%w: only a thread's first message has a summary, not a reply", domain.ErrInvalid)
	}
	return root, nil
}

func (s *SummaryService) markStale(ctx context.Context, workspaceID, rootID int64, sum *domain.ThreadSummary) error {
	latest, err := s.messages.LatestReplyID(ctx, workspaceID, rootID)
	if err != nil {
		return err
	}
	sum.Stale = latest > sum.LastMsgID
	return nil
}

// checkSummary rejects a response chat can't store. Search is another service, so a
// malformed answer is its failure, not the caller's.
func checkSummary(res *searchv1.SummarizeThreadResponse) error {
	if strings.TrimSpace(res.GetSummary()) == "" {
		return errors.New("empty summary")
	}
	embedding := res.GetEmbedding()
	if len(embedding) != domain.EmbeddingDims {
		return fmt.Errorf("embedding has %d dimensions, want %d", len(embedding), domain.EmbeddingDims)
	}
	for i, f := range embedding {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return fmt.Errorf("embedding value %d is %v", i, f)
		}
	}
	return nil
}
