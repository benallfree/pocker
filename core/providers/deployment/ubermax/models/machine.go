package models

import (
	"pocker/core/syncx"
)

type MachineRec struct {
	RecordBase
	Uuid       string `json:"uuid"`
	Name       string `json:"name"`
	Region     string `json:"region"`
	PrivateUrl string `json:"privateUrl"`
}

type Machine struct {
	rec *MachineRec
}

func (m *Machine) GetFieldMap() map[string]string {
	return map[string]string{
		"id":         m.rec.Id,
		"name":       m.rec.Name,
		"uuid":       m.rec.Uuid,
		"privateUrl": m.rec.PrivateUrl,
	}
}

type MachineReference struct {
	syncx.Reference[*Machine]
}

func NewMachine() *Machine {
	return &Machine{}
}
