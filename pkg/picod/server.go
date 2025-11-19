package picod

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

var startTime = time.Now() // 服务器启动时间

// Config 定义服务器配置
type Config struct {
	Port        int    `json:"port"`
	AccessToken string `json:"access_token"`
}

// Server 定义 PicoD HTTP 服务器
type Server struct {
	engine *gin.Engine
	config Config
}

// NewServer 创建一个新的 PicoD 服务器实例
func NewServer(config Config) *Server {
	// 生产模式下禁用 Gin 的调试输出
	gin.SetMode(gin.ReleaseMode)
	
	engine := gin.New()

	// 全局中间件
	engine.Use(gin.Logger())   // 请求日志
	engine.Use(gin.Recovery()) // 崩溃恢复

	// 认证中间件（仅对 API 路由）
	api := engine.Group("/api")
	api.Use(AuthMiddleware(config.AccessToken))
	{
		api.POST("/execute", ExecuteHandler)
		api.POST("/files", UploadFileHandler)
		api.GET("/files/*path", DownloadFileHandler)
	}

	// 健康检查（无需认证）
	engine.GET("/health", HealthCheckHandler)

	return &Server{
		engine: engine,
		config: config,
	}
}

// Run 启动服务器
func (s *Server) Run() error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	log.Printf("PicoD server starting on %s", addr)
	if s.config.AccessToken != "" {
		log.Printf("Authentication: enabled")
	} else {
		log.Printf("Authentication: disabled (WARNING: not recommended for production)")
	}
	return http.ListenAndServe(addr, s.engine)
}

// HealthCheckHandler 处理健康检查请求
func HealthCheckHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "PicoD",
		"version": "1.0.0",
		"uptime":  time.Since(startTime).String(),
	})
}
