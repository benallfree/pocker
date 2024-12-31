package ubermax

import (
	"net/url"
	"pocker/core/ioc"
)

var _ ioc.IDeployment = (*Deployment)(nil)

type Deployment struct {
	instance *Instance
}

func NewDeployment(instance *Instance) ioc.IDeployment {
	return &Deployment{instance: instance}
}

func (d *Deployment) IsLegacy() bool {
	return d.instance.MachineId == ""
}

func (d *Deployment) InstanceId() string {
	return d.instance.Id
}

func (d *Deployment) MachineId() string {
	return d.instance.MachineId
}

func (d *Deployment) IsUserVerified() bool {
	return true
}

func (d *Deployment) IsUserSuspended() bool {
	return false
}

func (d *Deployment) IsInstanceSuspended() bool {
	return false
}

func (d *Deployment) IsInstancePoweredOn() bool {
	return true
}

func (d *Deployment) InstanceSuspendedReason() string {
	return ""
}

func (d *Deployment) UserSuspendedReason() string {
	return ""
}

func (d *Deployment) PrivateUrl() *url.URL {
	return nil
}
