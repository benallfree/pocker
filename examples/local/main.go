package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"pocker"
	"pocker/core/proxy"
	"pocker/core/proxy/middleware"
	"syscall"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type EnvConfig struct {
	MothershipUrl               string `env:"MOTHERSHIP_URL,required"`
	MothershipAdminEmail        string `env:"MOTHERSHIP_ADMIN_EMAIL,required"`
	MothershipAdminPassword     string `env:"MOTHERSHIP_ADMIN_PASSWORD,required"`
	DevMode                     bool   `env:"DEV_MODE" envDefault:"false"`
	LegacyOriginUrl             string `env:"LEGACY_ORIGIN_URL,required"`
	LegacyApexDomain            string `env:"LEGACY_APEX_DOMAIN,required"`
	LegacyOriginHelperMachineId string `env:"LEGACY_ORIGIN_HELPER_MACHINE_ID"`
	LegacyOriginHelperProxyUrl  string `env:"LEGACY_ORIGIN_HELPER_PROXY_URL,required"`
	PHSecret                    string `env:"PH_SECRET,required"`
}

func main() {
	// Load .env file if present
	if err := godotenv.Load(); err != nil {
		slog.Warn("No .env file found: %v", err)
	}

	cfg, err := env.ParseAs[EnvConfig]()
	if err != nil {
		panic(fmt.Sprintf("Failed to parse environment variables: %v", err))
	}

	if cfg.DevMode {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	// CLI flags
	machine := flag.String("machine", "loc1", "simulate the machine the server is running on")
	httpAddr := flag.String("http", ":8080", "the HTTP server address")
	flag.Parse()

	fmt.Println("Running as local machine:", *machine)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// deploymentProvider := ubermax.New()
	// ioc.RegisterDeploymentService(deploymentProvider)
	// deploymentProvider.Start()

	// portProvider := port_range.NewFixedPortRangeProvider(port_range.FixedPortRangeProviderConfig{})
	// ioc.RegisterPort(portProvider)

	// containerProvider := in_process.NewContainerProvider(in_process.ContainerProviderConfig{
	// 	DevMode:  config.DevMode,
	// 	DataRoot: config.DataRoot,
	// })
	// ioc.RegisterContainer(containerProvider)

	// containerProvider.Start()
	// portProvider.Start()

	pocker := pocker.NewPocker(pocker.PockerConfig{
		ProxyConfig: proxy.ProxyConfig{
			ListenAddr: *httpAddr,
			DevMode:    cfg.DevMode,
			PockerMiddlewareConfig: middleware.PockerMiddlewareConfig{
				LegacyOriginUrl:             cfg.LegacyOriginUrl,
				LegacyApexDomain:            cfg.LegacyApexDomain,
				LegacyOriginHelperMachineId: cfg.LegacyOriginHelperMachineId,
				MachineId:                   *machine,
				LegacyOriginHelperProxyUrl:  cfg.LegacyOriginHelperProxyUrl,
				PHSecret:                    cfg.PHSecret,
			},
		},
	})
	go pocker.Start()

	<-ctx.Done()
	slog.Info("Shutting down...")
}
