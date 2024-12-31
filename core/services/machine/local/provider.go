package local

import (
	"log/slog"
	"pocker/core/ioc"
)

var _ ioc.IMachineInfoService = (*MachineInfoService)(nil)

type MachineInfoService struct {
	machineId string
	region    string
}

func New(machineId string, region string) ioc.IMachineInfoService {
	return &MachineInfoService{
		machineId: machineId,
		region:    region,
	}
}

func (p *MachineInfoService) Start() {
}

func (p *MachineInfoService) Region() string {
	return p.region
}

func (p *MachineInfoService) MachineId() string {
	return p.machineId
}

func (p *MachineInfoService) PrivateIp() string {
	return "127.0.0.1"
}

func (p *MachineInfoService) AppName() string {
	return "local"
}

func (p *MachineInfoService) PrintInfo() {
	slog.Info("Running on local machine",
		"region", p.Region(),
		"machine_id", p.MachineId(),
		"app_name", p.AppName(),
		"private_ip", p.PrivateIp())
}
