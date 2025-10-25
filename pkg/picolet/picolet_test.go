package picolet

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDeleteSandboxHandler(t *testing.T) {
	testScheme := runtime.NewScheme()
	utilruntime.Must(scheme.AddToScheme(testScheme))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(testScheme))

	// create fake sandbox client
	sandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "default",
		},
		Spec: sandboxv1alpha1.SandboxSpec{
			PodTemplate: sandboxv1alpha1.PodTemplate{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "test-image",
						},
					},
				},
			},
		},
	}
	// create fake client with the sandbox object
	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(sandbox).
		Build()

	picolet := &Picolet{
		sandboxClient: fakeClient,
		router:        mux.NewRouter(),
	}
	picolet.setupRoutes()

	// Test request for successful deletion
	t.Run("ValidDeleteRequest", func(t *testing.T) {
		requestBody := `{"Name": "test-sandbox", "namespace": "default"}`
		req := httptest.NewRequest("POST", "/delete", bytes.NewBufferString(requestBody))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		picolet.router.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
		}

		var response SandboxResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Errorf("failed to parse response: %v", err)
		}

		if !response.Success {
			t.Errorf("expected success=true, got success=false")
		}
	})

	// invalid request body test
	t.Run("InvalidRequestMissingFields", func(t *testing.T) {
		requestBody := `{"Name": ""}`
		req := httptest.NewRequest("POST", "/delete", bytes.NewBufferString(requestBody))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		picolet.router.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusBadRequest {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
		}
	})

	// test deletion of non-existent sandbox
	t.Run("NonExistentSandbox", func(t *testing.T) {
		requestBody := `{"Name": "non-existent", "namespace": "default"}`
		req := httptest.NewRequest("POST", "/delete", bytes.NewBufferString(requestBody))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		picolet.router.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusInternalServerError {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusInternalServerError)
		}
	})
}
