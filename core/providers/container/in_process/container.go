package in_process

import (
	"pocker/core/models"
	"sync"
	"sync/atomic"

	"github.com/pocketbase/pocketbase"
)

type Container struct {
	initOnce   sync.Once
	err        atomic.Value
	app        *pocketbase.PocketBase
	port       int
	url        string
	deployment *models.DeploymentReference
}

func (c *Container) Url() string {
	return c.url
}

func (c *Container) Deployment() *models.DeploymentReference {
	return c.deployment
}
