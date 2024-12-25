package helpers

import (
	"github.com/gin-gonic/gin"
)

func SetRegionContextFromFlyEnv() gin.HandlerFunc {
	return func(c *gin.Context) {
		info := MustGetFlyMachineInfo()
		region := info.Region
		if region == "" {
			panic("FLY_REGION environment variable not found")
		}
		c.Set("region", region)
		c.Next()
	}
}
