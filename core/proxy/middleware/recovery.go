package middleware

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"

	_ "embed"
)

//go:embed recovery.html
var recoveryTemplate string

type errorResponse struct {
	Error     interface{} `json:"error"`
	Signature string      `json:"signature"`
}

func RecoveryMiddleware() gin.HandlerFunc {
	tmpl := template.Must(template.New("error").Parse(recoveryTemplate))

	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				if err == http.ErrAbortHandler {
					slog.Debug("Request aborted", "request", c.Request.URL.Path)
					return
				}
				// Get stack trace
				stack := debug.Stack()

				// Log both the error and stack trace
				slog.Error("Caught panic on request", "request", c.Request.URL.Path, "error", err)
				slog.Error(fmt.Sprintf("Stack trace: %s\n", string(stack)))

				signature := fmt.Sprintf("%s-%s", c.Request.URL.Path, err)

				// Get Accept header and default to HTML if not specified
				accept := c.GetHeader("Accept")

				c.Status(http.StatusInternalServerError)

				// Prepare common response data
				data := errorResponse{
					Error:     err,
					Signature: signature,
				}

				// Return format based on Accept header
				switch {
				case strings.Contains(accept, "application/json"):
					c.JSON(http.StatusInternalServerError, data)

				case strings.Contains(accept, "text/plain"):
					c.String(http.StatusInternalServerError, "Internal Server Error: %S", signature)

				default: // HTML response
					tmpl.Execute(c.Writer, gin.H{
						"error":     err,
						"signature": signature,
					})
				}
			}
		}()
		c.Next()
	}
}
