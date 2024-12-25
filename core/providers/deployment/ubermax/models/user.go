package models

import (
	"pocker/core/syncx"
)

type S3Config struct {
	Key    string `json:"key"`
	Secret string `json:"secret"`
}

type UserRec struct {
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

type User struct {
	rec *UserRec
}

func (p *User) IsVerified() bool {
	return p.rec.Verified
}

func (p *User) SuspendedReason() string {
	return p.rec.Suspension
}

func (p *User) IsSuspended() bool {
	return p.rec.Suspension != ""
}

func (p *User) GetFieldMap() map[string]string {
	return map[string]string{
		"id":       p.rec.Id,
		"email":    p.rec.Email,
		"username": p.rec.Username,
	}
}

type UserReference struct {
	syncx.Reference[*User]
}

func NewUser() *User {
	return &User{}
}
