package fly

import (
	"log/slog"
	"pocker/core/ioc"

	"github.com/caarlos0/env/v11"
)

type MachineInfo struct {
	Region    string `env:"FLY_REGION,required"`
	AllocId   string `env:"FLY_ALLOC_ID,required"`
	AppName   string `env:"FLY_APP_NAME,required"`
	MachineId string `env:"FLY_MACHINE_ID,required"`
	PrivateIp string `env:"FLY_PRIVATE_IP,required"`
}

func MustGetFlyMachineInfo() MachineInfo {
	info, err := env.ParseAs[MachineInfo]()
	if err != nil {
		panic(err)
	}
	return info
}

type MachineInfoService struct {
}

var _ ioc.IMachineInfoService = (*MachineInfoService)(nil)

func New() ioc.IMachineInfoService {
	return &MachineInfoService{}
}

func (p *MachineInfoService) Start() {
}

func (p *MachineInfoService) Region() string {
	return MustGetFlyMachineInfo().Region
}

func (p *MachineInfoService) MachineId() string {
	return MustGetFlyMachineInfo().MachineId
}

func (p *MachineInfoService) PrivateIp() string {
	return MustGetFlyMachineInfo().PrivateIp
}

func (p *MachineInfoService) AppName() string {
	return MustGetFlyMachineInfo().AppName
}

func (p *MachineInfoService) PrintInfo() {
	slog.Info("Running on Fly.io",
		"region", p.Region(),
		"machine_id", p.MachineId(),
		"app_name", p.AppName(),
		"private_ip", p.PrivateIp())
}
