package ubermax

type RecordBase struct {
	CollectionId   string `json:"collectionId"`
	CollectionName string `json:"collectionName"`
	Id             string `json:"id"`
	Created        string `json:"created"`
	Updated        string `json:"updated"`
}
