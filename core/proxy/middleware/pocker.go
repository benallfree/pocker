package middleware

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"pocker/core/ioc"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type PockerMiddlewareConfig struct {
	LegacyOriginUrl             string
	LegacyOriginHelperProxyUrl  string
	LegacyApexDomain            string
	LegacyOriginHelperMachineId string
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

	legacyApexDomain := config.LegacyApexDomain
	if legacyApexDomain == "" {
		panic("Legacy apex domain is required")
	}

	legacyOriginHelperMachineId := config.LegacyOriginHelperMachineId
	if legacyOriginHelperMachineId == "" {
		panic("Legacy origin helper machine id is required")
	}

	secret := config.PHSecret
	if secret == "" {
		panic("PH secret is required")
	}

	thisMachineId := ioc.MachineInfoService().MachineId()
	isLegacyOriginHelper := thisMachineId == legacyOriginHelperMachineId

	mothershipApi := ioc.MothershipService()

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

	// ================================================
	// Create proxy URL
	// ================================================
	legacyProxy := httputil.NewSingleHostReverseProxy(legacyOriginUrl)
	legacyProxy.Transport = transport
	legacyProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Warn("Inside legacyproxy error handler", "error", err)
	}

	legacyHelperProxy := httputil.NewSingleHostReverseProxy(legacyOriginHelperProxyUrl)
	legacyHelperProxy.Transport = transport
	legacyHelperProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Warn("Inside legacy helper proxy error handler", "error", err)
	}

	handleLegacy := func(c *gin.Context, deployment ioc.IDeployment) {
		host := strings.Split(c.Request.Host, ":")[0]
		// slog.Debug("Received request from host", "host", host)

		subdomain := strings.Split(host, ".")[0]
		finalHost := fmt.Sprintf("%s.%s", subdomain, legacyApexDomain)
		c.Request.Host = finalHost
		c.Request.Header.Set("Host", finalHost)

		// logRequest("Modified request", c)
		// slog.Debug("Proxy URL", "url", proxyUrl)

		c.Request.Header.Set("X-Pockethost-Secret", secret)

		if !isLegacyOriginHelper {
			// slog.Debug("Machine id is not the legacy origin helper machine id, using legacy origin helper machine", "machine_id", thisMachineId)
			legacyHelperProxy.ServeHTTP(c.Writer, c.Request)
		} else {
			// slog.Debug("Machine id is the legacy origin helper machine id", "machine_id", thisMachineId)
			legacyProxy.ServeHTTP(c.Writer, c.Request)
		}
	}

	handleLocal := func(c *gin.Context, deployment ioc.IDeployment) {
		panic("handle local")
		// ================================================
		// At this point, we are local, so we need to get or create a PocketBase instance
		// ================================================
		// container, err := ioc.ContainerService().GetOrCreateContainer(deployment)
		// if err != nil {
		// 	c.String(http.StatusNotFound, "Could not launch PocketBase instance. Please try again later.")
		// 	c.Abort()
		// 	return
		// }

		// proxy := httputil.NewSingleHostReverseProxy(container.Url())
		// proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// 	panic(err)
		// }
		// proxy.ServeHTTP(c.Writer, c.Request)
	}

	handleNeighbor := func(c *gin.Context, deployment ioc.IDeployment) {
		panic("Pass to neighbor")
		// proxy := httputil.NewSingleHostReverseProxy(deployment.PrivateUrl())
		// proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// 	panic(err)
		// }
		// proxy.ServeHTTP(c.Writer, c.Request)
	}

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
		deployment, err := mothershipApi.GetDeploymentByIdentifier(c.Request.Host)
		if err != nil {
			c.String(http.StatusServiceUnavailable, fmt.Sprintf("%s", err))
			c.Abort()
			return
		}

		// ================================================
		// Securty checks - no point in proceeding if these fail
		// ================================================
		if !deployment.IsUserVerified() {
			c.String(http.StatusForbidden, "Please verify your PocketHost account.")
			c.Abort()
			return
		}

		if deployment.IsUserSuspended() {
			c.String(http.StatusForbidden, deployment.UserSuspendedReason())
			c.Abort()
			return
		}

		if deployment.IsInstanceSuspended() {
			c.String(http.StatusForbidden, deployment.InstanceSuspendedReason())
			c.Abort()
			return
		}

		if !deployment.IsInstancePoweredOn() {
			c.String(http.StatusForbidden, "Instance is not powered on")
			c.Abort()
			return
		}

		// ================================================
		// Migration check - if we need to migrate, do it
		// ================================================
		// if deployment.NeedsMigration() {
		// 	if !deployment.IsMigrating() {
		// 		deployment.BeginMigration()
		// 	}
		// 	c.String(http.StatusServiceUnavailable, "PocketHost is migrating your instance. Please try again later.")
		// 	c.Abort()
		// 	return
		// }

		isLegacy := deployment.IsLegacy()
		isLocal := deployment.MachineId() == thisMachineId
		isNeighbor := !isLegacy && deployment.MachineId() != thisMachineId

		if isLegacy {
			handleLegacy(c, deployment)
		}
		if isLocal {
			handleLocal(c, deployment)
		}
		if isNeighbor {
			handleNeighbor(c, deployment)
		}

		c.Next()
	}
}
