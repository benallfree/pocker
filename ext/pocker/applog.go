package pocker

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/tools/logger"
)

// appLogToInstanceLog copies the app's own log entries (Admin > Logs) to the instance log, as `pocketbase serve
// --dev` prints them: hook and cron errors, $app.logger() calls, failed requests. Successful requests stay out (they
// would drown the rest).
func appLogToInstanceLog(l *instanceLog) func(*logger.Log) {
	return func(log *logger.Log) {
		isRequest := fmt.Sprint(log.Data["type"]) == "request"
		if isRequest && log.Level < slog.LevelWarn {
			return
		}
		stream := "stdout"
		if log.Level >= slog.LevelWarn {
			stream = "stderr"
		}
		l.Write(stream, formatAppLog(log, isRequest))
	}
}

func formatAppLog(log *logger.Log, isRequest bool) string {
	var b strings.Builder
	b.WriteString(levelName(log.Level))
	b.WriteString(" ")
	b.WriteString(log.Message)
	if isRequest {
		if s, ok := log.Data["status"]; ok {
			fmt.Fprintf(&b, " %v", s)
		}
		for _, k := range []string{"error", "details"} {
			if v := log.Data[k]; v != nil && fmt.Sprint(v) != "map[]" {
				fmt.Fprintf(&b, "\n└─ %v", v)
			}
		}
		return b.String()
	}
	if len(log.Data) > 0 {
		keys := make([]string, 0, len(log.Data))
		for k := range log.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%s=%v", k, log.Data[k])
		}
		b.WriteString("\n└─ ")
		b.WriteString(strings.Join(parts, " "))
	}
	return b.String()
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARN"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}
