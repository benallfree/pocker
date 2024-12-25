package ubermax

import (
	"errors"
	"net/url"
	"pocker/core/ioc"
	"pocker/core/providers/deployment/ubermax/mothership"
)

type Provider struct {
}

type Deployment struct {
	instance ioc.IInstance
	user     ioc.IUser
	machine  ioc.IMachine
}

func New() ioc.IDeploymentProvider {
	provider := &Provider{}
	provider.Start()
	return provider
}

func (p *Provider) Start() {

}

func (p *Provider) GetDeploymentByHost(host string) (ioc.IDeployment, error) {
	db := mothership.Mothership()

	instance, err := db.GetInstanceByHostHeader(host)
	if err != nil {
		return nil, err
	}
	user, err := db.GetUserById(instance.UserId())
	if err != nil {
		return nil, err
	}
	machine, err := db.GetMachineById(instance.MachineId())
	if err != nil {
		return nil, err
	}

	deployment := &Deployment{
		instance: instance,
		user:     user,
		machine:  machine,
	}

	return deployment, nil
}

func (d *Deployment) Url() (*url.URL, error) {
	return nil, errors.New("not implemented")
}
