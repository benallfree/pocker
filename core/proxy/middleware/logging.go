package middleware

import (
	"log/slog"

	"github.com/gin-gonic/gin"
)

func RequestLoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		slog.Debug("Received request",
			"host", c.Request.Host,
			"path", c.Request.URL.Path,
			"remote_addr", c.Request.RemoteAddr)
		c.Next()
	}
}
