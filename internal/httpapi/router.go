// Package httpapi is the gin transport layer: it parses requests, calls
// services, and maps domain errors to HTTP responses.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/frangte/mnchat/internal/realtime"
	"github.com/frangte/mnchat/internal/service"
)

type Handler struct {
	auth      *service.AuthService
	chat      *service.ChatService
	hub       *realtime.Hub
	wsOrigins []string
}

func NewRouter(auth *service.AuthService, chat *service.ChatService, hub *realtime.Hub, wsOrigins []string) http.Handler {
	h := &Handler{auth: auth, chat: chat, hub: hub, wsOrigins: wsOrigins}

	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())

	v1 := r.Group("/api/v1")
	v1.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	v1.GET("/auth/demo-users", h.demoUsers)
	v1.POST("/auth/demo-login", h.demoLogin)
	v1.GET("/ws", h.websocket) // authenticates via ?token= since browsers can't set headers

	authed := v1.Group("", h.requireAuth)
	authed.GET("/me", h.me)
	authed.GET("/workspaces", h.listWorkspaces)
	authed.GET("/workspaces/:wsID/channels", h.listChannels)
	authed.POST("/workspaces/:wsID/channels", h.createChannel)
	authed.GET("/workspaces/:wsID/channels/:chID/messages", h.listMessages)
	authed.POST("/workspaces/:wsID/channels/:chID/messages", h.postMessage)
	return r
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if c.Request.URL.Path == "/api/v1/healthz" {
			return
		}
		slog.Info("http",
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"duration", time.Since(start).Round(time.Microsecond),
		)
	}
}
