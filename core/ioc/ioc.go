package ioc

import (
	"fmt"
	"pocker/core/syncx"
	"sync"
)

type IService interface {
	Start()
}

// Container represents the IoC container singleton
type IoCContainer struct {
	services syncx.Map[string, IService]
}

var (
	instance *IoCContainer
	once     sync.Once
)

// Ioc returns the singleton instance of the IoC container
func Ioc() *IoCContainer {
	once.Do(func() {
		instance = &IoCContainer{
			services: syncx.Map[string, IService]{},
		}
	})
	return instance
}

func (c *IoCContainer) Register(name string, provider IService) {
	_, loaded := c.services.LoadOrStore(name, provider)
	if loaded {
		panic(fmt.Sprintf("provider %s already registered", name))
	}
}

// Provider retrieves a service from the container
func (c *IoCContainer) Get(name string) IService {
	provider, ok := c.services.Load(name)
	if !ok {
		panic(fmt.Sprintf("provider %s not found", name))
	}
	return provider
}
