package proxy

import (
	"log/slog"
	"net/http"
	"pocker/core/proxy/middleware"

	"github.com/gin-gonic/gin"
)

type Proxy struct {
	config ProxyConfig
}

type ProxyConfig struct {
	PockerMiddlewareConfig middleware.PockerMiddlewareConfig
	ListenAddr             string
	Middlewares            []gin.HandlerFunc
	PockerMiddlewares      []gin.HandlerFunc
	DevMode                bool
}

func NewProxy(config ProxyConfig) *Proxy {
	return &Proxy{
		config: config,
	}
}

// Modify Start method to use middleware
func (p *Proxy) Start() {

	if !p.config.DevMode {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()

	p.applyGlobalMiddlewares(r)
	p.bindEdgeApi(r)
	p.bindPockerDefaultHandler(r)
	r.Run(p.config.ListenAddr)

	slog.Info("Starting main server",
		"addr", p.config.ListenAddr)

	if err := http.ListenAndServe(p.config.ListenAddr, nil); err != nil {
		slog.Error("Server failed to start",
			"error", err)
		panic(err)
	}
}

func (p *Proxy) applyGlobalMiddlewares(r *gin.Engine) {
	r.Use(middleware.RecoveryMiddleware())
	r.Use(middleware.RequestTimerMiddleware())
	r.Use(p.config.Middlewares...)
}

func (p *Proxy) bindEdgeApi(r *gin.Engine) {
	api := r.Group("/x")
	{
		api.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": "ok"})
		})
	}
}

func (p *Proxy) bindPockerDefaultHandler(r *gin.Engine) {
	pockerMiddlewares := []gin.HandlerFunc{
		middleware.RequestLoggerMiddleware(),
	}
	pockerMiddlewares = append(pockerMiddlewares, p.config.PockerMiddlewares...)
	pockerMiddlewares = append(pockerMiddlewares, middleware.PockerMiddleware(p.config.PockerMiddlewareConfig))

	r.NoRoute(pockerMiddlewares...)
}
