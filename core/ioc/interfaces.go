package ioc

import (
	"net/url"
	"pocker/core/syncx"
)

type IPortProvider interface {
	IService
	AllocatePort() (int, error)
}

type IInstance interface {
	syncx.IIndexedCacheItem
	UserId() string
	MachineId() string
}

type IUser interface {
	syncx.IIndexedCacheItem
}

type IMachine interface {
	syncx.IIndexedCacheItem
}

type ICentralDbProvider interface {
	IService
	GetInstanceByHostHeader(host string) (IInstance, error)
	GetUserById(id string) (IUser, error)
	GetMachineById(id string) (IMachine, error)
}

type IDeploymentProvider interface {
	GetDeploymentByHost(host string) (IDeployment, error)
	Start()
}
type IDeployment interface {
	Url() (*url.URL, error)
	// IsUserVerified() bool
	// IsUserSuspended() bool
	// IsInstancePoweredOn() bool
	// IsInstanceSuspended() bool
	// IsMigrating() bool
	// IsLocal() bool
	// PrivateUrl() *url.URL
	// UserSuspendedReason() string
	// InstanceSuspendedReason() string
	// BeginMigration()
	// NeedsMigration() bool
	// CanRun() string
}

type IDeploymentContainer interface {
	Deployment() IDeployment
	Url() string
}

type IContainer interface {
	Deployment() IDeployment
	Url() string
}

type IContainerProvider interface {
	IService
	GetOrCreateContainer(deployment IDeployment) (IContainer, error)
}

func Port() IPortProvider {
	service := Ioc().Get("port")
	toProvider := any(service).(IPortProvider)
	return toProvider
}
