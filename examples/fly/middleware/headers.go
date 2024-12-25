package middleware

import (
	"pocker/examples/fly/helpers"

	"github.com/gin-gonic/gin"
)

func FlyHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		machineInfo := helpers.MustGetFlyMachineInfo()
		c.Header("X-PocketHost-Machine-Id", machineInfo.MachineId)
		c.Header("X-PocketHost-Region", machineInfo.Region)
		c.Next()
	}
}
