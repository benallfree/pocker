package models

import (
	"net/http"
	"net/http/httputil"
	"pocker/core/ioc"
	"pocker/core/syncx"
)

type Deployment struct {
}

func (d *Deployment) Url() string {

	if reason := deployment.CanRun(); reason != "" {
		c.String(http.StatusNotFound, reason)
		c.Abort()
		return
	}

	// ================================================
	// Local check - if we are not local, proxy to the private URL
	// ================================================
	if !deployment.IsLocal() {
		proxy := httputil.NewSingleHostReverseProxy(deployment.PrivateUrl())
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			panic(err)
		}
		proxy.ServeHTTP(c.Writer, c.Request)
	}

	// ================================================
	// At this point, we are local, so we need to get or create a PocketBase instance
	// ================================================
	container, err := ioc.Container().GetOrCreateContainer(deployment)
	if err != nil {
		c.String(http.StatusNotFound, "Could not launch PocketBase instance. Please try again later.")
		c.Abort()
		return
	}

}

func (d *Deployment) CanRun() string {
	// ================================================
	// Securty checks - no point in proceeding if these fail
	// ================================================
	if !d.IsUserVerified() {
		return "Please verify your PocketHost account."
	}

	if d.IsUserSuspended() {
		return d.UserSuspendedReason()
	}

	if d.IsInstanceSuspended() {
		return d.InstanceSuspendedReason()
	}

	if !d.IsInstancePoweredOn() {
		return "Instance is not powered on"
	}

	// ================================================
	// Migration check - if we need to migrate, do it
	// ================================================
	if d.NeedsMigration() {
		if !d.IsMigrating() {
			d.BeginMigration()
		}
		return "PocketHost is migrating your instance. Please try again later."
	}

	return ""
}

func (d *Deployment) GetFieldMap() map[string]string {
	return map[string]string{
		"instanceId": d.Instance.Get().Id,
		"cname":      d.Instance.Get().Cname,
		"subdomain":  d.Instance.Get().Subdomain,
	}
}

type DeploymentReference struct {
	syncx.Reference[*Deployment]
}

func NewDeploymentReference() *DeploymentReference {
	return &DeploymentReference{
		Reference: *syncx.NewReference(
			&Deployment{},
		),
	}
}
