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

package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"k8s.io/klog/v2"
)

// CertWatcher watches certificate and CA files and reloads them on change.
// It provides GetCertificate and GetCAPool callbacks for use with tls.Config,
// enabling zero-downtime rotation for both identity certificates and trust bundles.
type CertWatcher struct {
	certFile string
	keyFile  string
	caFile   string // optional; empty if no CA watching needed

	mu     sync.RWMutex
	cert   *tls.Certificate
	caPool *x509.CertPool // nil if caFile is empty
	once   sync.Once

	watcher *fsnotify.Watcher
	done    chan struct{}
}

// NewCertWatcher creates a CertWatcher that monitors the given cert/key files
// and optionally a CA bundle file. It loads the initial certificate (and CA pool
// if caFile is non-empty) and starts watching for changes.
func NewCertWatcher(certFile, keyFile, caFile string) (*CertWatcher, error) {
	cw := &CertWatcher{
		certFile: certFile,
		keyFile:  keyFile,
		caFile:   caFile,
		done:     make(chan struct{}),
	}

	// Load initial certificate
	if err := cw.reload(); err != nil {
		return nil, fmt.Errorf("initial cert load: %w", err)
	}

	// Load initial CA pool if configured
	if caFile != "" {
		if err := cw.reloadCA(); err != nil {
			return nil, fmt.Errorf("initial CA load: %w", err)
		}
	}

	// Setup file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create file watcher: %w", err)
	}
	cw.watcher = watcher

	// Watch the cert file (key file is typically updated atomically with cert)
	if err := watcher.Add(certFile); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("watch cert file %s: %w", certFile, err)
	}
	if err := watcher.Add(keyFile); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("watch key file %s: %w", keyFile, err)
	}
	if caFile != "" {
		if err := watcher.Add(caFile); err != nil {
			watcher.Close()
			return nil, fmt.Errorf("watch CA file %s: %w", caFile, err)
		}
	}

	go cw.watchLoop()
	return cw, nil
}

// GetCertificate returns the current certificate. Safe for concurrent use.
// This is intended as the tls.Config.GetCertificate callback.
func (cw *CertWatcher) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.cert, nil
}

// GetClientCertificate returns the current certificate for client TLS.
// This is intended as the tls.Config.GetClientCertificate callback.
func (cw *CertWatcher) GetClientCertificate(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.cert, nil
}

// GetCAPool returns the current CA certificate pool. Safe for concurrent use.
// Returns nil if no CA file is being watched.
func (cw *CertWatcher) GetCAPool() *x509.CertPool {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.caPool
}

// Stop stops the file watcher. Safe to call multiple times.
func (cw *CertWatcher) Stop() {
	cw.once.Do(func() {
		close(cw.done)
		if cw.watcher != nil {
			cw.watcher.Close()
		}
	})
}

// reload must not be called while cw.mu is held.
func (cw *CertWatcher) reload() error {
	cert, err := tls.LoadX509KeyPair(cw.certFile, cw.keyFile)
	if err != nil {
		return err
	}
	cw.mu.Lock()
	cw.cert = &cert
	cw.mu.Unlock()
	klog.V(2).Infof("Reloaded TLS certificate from %s", cw.certFile)
	return nil
}

// reloadCA reloads the CA bundle from disk. Must not be called while cw.mu is held.
func (cw *CertWatcher) reloadCA() error {
	caCert, err := os.ReadFile(cw.caFile)
	if err != nil {
		return fmt.Errorf("read CA file %s: %w", cw.caFile, err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("no valid CA certificates found in %s", cw.caFile)
	}
	cw.mu.Lock()
	cw.caPool = caPool
	cw.mu.Unlock()
	klog.V(2).Infof("Reloaded CA bundle from %s", cw.caFile)
	return nil
}

// reloadAll reloads the cert/key pair and (if configured) the CA bundle.
func (cw *CertWatcher) reloadAll() {
	if err := cw.reload(); err != nil {
		klog.Errorf("Failed to reload certificate: %v", err)
	}
	if cw.caFile != "" {
		if err := cw.reloadCA(); err != nil {
			klog.Errorf("Failed to reload CA bundle: %v", err)
		}
	}
}

func (cw *CertWatcher) watchLoop() {
	var debounceTimer *time.Timer
	scheduleReload := func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		debounceTimer = time.AfterFunc(200*time.Millisecond, func() {
			select {
			case <-cw.done:
				return
			default:
			}
			cw.reloadAll()
		})
	}

	defer func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
	}()

	for {
		select {
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			// Reload on write or create
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				scheduleReload()
			}
			// Re-watch after atomic rename (spiffe-helper does atomic renames for both cert and key)
			if event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove) {
				cw.handleRenameEvent(event.Name)
				scheduleReload()
			}
		case err, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
			klog.Errorf("Certificate watcher error: %v", err)
		case <-cw.done:
			return
		}
	}
}

// handleRenameEvent manages the retry loop for re-watching files after atomic renames.
func (cw *CertWatcher) handleRenameEvent(targetFile string) {
	_ = cw.watcher.Remove(targetFile)

	// Run the retry loop in a background goroutine to prevent blocking the watchLoop
	// which could cause fsnotify buffers to overflow during long backoffs.
	go func() {
		delay := 100 * time.Millisecond
		for i := 0; i < 5; i++ {
			// Exit early if the watcher is intentionally stopped
			select {
			case <-cw.done:
				return
			default:
			}

			if err := cw.watcher.Add(targetFile); err == nil {
				return // Success
			}
			klog.V(4).Infof("Failed to re-watch %s (attempt %d/5). Retrying in %v...", targetFile, i+1, delay)

			// Wait for delay, or exit immediately if stopped
			select {
			case <-cw.done:
				return
			case <-time.After(delay):
			}
			delay *= 2 // Exponential backoff (100, 200, 400, 800, 1600ms)
		}

		klog.Errorf("CRITICAL: Exhausted retry budget attempting to re-watch %s. Certificate rotation is permanently broken! Process restart required.", targetFile)
	}()
}
