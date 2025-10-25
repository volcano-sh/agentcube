package picolet

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Picolet struct {
	listenAddr    string
	kubeClient    kubernetes.Interface
	sandboxClient client.Client
	router        *mux.Router
	httpServer    *http.Server
}

type SandboxRequest struct {
	Name      string `json:"Name"`
	Namespace string `json:"namespace"`
}

type SandboxResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

func NewPicolet(listenAddr, kubeconfig string) (*Picolet, error) {
	config, err := getClientConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	schemeBuilder := runtime.NewScheme()
	utilruntime.Must(scheme.AddToScheme(schemeBuilder))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(schemeBuilder))
	sandboxClient, err := client.New(config, client.Options{Scheme: schemeBuilder})
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox client: %w", err)
	}

	picolet := &Picolet{
		listenAddr:    listenAddr,
		kubeClient:    clientset,
		sandboxClient: sandboxClient,
		router:        mux.NewRouter(),
	}

	picolet.setupRoutes()

	picolet.httpServer = &http.Server{
		Addr:    listenAddr,
		Handler: picolet.router,
	}

	return picolet, nil
}

func getClientConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		return rest.InClusterConfig()
	}

	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func (p *Picolet) setupRoutes() {
	p.router.HandleFunc("/delete", p.handleSandboxDelete).Methods("POST")
}

func (p *Picolet) Start(ctx context.Context) error {
	log.Printf("Starting picolet on %s", p.listenAddr)

	errCh := make(chan error, 1)
	go func() {
		if err := p.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Println("Shutting down picolet...")
		return p.httpServer.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}

func (p *Picolet) handleSandboxDelete(w http.ResponseWriter, r *http.Request) {
	var req SandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Namespace == "" {
		http.Error(w, "sandboxName and namespace are required", http.StatusBadRequest)
		return
	}

	success, message := p.deleteSandbox(req.Namespace, req.Name)

	response := SandboxResponse{
		Success: success,
		Message: message,
	}

	w.Header().Set("Content-Type", "application/json")
	if success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	json.NewEncoder(w).Encode(response)
}

func (p *Picolet) deleteSandbox(namespace, name string) (bool, string) {
	sandbox := &sandboxv1alpha1.Sandbox{}
	if err := p.sandboxClient.Get(context.TODO(), client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, sandbox); err != nil {
		return false, fmt.Sprintf("Failed to get sandbox: %v", err)
	}

	if err := p.sandboxClient.Delete(context.TODO(), sandbox); err != nil {
		return false, fmt.Sprintf("Failed to delete sandbox: %v", err)
	}

	log.Printf("Pausing sandbox %s/%s", namespace, name)

	return true, "Sandbox paused successfully"
}
