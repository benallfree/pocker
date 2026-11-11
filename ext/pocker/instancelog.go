package pocker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// instanceLog appends JSON lines {message, stream, time} to <instance folder>/logs/exec.log, the same format the
// PocketHost edge daemon writes for containers, and rotates it to exec.log.1 past maxSize.
type instanceLog struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	f       *os.File
	size    int64
}

const instanceLogMaxSize = 10 << 20

var instanceLogs sync.Map // path -> *instanceLog (one writer per file, shared by an app's VMs and restarts)

func openInstanceLog(dir string) *instanceLog {
	path := filepath.Join(dir, "logs", "exec.log")
	l, _ := instanceLogs.LoadOrStore(path, &instanceLog{path: path, maxSize: instanceLogMaxSize})
	return l.(*instanceLog)
}

func (l *instanceLog) Write(stream, message string) {
	line, err := json.Marshal(struct {
		Message string `json:"message"`
		Stream  string `json:"stream"`
		Time    string `json:"time"`
	}{message, stream, time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
			return
		}
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		st, _ := f.Stat()
		l.f, l.size = f, 0
		if st != nil {
			l.size = st.Size()
		}
	}
	if l.size+int64(len(line)) > l.maxSize {
		_ = l.f.Close()
		_ = os.Rename(l.path, l.path+".1")
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			l.f = nil
			return
		}
		l.f, l.size = f, 0
	}
	n, _ := l.f.Write(line)
	l.size += int64(n)
}
