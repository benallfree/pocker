package ioc

import "net/url"

type IContainer interface {
	Url() *url.URL
}

type IContainerService interface {
	IService
	GetOrCreateContainer(deployment IDeployment) (IContainer, error)
}

func RegisterContainerService(provider IContainerService) {
	providerService := IService(provider)
	Ioc().Register("containerService", providerService)
}

func ContainerService() IContainerService {
	service := Ioc().Get("containerService")
	toProvider := any(service).(IContainerService)
	return toProvider
}
