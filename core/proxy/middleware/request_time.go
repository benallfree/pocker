package middleware

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

type customResponseWriter struct {
	gin.ResponseWriter
	start   time.Time
	context *gin.Context
}

func NewCustomResponseWriter(c *gin.Context) *customResponseWriter {
	return &customResponseWriter{
		ResponseWriter: c.Writer,
		start:          time.Now(),
		context:        c,
	}
}

func (w *customResponseWriter) WriteHeader(code int) {
	elapsed := time.Since(w.start)
	end := time.Now()
	w.Header().Set("X-PocketHost-Request-StartTime", fmt.Sprintf("%d", w.start.UnixMilli()))
	w.Header().Set("X-PocketHost-Request-End-Time", fmt.Sprintf("%d", end.UnixMilli()))
	w.Header().Set("X-PocketHost-Request-Duration", fmt.Sprintf("%d", elapsed.Milliseconds()))
	w.ResponseWriter.WriteHeader(code)
	slog.Debug("Request timing",
		"url", w.context.Request.URL.String(),
		"scheme", w.context.Request.URL.Scheme,
		"host", w.context.Request.Host,
		"path", w.context.Request.URL.Path,
		"method", w.context.Request.Method,
		"start", w.start.UnixMilli(),
		"end", end.UnixMilli(),
		"duration", elapsed.Milliseconds())
}

func RequestTimerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		writer := NewCustomResponseWriter(c)
		c.Writer = writer
		c.Next()
	}
}
