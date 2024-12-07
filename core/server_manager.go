package core

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/pocketbase/pocketbase/tools/hook"
)

type Instance struct {
	App  *pocketbase.PocketBase
	Port int
}

type ServerManager struct {
	instances map[string]*Instance
	mu        sync.RWMutex
	nextPort  int
	portMu    sync.Mutex
}

func NewServerManager() *ServerManager {
	return &ServerManager{
		instances: make(map[string]*Instance),
		nextPort:  10000, // Starting port
	}
}

func (sm *ServerManager) allocatePort() (int, error) {
	sm.portMu.Lock()
	defer sm.portMu.Unlock()

	const maxPort = 12000

	// Try ports sequentially until we find an available one
	for port := sm.nextPort; port < maxPort; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			listener.Close()
			sm.nextPort = port + 1
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available ports in range 10000-12000")
}

func (sm *ServerManager) GetOrCreateInstance(subdomain string) (*Instance, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	log.Printf("Currently cached instances: %d", len(sm.instances))

	// Check if instance exists
	if instance, exists := sm.instances[subdomain]; exists {
		return instance, nil
	}

	port, err := sm.allocatePort()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate port: %w", err)
	}
	log.Printf("Found available port: %d", port)

	// ensureDir creates a directory if it doesn't exist
	ensureDir := func(path string) error {
		return os.MkdirAll(path, 0755)
	}

	// Ensure subdomain directory exists
	subdomainDir := filepath.Join("data", subdomain)
	if err := ensureDir(subdomainDir); err != nil {
		return nil, fmt.Errorf("failed to create subdomain directory: %w", err)
	}

	// Create new PocketBase instance
	app := pocketbase.NewWithConfig(pocketbase.Config{
		HideStartBanner: true,
		DefaultDev:      true,
		DefaultDataDir:  filepath.Join(subdomainDir, "pb_data"),
	})

	// Register jsvm plugin
	jsvm.MustRegister(app, jsvm.Config{
		MigrationsDir: filepath.Join(subdomainDir, "pb_migrations"),
		HooksDir:      filepath.Join(subdomainDir, "pb_hooks"),
		HooksWatch:    true,
	})

	// static route to serves files from the provided public dir
	// (if publicDir exists and the route path is not already defined)
	publicDir := filepath.Join(subdomainDir, "pb_public")
	indexFallback := true
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Func: func(e *core.ServeEvent) error {
			if !e.Router.HasRoute(http.MethodGet, "/{path...}") {
				e.Router.GET("/{path...}", apis.Static(os.DirFS(publicDir), indexFallback))
			}

			return e.Next()
		},
		Priority: 999, // execute as latest as possible to allow users to provide their own route
	})

	// Start the PocketBase instance
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Recovered from panic in server %d: %v", port, r)
			}
		}()
		if err := app.Serve(port); err != nil {
			log.Println(err)
		}
	}()

	ready := make(chan error)

	go func() {
		// Wait for server to be ready by polling health endpoint
		healthURL := fmt.Sprintf("http://localhost:%d/api/health", port)

		triesRemaining := 100
		for triesRemaining > 0 {
			log.Printf("Checking server health... (%d tries remaining)", triesRemaining)
			triesRemaining--
			resp, err := http.Get(healthURL)
			if err == nil && resp.StatusCode == 200 {
				resp.Body.Close()
				ready <- nil
				return
			}
			if resp != nil {
				resp.Body.Close()
			}
			time.Sleep(10 * time.Millisecond)
		}
		ready <- fmt.Errorf("Timeout waiting to start")
	}()

	if err := <-ready; err != nil {
		log.Printf("Failed to start server %s: %v", subdomain, err)
		return nil, err
	}

	instance := &Instance{
		App:  app,
		Port: port,
	}
	sm.instances[subdomain] = instance

	return instance, nil
}
