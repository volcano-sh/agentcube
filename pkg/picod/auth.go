package picod

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware 认证中间件，验证 Bearer Token
func AuthMiddleware(accessToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 如果未配置 Access Token，则跳过认证
		if accessToken == "" {
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized",
				"code":  http.StatusUnauthorized,
			})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized: Invalid token format",
				"code":  http.StatusUnauthorized,
			})
			c.Abort()
			return
		}

		token := parts[1]
		if token != accessToken {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized: Invalid token",
				"code":  http.StatusUnauthorized,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

