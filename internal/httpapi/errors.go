package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/frangte/mnchat/internal/domain"
)

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func abortError(c *gin.Context, status int, code, message string) {
	var body errorBody
	body.Error.Code = code
	body.Error.Message = message
	c.AbortWithStatusJSON(status, body)
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		abortError(c, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, domain.ErrInvalid):
		abortError(c, http.StatusBadRequest, "invalid", detail(err, domain.ErrInvalid))
	case errors.Is(err, domain.ErrConflict):
		abortError(c, http.StatusConflict, "conflict", detail(err, domain.ErrConflict))
	case errors.Is(err, domain.ErrUnauthorized):
		abortError(c, http.StatusUnauthorized, "unauthorized", "missing or invalid token")
	case errors.Is(err, domain.ErrUnavailable):
		abortError(c, http.StatusServiceUnavailable, "unavailable", detail(err, domain.ErrUnavailable))
	default:
		slog.Error("request failed", "path", c.FullPath(), "err", err)
		abortError(c, http.StatusInternalServerError, "internal", "internal server error")
	}
}

// detail strips the sentinel prefix from "invalid input: name is required",
// since the code field already conveys the category.
func detail(err, sentinel error) string {
	return strings.TrimPrefix(err.Error(), sentinel.Error()+": ")
}

// idParam parses a positive integer path parameter, writing a 404 if malformed.
func idParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		writeError(c, domain.ErrNotFound)
		return 0, false
	}
	return id, true
}
