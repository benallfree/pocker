package ubermax

type S3Config struct {
	Key    string `json:"key"`
	Secret string `json:"secret"`
}

type User struct {
	RecordBase
	Email                string   `json:"email"`
	PasswordHash         string   `json:"passwordHash"`
	S3                   S3Config `json:"s3"`
	Subscription         string   `json:"subscription"`
	SubscriptionInterval string   `json:"subscription_interval"`
	TokenKey             string   `json:"tokenKey"`
	Unsubscribe          bool     `json:"unsubscribe"`
	Updated              string   `json:"updated"`
	Username             string   `json:"username"`
	Verified             bool     `json:"verified"`
	Suspension           string   `json:"suspension"`
	DoubleVerified       bool     `json:"double_verified"`
}
