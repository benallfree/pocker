package middleware

import (
	"pocker/core/ioc"

	"github.com/gin-gonic/gin"
)

func FlyHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-PocketHost-Machine-Id", ioc.MachineInfoService().MachineId())
		c.Header("X-PocketHost-Region", ioc.MachineInfoService().Region())
		c.Next()
	}
}
