package in_process

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"pocker/core/ioc"
	"pocker/core/models"
	"pocker/core/syncx"
	"sync"
	"sync/atomic"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/pocketbase/pocketbase/tools/hook"
)

type ContainerProvider struct {
	initOnce        sync.Once
	containers      syncx.Map[string, *Container]
	containersCount atomic.Int32
	config          ContainerProviderConfig
}

type ContainerProviderConfig struct {
	DataRoot string
	DevMode  bool
}

func NewContainerProvider(config ContainerProviderConfig) *ContainerProvider {
	provider := ContainerProvider{
		containers:      syncx.Map[string, *Container]{},
		containersCount: atomic.Int32{},
		config:          config,
	}

	return &provider
}

// ensureDir creates a directory if it doesn't exist
func ensureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

func (sm *ContainerProvider) GetOrCreateContainer(deployment *models.DeploymentReference) (*Container, error) {
	slog.Debug("Currently cached instances",
		"count", sm.containersCount.Load())

	deployment.RLock()
	defer deployment.RUnlock()

	instance := deployment.Get().Instance.Get()

	container, _ := sm.containers.LoadOrStore(instance.Id, &Container{
		initOnce:   sync.Once{},
		err:        atomic.Value{},
		deployment: deployment,
	})
	container.initOnce.Do(func() {
		defer func() {
			err := recover()
			if err != nil {
				container.err.Store(fmt.Errorf("failed to initialize container %s: %v", instance.Id, err))
				sm.containers.Delete(instance.Id)
			}
			sm.containersCount.Add(1)
		}()

		port, err := ioc.Port().AllocatePort()
		if err != nil {
			panic(fmt.Errorf("failed to allocate port: %w", err))
		}

		// Ensure subdomain directory exists
		instanceDir := sm.dataDir(instance.Id)
		if err := ensureDir(instanceDir); err != nil {
			panic(fmt.Errorf("failed to create instance directory: %w", err))
		}

		// Create new PocketBase instance
		app := pocketbase.NewWithConfig(pocketbase.Config{
			HideStartBanner: true,
			DefaultDev:      sm.config.DevMode,
			DefaultDataDir:  filepath.Join(instanceDir, "pb_data"),
		})

		// Register jsvm plugin
		jsvm.MustRegister(app, jsvm.Config{
			MigrationsDir: filepath.Join(instanceDir, "pb_migrations"),
			HooksDir:      filepath.Join(instanceDir, "pb_hooks"),
			HooksWatch:    true,
		})

		// static route to serves files from the provided public dir
		// (if publicDir exists and the route path is not already defined)
		publicDir := filepath.Join(instanceDir, "pb_public")
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
		startError := make(chan error)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Recovered from panic in server",
						"instance_id", instance.Id,
						"error", r)
				}
			}()

			app.OnServe().BindFunc(func(e *core.ServeEvent) error {
				e.Next()
				startError <- nil
				return nil
			})
			if err := app.Serve(port); err != nil {
				startError <- err
				slog.Error("Failed to start server",
					"instance_id", instance.Id,
					"error", err)
			}

			// Delete the container from the map if it exists
			sm.containers.Delete(instance.Id)
		}()

		err, ok := <-startError
		if !ok {
			panic(fmt.Errorf("failed to start server %s: %v", instance.Id, err))
		}

		slog.Debug("Server started",
			"instance_id", instance.Id,
			"port", port)

		container.app = app
		container.port = port
		container.url = fmt.Sprintf("http://localhost:%d", port)
	})

	if container.err.Load() != nil {
		return nil, container.err.Load().(error)
	}

	return container, nil
}

func (sm *ContainerProvider) Start() {
	sm.initOnce.Do(sm.cleanupDataDir)
}

func (sm *ContainerProvider) dataDir(paths ...string) string {
	abs, err := filepath.Abs(filepath.Join(sm.config.DataRoot, filepath.Join(paths...)))
	if err != nil {
		panic(fmt.Errorf("failed to get absolute path for data root: %w", err))
	}
	return abs
}

func (sm *ContainerProvider) cleanupDataDir() {
	// Delete all subdirectories in data dir

	if err := os.MkdirAll(sm.dataDir(), 0755); err != nil {
		slog.Error("Failed to create data directory",
			"error", err)
	}

	// List and log subdirectories before deletion
	if entries, err := os.ReadDir(sm.dataDir()); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				subdomainDir, err := filepath.Abs(filepath.Join(sm.dataDir(), entry.Name()))
				if err != nil {
					slog.Error("Failed to get absolute path for subdirectory",
						"error", err)
					continue
				}
				slog.Debug("Deleting subdirectory",
					"path", subdomainDir)
				os.RemoveAll(subdomainDir)
			}
		}
	}
}