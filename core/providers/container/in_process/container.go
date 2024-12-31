package in_process

import (
	"net/url"
	"pocker/core/ioc"
	"sync"
	"sync/atomic"

	"github.com/pocketbase/pocketbase"
)

var _ ioc.IContainer = (*Container)(nil)

type Container struct {
	initOnce   sync.Once
	err        atomic.Value
	app        *pocketbase.PocketBase
	port       int
	url        *url.URL
	deployment ioc.IDeployment
}

func (c *Container) Url() *url.URL {
	return c.url
}

func (c *Container) Deployment() ioc.IDeployment {
	return c.deployment
}
