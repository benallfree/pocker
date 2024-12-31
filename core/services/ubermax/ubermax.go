package ubermax

import (
	"pocker/core/ioc"
)

var _ ioc.IMothershipService = (*Ubermax)(nil)

type Ubermax struct {
}

func New() ioc.IMothershipService {
	provider := &Ubermax{}
	return provider
}

func (p *Ubermax) Start() {

}

func (p *Ubermax) GetDeploymentByIdentifier(identifier string) (ioc.IDeployment, error) {
	deployment := NewDeployment(
		&Instance{
			MachineId: "",
		},
	)
	return deployment, nil
}
