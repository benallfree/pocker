package middleware

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	_ "embed"
)

//go:embed recovery.html
var recoveryTemplate string

type errorResponse struct {
	Error     interface{} `json:"error"`
	Timestamp int64       `json:"timestamp"`
}

func RecoveryMiddleware() gin.HandlerFunc {
	tmpl := template.Must(template.New("error").Parse(recoveryTemplate))

	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// Get stack trace
				stack := debug.Stack()

				// Log both the error and stack trace
				slog.Error(fmt.Sprintf("Panic in request handler: %s\n%s", err, string(stack)))

				timestamp := time.Now().UnixMicro()

				// Get Accept header and default to HTML if not specified
				accept := c.GetHeader("Accept")

				c.Status(http.StatusInternalServerError)

				// Prepare common response data
				data := errorResponse{
					Error:     err,
					Timestamp: timestamp,
				}

				// Return format based on Accept header
				switch {
				case strings.Contains(accept, "application/json"):
					c.JSON(http.StatusInternalServerError, data)

				case strings.Contains(accept, "text/plain"):
					c.String(http.StatusInternalServerError, "Internal Server Error\nError: %v\nTimestamp: %d", err, timestamp)

				default: // HTML response
					tmpl.Execute(c.Writer, gin.H{
						"error":     err,
						"timestamp": timestamp,
					})
				}
			}
		}()
		c.Next()
	}
}
