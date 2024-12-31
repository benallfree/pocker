package ubermax

import (
	"net/url"

	"github.com/caarlos0/env/v11"
)

type LegacySettingsType struct {
	OriginUrl             *url.URL `env:"LEGACY_ORIGIN_URL,required"`
	OriginHelperProxyUrl  *url.URL `env:"LEGACY_ORIGIN_HELPER_PROXY_URL,required"`
	ApexDomain            string   `env:"LEGACY_APEX_DOMAIN,required"`
	OriginHelperMachineId string   `env:"LEGACY_ORIGIN_HELPER_MACHINE_ID,required"`
}

var legacySettings *LegacySettingsType

func LegacySettings() *LegacySettingsType {
	if legacySettings == nil {
		env.Parse(&legacySettings)
	}
	return legacySettings
}
