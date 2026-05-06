/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package picod

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(_ *http.Request) bool {
		return true // Allow all origins for sandbox internal connections
	},
}

// EnvdProcessStartHandler handles POST /envd/process/start
func (s *Server) EnvdProcessStartHandler(c *gin.Context) {
	var req ProcessStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}

	if len(req.Cmd) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cmd is required"})
		return
	}

	// Resolve working directory relative to workspace
	cwd := req.Cwd
	if cwd != "" {
		safeCwd, err := s.sanitizePath(cwd)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		cwd = safeCwd
	}

	mp, err := s.processRegistry.Start(c.Request.Context(), req.Cmd, req.Env, cwd, req.Timeout)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start process: %v", err)})
		return
	}

	// Check if client wants WebSocket upgrade
	if websocket.IsWebSocketUpgrade(c.Request) {
		s.handleProcessWebSocket(c, mp.ProcessID)
		return
	}

	c.JSON(http.StatusOK, mp)
}

func (s *Server) handleProcessWebSocket(c *gin.Context, processID string) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		klog.Errorf("failed to upgrade websocket: %v", err)
		return
	}
	defer conn.Close()

	events, err := s.processRegistry.Subscribe(processID)
	if err != nil {
		klog.Errorf("failed to subscribe to process %s: %v", processID, err)
		_ = conn.WriteJSON(ProcessEvent{
			Type:  ProcessEventTypeError,
			Error: err.Error(),
			Time:  time.Now(),
		})
		return
	}
	defer s.processRegistry.Unsubscribe(processID, events)

	// Send initial info event
	mp, err := s.processRegistry.Get(processID)
	if err == nil {
		_ = conn.WriteJSON(ProcessEvent{
			Type: ProcessEventTypeInfo,
			Data: fmt.Sprintf("process_id=%s pid=%d", mp.ProcessID, mp.PID),
			Time: time.Now(),
		})
	}

	// Forward events to websocket
	for event := range events {
		if err := conn.WriteJSON(event); err != nil {
			klog.V(2).Infof("websocket write error for process %s: %v", processID, err)
			return
		}
	}
}

// EnvdProcessInputHandler handles POST /envd/process/input
func (s *Server) EnvdProcessInputHandler(c *gin.Context) {
	var req ProcessInputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}

	if err := s.processRegistry.Input(req.ProcessID, req.Data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to send input: %v", err)})
		return
	}

	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

// EnvdProcessCloseStdinHandler handles POST /envd/process/close-stdin
func (s *Server) EnvdProcessCloseStdinHandler(c *gin.Context) {
	var req ProcessCloseStdinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}

	if err := s.processRegistry.CloseStdin(req.ProcessID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to close stdin: %v", err)})
		return
	}

	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

// EnvdProcessSignalHandler handles POST /envd/process/signal
func (s *Server) EnvdProcessSignalHandler(c *gin.Context) {
	var req ProcessSignalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}

	if err := s.processRegistry.Signal(req.ProcessID, req.Signal); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to send signal: %v", err)})
		return
	}

	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

// EnvdProcessListHandler handles GET /envd/process/list
func (s *Server) EnvdProcessListHandler(c *gin.Context) {
	processes := s.processRegistry.List()
	c.JSON(http.StatusOK, gin.H{"processes": processes})
}
