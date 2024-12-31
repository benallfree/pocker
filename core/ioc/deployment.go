package ioc

type IDeploymentService interface {
	GetDeploymentByHost(host string) (IDeployment, error)
	Start()
}

type IDeployment interface {
	IsLegacy() bool
	InstanceId() string
	MachineId() string
	IsUserVerified() bool
	IsUserSuspended() bool
	IsInstanceSuspended() bool
	IsInstancePoweredOn() bool
	InstanceSuspendedReason() string
	UserSuspendedReason() string
}

type IDeploymentContainer interface {
	Deployment() IDeployment
	Url() string
}

func RegisterDeploymentService(provider IDeploymentService) {
	providerService := IService(provider)
	Ioc().Register("deploymentService", providerService)
}

func DeploymentService() IDeploymentService {
	service := Ioc().Get("deploymentService")
	toProvider := any(service).(IDeploymentService)
	return toProvider
}
