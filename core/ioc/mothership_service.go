package ioc

import "pocker/core/syncx"

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

type IMothershipService interface {
	IService
	GetDeploymentByIdentifier(identifier string) (IDeployment, error)
}

func RegisterMothershipService(provider IMothershipService) {
	providerService := IService(provider)
	Ioc().Register("mothershipService", providerService)
}

func MothershipService() IMothershipService {
	service := Ioc().Get("mothershipService")
	toProvider := any(service).(IMothershipService)
	return toProvider
}
