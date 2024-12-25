package helpers

import (
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
