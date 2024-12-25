package models

import (
	"log/slog"
	"pocker/core/syncx"
)

type InstanceRec struct {
	RecordBase
	MachineId   string            `json:"machineId"`
	Cname       string            `json:"cname"`
	CnameActive bool              `json:"cname_active"`
	Dev         bool              `json:"dev"`
	IdleTtl     int               `json:"idleTtl"`
	Power       bool              `json:"power"`
	Region      string            `json:"region"`
	S3          string            `json:"s3"`
	Secrets     map[string]string `json:"secrets"`
	Status      string            `json:"status"`
	Subdomain   string            `json:"subdomain"`
	Suspension  string            `json:"suspension"`
	SyncAdmin   bool              `json:"syncAdmin"`
	Uid         string            `json:"uid"`
	Updated     string            `json:"updated"`
	Version     string            `json:"version"`
	Volume      string            `json:"volume"`
}

type Instance struct {
	rec *InstanceRec
}

func (p *Instance) IsSuspended() bool {
	return p.rec.Suspension != ""
}

func (p *Instance) SuspendedReason() string {
	return p.rec.Suspension
}

func (p *Instance) IsPoweredOn() bool {
	return p.rec.Power
}

func (p *Instance) GetFieldMap() map[string]string {
	slog.Debug("Getting field map for Instance",
		"instance", p)

	fields := map[string]string{
		"id":        p.rec.Id,
		"cname":     p.rec.Cname,
		"subdomain": p.rec.Subdomain,
	}

	slog.Debug("Field map generated",
		"fields", fields)

	return fields
}

type InstanceReference struct {
	syncx.Reference[*Instance]
}

func NewInstance() *Instance {
	return &Instance{}
}

func (p *Instance) MachineId() string {
	return p.rec.MachineId
}

func (p *Instance) UserId() string {
	return p.rec.Uid
}

func (p *Instance) Cname() string {
	return p.rec.Cname
}

func (p *Instance) Subdomain() string {
	return p.rec.Subdomain
}
