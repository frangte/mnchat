package server

import (
	"context"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
	"github.com/frangte/mnchat/internal/search/semantic"
	"github.com/frangte/mnchat/internal/search/store"
)

const (
	// The summary model's input budget: the root and the newest replies, up to
	// these limits. Recent replies hold the decision and the next step.
	maxThreadMessages = 200
	maxThreadChars    = 12_000

	// Chat caps a message at 4,000 characters, so the cut rarely applies. With it,
	// the root and the newest reply always fit in maxThreadChars together.
	maxMessageChars = 4_000
	maxNameChars    = 100
)

// SummarizeThread summarizes a thread and embeds the summary. It's all-or-nothing:
// chat stores the summary and its embedding together, so a failed embedding fails
// the call rather than return a summary search could never find.
func (s *Server) SummarizeThread(ctx context.Context, req *searchv1.SummarizeThreadRequest) (*searchv1.SummarizeThreadResponse, error) {
	const rpc = "SummarizeThread"
	if req.GetWorkspaceId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "workspace_id must be positive")
	}
	if req.GetThreadRootId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "thread_root_id must be positive")
	}

	ctx, cancel := context.WithTimeout(ctx, s.summarizeTimeout)
	defer cancel()
	msgs, err := s.store.ThreadMessages(ctx, req.GetWorkspaceId(), req.GetThreadRootId(), maxThreadMessages)
	if err != nil {
		return nil, storeError(ctx, rpc, err)
	}
	// The thread may not have replicated yet, or it was deleted.
	if len(msgs) == 0 {
		return nil, status.Error(codes.NotFound, "thread not found")
	}
	if msgs[0].ThreadRootID != 0 {
		return nil, status.Error(codes.InvalidArgument, "thread_root_id is a reply, not a thread root")
	}

	transcript, lastID := buildTranscript(msgs)
	summary, _, err := s.ai.Summarizer.Summarize(ctx, transcript)
	if err != nil {
		return nil, aiError(ctx, rpc, err)
	}
	embedding, err := s.ai.Embedder.Embed(ctx, summary)
	if err != nil {
		return nil, aiError(ctx, rpc, err)
	}
	return &searchv1.SummarizeThreadResponse{
		Summary:   summary,
		Embedding: embedding,
		LastMsgId: lastID,
		Model:     s.ai.Model,
	}, nil
}

// SearchThreads ranks the summarized threads in the caller's channels by meaning.
func (s *Server) SearchThreads(ctx context.Context, req *searchv1.SearchThreadsRequest) (*searchv1.SearchThreadsResponse, error) {
	const rpc = "SearchThreads"
	if req.GetWorkspaceId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "workspace_id must be positive")
	}
	text := strings.TrimSpace(req.GetQuery())
	if text == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	// Chat sends the caller's readable channels, so none means nothing to search,
	// and no reason to call the model.
	if len(req.GetChannelIds()) == 0 {
		return &searchv1.SearchThreadsResponse{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.threadSearchTimeout)
	defer cancel()
	vectors, err := s.store.ThreadVectors(ctx, req.GetWorkspaceId(), req.GetChannelIds())
	if err != nil {
		return nil, storeError(ctx, rpc, err)
	}
	// Nothing summarized in these channels yet: skip embedding the query.
	if len(vectors) == 0 {
		return &searchv1.SearchThreadsResponse{}, nil
	}
	query, err := s.ai.Embedder.Embed(ctx, text)
	if err != nil {
		return nil, aiError(ctx, rpc, err)
	}

	candidates := make([]semantic.Candidate, len(vectors))
	for i, v := range vectors {
		candidates[i] = semantic.Candidate{ThreadRootID: v.ThreadRootID, Embedding: v.Embedding}
	}
	hits := semantic.Rank(query, candidates, clampLimit(req.GetLimit(), defaultThreadLimit, maxThreadLimit))

	resp := &searchv1.SearchThreadsResponse{Hits: make([]*searchv1.ThreadHit, 0, len(hits))}
	for _, h := range hits {
		resp.Hits = append(resp.Hits, &searchv1.ThreadHit{ThreadRootId: h.ThreadRootID, Score: h.Score})
	}
	return resp, nil
}

// buildTranscript formats msgs, root first and then replies in ID order, as one
// "Name: text" line per message. It keeps the root and as many of the newest
// replies as fit in maxThreadMessages and maxThreadChars, dropping the oldest
// replies first. lastID is the highest ID read, which is always in the transcript.
func buildTranscript(msgs []store.ThreadMessage) (transcript string, lastID int64) {
	for _, m := range msgs {
		lastID = max(lastID, m.ID)
	}

	root := formatLine(msgs[0])
	budget := maxThreadChars - utf8.RuneCountInString(root)
	replies := msgs[1:]
	if len(replies) > maxThreadMessages-1 {
		replies = replies[len(replies)-(maxThreadMessages-1):]
	}

	// Walk back from the newest reply until the budget runs out.
	kept := make([]string, 0, len(replies))
	for i := len(replies) - 1; i >= 0; i-- {
		line := formatLine(replies[i])
		cost := utf8.RuneCountInString(line) + 1 // the newline before it
		if cost > budget {
			break
		}
		budget -= cost
		kept = append(kept, line)
	}

	var b strings.Builder
	b.WriteString(root)
	for i := len(kept) - 1; i >= 0; i-- {
		b.WriteByte('\n')
		b.WriteString(kept[i])
	}
	return b.String(), lastID
}

// formatLine puts a message on one line, so a body with line breaks can't start
// a line that looks like another person speaking.
func formatLine(m store.ThreadMessage) string {
	name := truncateRunes(oneLine(m.UserName), maxNameChars)
	if name == "" {
		name = "Unknown"
	}
	return name + ": " + truncateRunes(oneLine(m.Body), maxMessageChars)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
