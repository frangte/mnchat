// Package httpapi is the gin transport layer: it parses requests, calls
// services, and maps domain errors to HTTP responses.
package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/frangte/mnchat/internal/domain"
	"github.com/frangte/mnchat/internal/realtime"
	"github.com/frangte/mnchat/internal/service"
)

const (
	metricsRoute = "/metrics"
	wsRoute      = "/api/v1/ws"

	readyTimeout = 2 * time.Second
)

// Pinger reports whether the primary database responds. *sql.DB satisfies it.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// RequestMetrics records finished requests. Telemetry must never block a request.
type RequestMetrics interface {
	HTTPRequest(method, route string, status int)
	HTTPDuration(method, route string, d time.Duration)
}

type Handler struct {
	auth      *service.AuthService
	chat      *service.ChatService
	search    *service.SearchService
	hub       *realtime.Hub
	db        Pinger
	wsOrigins []string
}

func NewRouter(
	auth *service.AuthService,
	chat *service.ChatService,
	search *service.SearchService,
	hub *realtime.Hub,
	db Pinger,
	metrics RequestMetrics,
	metricsHandler http.Handler,
	wsOrigins []string,
) http.Handler {
	h := &Handler{auth: auth, chat: chat, search: search, hub: hub, db: db, wsOrigins: wsOrigins}

	r := gin.New()
	// requestMetrics goes first so it still sees the 500 after Recovery handles a panic.
	r.Use(requestMetrics(metrics), gin.Recovery(), requestLogger())

	r.GET(metricsRoute, gin.WrapH(metricsHandler))

	v1 := r.Group("/api/v1")
	v1.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	v1.GET("/readyz", h.readyz)
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
	authed.GET("/workspaces/:wsID/messages/:msgID/replies", h.listReplies)
	authed.GET("/workspaces/:wsID/search", h.searchWorkspace)
	return r
}

// readyz reports whether the primary responds. It must never check search, because
// chat keeps working without it.
func (h *Handler) readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), readyTimeout)
	defer cancel()
	if err := h.db.PingContext(ctx); err != nil {
		slog.Warn("readiness check failed", "err", err)
		writeError(c, fmt.Errorf("%w: database is not responding", domain.ErrUnavailable))
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// requestMetrics uses the route template and known methods only, keeping label
// cardinality bounded whatever a client sends. A WebSocket request is counted when
// the socket closes, with status 101, and has no duration: it lasts as long as the
// connection.
func requestMetrics(m RequestMetrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == metricsRoute {
			return
		}
		if route == "" {
			route = "unmatched"
		}
		method := methodLabel(c.Request.Method)
		m.HTTPRequest(method, route, c.Writer.Status())
		if route != wsRoute {
			m.HTTPDuration(method, route, time.Since(start))
		}
	}
}

var knownMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// methodLabel folds unknown methods together, since net/http accepts any token.
func methodLabel(method string) string {
	if slices.Contains(knownMethods, method) {
		return method
	}
	return "OTHER"
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
