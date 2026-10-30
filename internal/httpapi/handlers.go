package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/frangte/mnchat/internal/domain"
)

const ctxUserID = "userID"

func (h *Handler) requireAuth(c *gin.Context) {
	token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if !ok {
		writeError(c, domain.ErrUnauthorized)
		return
	}
	userID, err := h.auth.Authenticate(token)
	if err != nil {
		writeError(c, err)
		return
	}
	c.Set(ctxUserID, userID)
	c.Next()
}

func userID(c *gin.Context) int64 { return c.GetInt64(ctxUserID) }

func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		writeError(c, fmt.Errorf("%w: %v", domain.ErrInvalid, err))
		return false
	}
	return true
}

func (h *Handler) demoUsers(c *gin.Context) {
	users, err := h.auth.DemoUsers(c)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": users})
}

func (h *Handler) demoLogin(c *gin.Context) {
	var req struct {
		UserID int64 `json:"user_id" binding:"required"`
	}
	if !bindJSON(c, &req) {
		return
	}
	token, user, err := h.auth.DemoLogin(c, req.UserID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "user": user})
}

func (h *Handler) me(c *gin.Context) {
	user, err := h.auth.Me(c, userID(c))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, user)
}

func (h *Handler) listWorkspaces(c *gin.Context) {
	items, err := h.chat.Workspaces(c, userID(c))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) listChannels(c *gin.Context) {
	wsID, ok := idParam(c, "wsID")
	if !ok {
		return
	}
	items, err := h.chat.Channels(c, wsID, userID(c))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) createChannel(c *gin.Context) {
	wsID, ok := idParam(c, "wsID")
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !bindJSON(c, &req) {
		return
	}
	ch, err := h.chat.CreateChannel(c, wsID, userID(c), req.Name)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, ch)
}

func (h *Handler) listMessages(c *gin.Context) {
	wsID, ok := idParam(c, "wsID")
	if !ok {
		return
	}
	chID, ok := idParam(c, "chID")
	if !ok {
		return
	}
	before, _ := strconv.ParseInt(c.Query("before"), 10, 64)
	limit, _ := strconv.Atoi(c.Query("limit"))
	items, err := h.chat.Messages(c, wsID, chID, userID(c), before, limit)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) postMessage(c *gin.Context) {
	wsID, ok := idParam(c, "wsID")
	if !ok {
		return
	}
	chID, ok := idParam(c, "chID")
	if !ok {
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if !bindJSON(c, &req) {
		return
	}
	msg, err := h.chat.PostMessage(c, wsID, chID, userID(c), req.Body)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, msg)
}
