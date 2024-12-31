package ubermax

import "github.com/caarlos0/env/v11"

type IntercomSettingsType struct {
	Secret string `env:"PH_SECRET,required"`
}

var intercomSettings *IntercomSettingsType

func IntercomSettings() *IntercomSettingsType {
	if intercomSettings == nil {
		env.Parse(&intercomSettings)
	}
	return intercomSettings
}
