package ioc

type IPortService interface {
	IService
	AllocatePort() (int, error)
}

func RegisterPortService(provider IPortService) {
	Ioc().Register("port", provider)
}

func Port() IPortService {
	service := Ioc().Get("port")
	toProvider := any(service).(IPortService)
	return toProvider
}
