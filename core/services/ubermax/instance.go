package ubermax

type Instance struct {
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
