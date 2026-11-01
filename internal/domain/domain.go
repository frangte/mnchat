// Package domain holds the core types and errors shared by services, repos and transports.
package domain

import (
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrInvalid      = errors.New("invalid input")
	ErrConflict     = errors.New("conflict")
	ErrUnauthorized = errors.New("unauthorized")
	ErrUnavailable  = errors.New("unavailable")
)

type User struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

type Workspace struct {
	ID      int64  `json:"id"`
	OrgID   int64  `json:"org_id"`
	OrgName string `json:"org_name"`
	Name    string `json:"name"`
	Role    string `json:"role"` // the caller's role in this workspace
}

type ChannelType string

const (
	ChannelPublic  ChannelType = "public"
	ChannelPrivate ChannelType = "private"
	ChannelDM      ChannelType = "dm"
)

type Channel struct {
	ID          int64       `json:"id"`
	WorkspaceID int64       `json:"workspace_id"`
	Type        ChannelType `json:"type"`
	Name        string      `json:"name"`
	IsMember    bool        `json:"is_member"` // whether the caller has joined
	CreatedAt   time.Time   `json:"created_at"`
}

type Message struct {
	ID           int64     `json:"id"`
	WorkspaceID  int64     `json:"workspace_id"`
	ChannelID    int64     `json:"channel_id"`
	ThreadRootID *int64    `json:"thread_root_id"`
	UserID       int64     `json:"user_id"`
	UserName     string    `json:"user_name"`
	Body         string    `json:"body"`
	ReplyCount   int       `json:"reply_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// EmbeddingDims is the size of a summary embedding: bge-m3's output, and the
// thread_summaries.embedding VECTOR(1024) column.
const EmbeddingDims = 1024

// ThreadSummary is an AI summary of a thread. Only the fields with JSON names reach
// clients; the IDs locate the row, and the embedding never leaves the repo.
type ThreadSummary struct {
	ThreadRootID int64     `json:"-"`
	WorkspaceID  int64     `json:"-"`
	ChannelID    int64     `json:"-"`
	Summary      string    `json:"summary"`
	LastMsgID    int64     `json:"last_msg_id"` // newest reply the summary covers
	Model        string    `json:"model"`
	Stale        bool      `json:"stale"` // a reply newer than LastMsgID exists on the primary
	UpdatedAt    time.Time `json:"updated_at"`
}

const (
	EventMessageCreated = "message.created"
	EventChannelCreated = "channel.created"
)

// Event is pushed to connected clients over WebSocket.
type Event struct {
	Type      string `json:"type"`
	ChannelID int64  `json:"channel_id"`
	Data      any    `json:"data"`
	// Audience limits delivery to these users. Nil means the whole workspace.
	Audience []int64 `json:"-"`
}
