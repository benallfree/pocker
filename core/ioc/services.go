package ioc

func RegisterDeploymentService(provider IDeploymentProvider) {
	providerService := IService(provider)
	Ioc().Register("deploymentService", providerService)
}

func DeploymentService() IDeploymentProvider {
	service := Ioc().Get("deploymentService")
	toProvider := any(service).(IDeploymentProvider)
	return toProvider
}

// Container returns the container manager
func RegisterContainer(provider IContainerProvider) {
	Ioc().Register("container", provider)
}

func Container() IContainerProvider {
	service := Ioc().Get("container")
	toProvider := any(service).(IContainerProvider)
	return toProvider
}

func RegisterPort(provider IPortProvider) {
	Ioc().Register("port", provider)
}
