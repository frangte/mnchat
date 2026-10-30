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
	CreatedAt    time.Time `json:"created_at"`
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
}
