package ioc

type IMachineInfoService interface {
	IService
	MachineId() string
	Region() string
	PrivateIp() string
	AppName() string
	PrintInfo()
}

func RegisterMachineInfoService(provider IMachineInfoService) {
	Ioc().Register("machineInfoService", provider)
}

func MachineInfoService() IMachineInfoService {
	service := Ioc().Get("machineInfoService")
	toProvider := any(service).(IMachineInfoService)
	return toProvider
}
