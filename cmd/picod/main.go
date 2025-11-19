package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/volcano-sh/agentcube/pkg/picod"
)

func main() {
	port := flag.Int("port", 9527, "Port for the PicoD server to listen on")
	accessToken := flag.String("access-token", "", "Access token for authentication (optional)")
	accessTokenFile := flag.String("access-token-file", "", "Path to a file containing the access token (optional)")

	flag.Parse()

	config := picod.Config{
		Port: *port,
	}

	// 优先级: 命令行参数 > 文件 > 环境变量
	if *accessToken != "" {
		config.AccessToken = *accessToken
	} else if *accessTokenFile != "" {
		content, err := os.ReadFile(*accessTokenFile)
		if err != nil {
			log.Fatalf("Failed to read access token file %s: %v", *accessTokenFile, err)
		}
		config.AccessToken = strings.TrimSpace(string(content))
	} else {
		config.AccessToken = os.Getenv("PICOD_ACCESS_TOKEN")
	}

	if config.AccessToken == "" {
		fmt.Println("⚠️  Warning: No access token provided. PicoD will run without authentication.")
		fmt.Println("   This is NOT recommended for production environments.")
		fmt.Println("   Set token via --access-token, --access-token-file, or PICOD_ACCESS_TOKEN env var.")
	}

	// 创建并启动服务器
	server := picod.NewServer(config)

	if err := server.Run(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

