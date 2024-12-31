package ubermax

type Machine struct {
	RecordBase
	Uuid       string `json:"uuid"`
	Name       string `json:"name"`
	Region     string `json:"region"`
	PrivateUrl string `json:"privateUrl"`
}
