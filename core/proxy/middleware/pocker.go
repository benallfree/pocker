package middleware

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type PockerMiddlewareConfig struct {
	LegacyOriginUrl             string
	LegacyOriginHelperProxyUrl  string
	LegacyApexDomain            string
	LegacyOriginHelperMachineId string
	MachineId                   string
	PHSecret                    string
}

// Modify handleRequest to be the core handler without middleware
func PockerMiddleware(config PockerMiddlewareConfig) gin.HandlerFunc {
	legacyOriginUrl, err := url.Parse(config.LegacyOriginUrl)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse legacy origin url: %s", err))
	}

	legacyOriginHelperProxyUrl, err := url.Parse(config.LegacyOriginHelperProxyUrl)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse legacy origin helper proxy url: %s", err))
	}
	slog.Debug("Legacy origin helper proxy url", "url", legacyOriginHelperProxyUrl)

	if config.MachineId == "" {
		panic("Machine id is required")
	}
	slog.Debug("Machine id", "machine_id", config.MachineId)

	legacyApexDomain := config.LegacyApexDomain
	if legacyApexDomain == "" {
		panic("Legacy apex domain is required")
	}

	thisMachineId := config.MachineId
	if thisMachineId == "" {
		panic("Machine id is required")
	}

	legacyOriginHelperMachineId := config.LegacyOriginHelperMachineId
	if legacyOriginHelperMachineId == "" {
		panic("Legacy origin helper machine id is required")
	}

	secret := config.PHSecret
	if secret == "" {
		panic("PH secret is required")
	}

	isLegacyOriginHelper := thisMachineId == legacyOriginHelperMachineId
	// slog.Debug("Is legacy origin helper", "is_legacy_origin_helper", isLegacyOriginHelper)
	return func(c *gin.Context) {
		// deployment, err := ioc.DeploymentService().GetDeploymentByHost(c.Request.Host)
		// if err != nil {
		// 	c.String(http.StatusNotFound, "Deployment not found")
		// 	c.Abort()
		// 	return
		// }

		// url, err := deployment.Url()
		// if err != nil {
		// 	c.String(http.StatusServiceUnavailable, fmt.Sprintf("%s", err))
		// 	c.Abort()
		// 	return
		// }

		host := strings.Split(c.Request.Host, ":")[0]
		// slog.Debug("Received request from host", "host", host)

		subdomain := strings.Split(host, ".")[0]
		finalHost := fmt.Sprintf("%s.%s", subdomain, legacyApexDomain)
		c.Request.Host = finalHost
		c.Request.Header.Set("Host", finalHost)

		proxyUrl := legacyOriginUrl
		if !isLegacyOriginHelper {
			// slog.Debug("Machine id is not the legacy origin helper machine id, using legacy origin helper machine", "machine_id", thisMachineId)
			proxyUrl = legacyOriginHelperProxyUrl
		}

		// logRequest("Modified request", c)
		// slog.Debug("Proxy URL", "url", proxyUrl)

		c.Request.Header.Set("X-Pockethost-Secret", secret)

		// ================================================
		// Create proxy URL
		// ================================================

		proxy := httputil.NewSingleHostReverseProxy(proxyUrl)

		// Configure proxy with custom transport that skips TLS verification
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			MaxIdleConns:        100000,
			MaxConnsPerHost:     1000,
			MaxIdleConnsPerHost: 1000,
			IdleConnTimeout:     5 * time.Minute,
		}
		proxy.Transport = transport
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("Inside proxy error handler", "error", err)
		}
		proxy.ServeHTTP(c.Writer, c.Request)
		c.Next()
	}
}

func logRequest(title string, c *gin.Context) {
	slog.Debug("--------------------------------")
	slog.Debug(title, "method", c.Request.Method, "url", c.Request.URL.String())
	for k, v := range c.Request.Header {
		slog.Debug("\t", "key", k, "value", v)
	}
	slog.Debug("--------------------------------")
}
