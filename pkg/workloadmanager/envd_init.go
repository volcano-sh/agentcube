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

package workloadmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultEnvdPort    uint32 = 49983
	defaultEnvdVersion        = "0.2.0"
	envdInitTimeout           = 15 * time.Second
	envdInitRetryDelay        = 300 * time.Millisecond
)

type envdInitRequest struct {
	EnvVars     map[string]string `json:"envVars,omitempty"`
	AccessToken string            `json:"accessToken,omitempty"`
	Timestamp   string            `json:"timestamp"`
}

var envdInitHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// initEnvd initializes an envd instance inside a sandbox Pod before it is marked ready.
var initEnvd = func(ctx context.Context, podIP string, port uint32, envVars map[string]string, accessToken string) (string, error) {
	if port == 0 {
		port = defaultEnvdPort
	}
	initURL := fmt.Sprintf("http://%s/init", net.JoinHostPort(podIP, strconv.FormatUint(uint64(port), 10)))

	initCtx, cancel := context.WithTimeout(ctx, envdInitTimeout)
	defer cancel()

	var lastErr error
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(envdInitRetryDelay)
			select {
			case <-initCtx.Done():
				timer.Stop()
				if lastErr != nil {
					return "", fmt.Errorf("envd /init failed: %w", lastErr)
				}
				return "", initCtx.Err()
			case <-timer.C:
			}
		}

		payload := envdInitRequest{
			EnvVars:     envVars,
			AccessToken: accessToken,
			Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		}
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("marshal envd init request: %w", err)
		}

		req, err := http.NewRequestWithContext(initCtx, http.MethodPost, initURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("create envd init request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := envdInitHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			if initCtx.Err() != nil {
				return "", fmt.Errorf("envd /init request failed: %w", lastErr)
			}
			continue
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
			version := resp.Header.Get("X-Envd-Version")
			if version == "" {
				version = defaultEnvdVersion
			}
			return version, nil
		}

		lastErr = fmt.Errorf("envd /init returned status %d: %s", resp.StatusCode, string(respBody))
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			if initCtx.Err() != nil {
				return "", lastErr
			}
			continue
		}
		return "", lastErr
	}
}
