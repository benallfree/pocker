package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"pocker"
	"pocker/core/ioc"
	"pocker/core/proxy"
	"pocker/core/services/machine/fly"
	"pocker/core/services/ubermax"
	"syscall"

	"pocker/examples/fly/middleware"

	pockerMiddleware "pocker/core/proxy/middleware"

	"github.com/caarlos0/env/v11"
	"github.com/gin-gonic/gin"
)

type EnvConfig struct {
	MothershipUrl               string `env:"MOTHERSHIP_URL,required"`
	MothershipAdminEmail        string `env:"MOTHERSHIP_ADMIN_EMAIL,required"`
	MothershipAdminPassword     string `env:"MOTHERSHIP_ADMIN_PASSWORD,required"`
	DevMode                     bool   `env:"DEV_MODE" envDefault:"false"`
	LegacyApexDomain            string `env:"LEGACY_APEX_DOMAIN,required"`
	LegacyOriginUrl             string `env:"LEGACY_ORIGIN_URL,required"`
	LegacyOriginHelperProxyUrl  string `env:"LEGACY_ORIGIN_HELPER_PROXY_URL,required"`
	LegacyOriginHelperMachineId string `env:"LEGACY_ORIGIN_HELPER_MACHINE_ID,required"`
	PHSecret                    string `env:"PH_SECRET,required"`
}

func main() {
	cfg, err := env.ParseAs[EnvConfig]()
	if err != nil {
		panic(fmt.Sprintf("Failed to parse environment variables: %v", err))
	}
	if cfg.DevMode {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Add HTTP port flag
	httpAddr := flag.String("http", ":8080", "the HTTP server address")
	flag.Parse()

	// Bootstrap providers
	machineInfoService := fly.New()
	ioc.RegisterMachineInfoService(machineInfoService)

	mothershipService := ubermax.New()
	ioc.RegisterMothershipService(mothershipService)

	machineInfoService.Start()
	mothershipService.Start()

	// And begin proxy
	displayFlyInfo()

	pocker := pocker.NewPocker(pocker.PockerConfig{
		ProxyConfig: proxy.ProxyConfig{
			ListenAddr: *httpAddr,
			Middlewares: []gin.HandlerFunc{
				middleware.FlyHeadersMiddleware(),
			},
			DevMode: cfg.DevMode,
			PockerMiddlewareConfig: pockerMiddleware.PockerMiddlewareConfig{
				LegacyOriginHelperMachineId: cfg.LegacyOriginHelperMachineId,
				LegacyOriginHelperProxyUrl:  cfg.LegacyOriginHelperProxyUrl,
				LegacyOriginUrl:             cfg.LegacyOriginUrl,
				LegacyApexDomain:            cfg.LegacyApexDomain,
				PHSecret:                    cfg.PHSecret,
			},
		},
	})
	go pocker.Start()

	<-ctx.Done()
	fmt.Println("\nShutting down...")
}

func displayFlyInfo() {
	info := ioc.MachineInfoService()
	log.Printf("Running on Fly.io - Region: %s, Machine ID: %s, App: %s, Private IP: %s",
		info.Region(),
		info.MachineId(),
		info.AppName(),
		info.PrivateIp())
}
