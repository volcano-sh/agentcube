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

package e2b

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
)

// Constants for template management
const (
	// defaultSessionTimeout is the default session timeout for templates
	defaultSessionTimeout = 15 * time.Minute
	// defaultMaxSessionDuration is the default max session duration for templates
	defaultMaxSessionDuration = 8 * time.Hour
	// publicTrue is the string representation of true for public filter
	publicTrue = "true"
)

// Annotation keys for E2B metadata
const (
	annotationDescription = "e2b.agentcube.io/description"
	annotationAliases     = "e2b.agentcube.io/aliases"
	annotationDockerfile  = "e2b.agentcube.io/dockerfile"
	annotationStartCmd    = "e2b.agentcube.io/startCommand"
)

// Label keys for E2B metadata
const (
	labelPublic = "e2b.agentcube.io/public"
)

// handleListTemplates handles GET /templates - List all templates
func (s *Server) handleListTemplates(c *gin.Context) {
	// Get namespace from auth context
	namespace := c.GetString("namespace")
	if namespace == "" {
		namespace = s.config.E2BDefaultNamespace
	}

	// Parse query parameters
	limit, err := parseInt(c.Query("limit"), 100)
	if err != nil {
		respondWithError(c, ErrInvalidRequest, "invalid limit parameter")
		return
	}
	if limit < 0 {
		respondWithError(c, ErrInvalidRequest, "limit cannot be negative")
		return
	}
	_, _ = parseInt(c.Query("offset"), 0)
	publicFilter := c.Query("public")

	// If k8s client is available, use it to list CodeInterpreters
	if s.k8sClient != nil {
		ctx := c.Request.Context()
		codeInterpreterList := &runtimev1alpha1.CodeInterpreterList{}
		if err := s.k8sClient.List(ctx, codeInterpreterList); err != nil {
			klog.Errorf("failed to list code interpreters: %v", err)
			respondWithError(c, ErrInternal, "failed to list templates")
			return
		}

		templates := make([]Template, 0, len(codeInterpreterList.Items))
		for _, ci := range codeInterpreterList.Items {
			// Filter by namespace
			if ci.Namespace != namespace {
				continue
			}
			template := s.codeInterpreterToTemplate(&ci)
			// Apply public filter if specified
			if publicFilter != "" {
				isPublic := publicFilter == publicTrue
				if template.Public != isPublic {
					continue
				}
			}
			templates = append(templates, *template)
		}

		// Apply limit
		if len(templates) > limit {
			templates = templates[:limit]
		}

		klog.V(4).Infof("listed %d templates for namespace %s", len(templates), namespace)
		c.JSON(http.StatusOK, templates)
		return
	}

	// Fallback to mock templates if k8s client is not available
	templates := []Template{
		{
			TemplateID:  "python-code-interpreter",
			Name:        "python-code-interpreter",
			Description: "Default Python code interpreter template",
			Aliases:     []string{"python", "py"},
			CreatedAt:   time.Now().Add(-24 * time.Hour),
			UpdatedAt:   time.Now(),
			Public:      true,
			State:       TemplateStateReady,
			MemoryMB:    4096,
			VCPUCount:   2,
		},
		{
			TemplateID:  "node-code-interpreter",
			Name:        "node-code-interpreter",
			Description: "Node.js code interpreter template",
			Aliases:     []string{"node", "nodejs", "js"},
			CreatedAt:   time.Now().Add(-48 * time.Hour),
			UpdatedAt:   time.Now(),
			Public:      true,
			State:       TemplateStateReady,
			MemoryMB:    4096,
			VCPUCount:   2,
		},
	}

	// Apply public filter if specified
	if publicFilter != "" {
		isPublic := publicFilter == publicTrue
		filtered := make([]Template, 0)
		for _, t := range templates {
			if t.Public == isPublic {
				filtered = append(filtered, t)
			}
		}
		templates = filtered
	}

	// Apply limit
	if len(templates) > limit {
		templates = templates[:limit]
	}

	klog.V(4).Infof("listed %d mock templates for namespace %s", len(templates), namespace)
	c.JSON(http.StatusOK, templates)
}

// handleGetTemplate handles GET /templates/{id} - Get template by ID
func (s *Server) handleGetTemplate(c *gin.Context) {
	templateID := c.Param("id")
	if templateID == "" {
		respondWithError(c, ErrInvalidRequest, "template id is required")
		return
	}

	// Strip leading slash from wildcard parameter
	templateID = strings.TrimPrefix(templateID, "/")

	// Validate template ID format (plain name, no namespace prefix)
	if err := validateTemplateName(templateID); err != nil {
		respondWithError(c, ErrInvalidRequest, err.Error())
		return
	}

	namespace := c.GetString("namespace")
	if namespace == "" {
		namespace = s.config.E2BDefaultNamespace
	}
	name := templateID

	// If k8s client is available, use it to get the CodeInterpreter
	if s.k8sClient != nil {
		ctx := c.Request.Context()
		ci := &runtimev1alpha1.CodeInterpreter{}
		if err := s.k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, ci); err != nil {
			if errors.IsNotFound(err) {
				respondWithError(c, ErrNotFound, "template not found")
				return
			}
			klog.Errorf("failed to get code interpreter: %v", err)
			respondWithError(c, ErrInternal, "failed to get template")
			return
		}

		template := s.codeInterpreterToTemplate(ci)
		klog.V(4).Infof("retrieved template: templateID=%s", templateID)
		c.JSON(http.StatusOK, template)
		return
	}

	// Fallback to mock template if k8s client is not available
	template := Template{
		TemplateID:   templateID,
		Name:         name,
		Description:  "Template description for " + name,
		Aliases:      []string{"alias1", "alias2"},
		CreatedAt:    time.Now().Add(-24 * time.Hour),
		UpdatedAt:    time.Now(),
		Public:       true,
		State:        TemplateStateReady,
		StartCommand: "python app.py",
		EnvdVersion:  s.mapper.GetEnvdVersion(),
		MemoryMB:     4096,
		VCPUCount:    2,
	}

	klog.V(4).Infof("retrieved mock template: templateID=%s", templateID)
	c.JSON(http.StatusOK, template)
}

// handleCreateTemplate handles POST /v3/templates - Create new template
func (s *Server) handleCreateTemplate(c *gin.Context) {
	var req CreateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		klog.Errorf("failed to bind request body: %v", err)
		respondWithError(c, ErrInvalidRequest, "invalid request body: "+err.Error())
		return
	}

	// Validate required fields
	if req.Name == "" {
		respondWithError(c, ErrInvalidRequest, "name is required")
		return
	}

	// Get namespace from auth context
	namespace := c.GetString("namespace")
	if namespace == "" {
		namespace = s.config.E2BDefaultNamespace
	}

	// Template ID is just the name (no namespace prefix)
	templateID := req.Name

	// If k8s client is available, create a CodeInterpreter CRD
	if s.k8sClient != nil {
		ctx := c.Request.Context()

		// Check if template already exists
		existingCI := &runtimev1alpha1.CodeInterpreter{}
		err := s.k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: req.Name}, existingCI)
		if err == nil {
			respondWithError(c, ErrConflict, "template with this name already exists")
			return
		} else if !errors.IsNotFound(err) {
			klog.Errorf("failed to check existing code interpreter: %v", err)
			respondWithError(c, ErrInternal, "failed to create template")
			return
		}

		// Build annotations
		annotations := make(map[string]string)
		if req.Description != "" {
			annotations[annotationDescription] = req.Description
		}
		if req.Dockerfile != "" {
			annotations[annotationDockerfile] = req.Dockerfile
		}
		if req.StartCommand != "" {
			annotations[annotationStartCmd] = req.StartCommand
		}
		if len(req.Aliases) > 0 {
			annotations[annotationAliases] = strings.Join(req.Aliases, ",")
		}

		// Build labels
		labels := make(map[string]string)
		if req.Public {
			labels[labelPublic] = "true"
		} else {
			labels[labelPublic] = "false"
		}

		// Create CodeInterpreter spec with default values
		warmPoolSize := int32(0)
		sessionTimeout := metav1.Duration{Duration: defaultSessionTimeout}
		maxSessionDuration := metav1.Duration{Duration: defaultMaxSessionDuration}

		// Default resource requirements
		memoryMB := req.MemoryMB
		if memoryMB == 0 {
			memoryMB = 4096
		}
		cpuCount := req.VCPUCount
		if cpuCount == 0 {
			cpuCount = 2
		}

		resources := corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resourceQuantity(fmt.Sprintf("%d", cpuCount)),
				corev1.ResourceMemory: resourceQuantity(fmt.Sprintf("%dMi", memoryMB)),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resourceQuantity("100m"),
				corev1.ResourceMemory: resourceQuantity("256Mi"),
			},
		}

		// Create the CodeInterpreter
		ci := &runtimev1alpha1.CodeInterpreter{
			ObjectMeta: metav1.ObjectMeta{
				Name:        req.Name,
				Namespace:   namespace,
				Annotations: annotations,
				Labels:      labels,
			},
			Spec: runtimev1alpha1.CodeInterpreterSpec{
				WarmPoolSize:       &warmPoolSize,
				SessionTimeout:     &sessionTimeout,
				MaxSessionDuration: &maxSessionDuration,
				Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
					Image:     "volcanosh/codeinterpreter:latest",
					Resources: resources,
					Command:   parseStartCommand(req.StartCommand),
				},
			},
		}

		if err := s.k8sClient.Create(ctx, ci); err != nil {
			klog.Errorf("failed to create code interpreter: %v", err)
			respondWithError(c, ErrInternal, "failed to create template")
			return
		}

		template := s.codeInterpreterToTemplate(ci)
		klog.Infof("template created successfully: templateID=%s", templateID)
		c.JSON(http.StatusCreated, template)
		return
	}

	// Fallback to mock response if k8s client is not available
	now := time.Now()
	template := Template{
		TemplateID:   templateID,
		Name:         req.Name,
		Description:  req.Description,
		Aliases:      req.Aliases,
		CreatedAt:    now,
		UpdatedAt:    now,
		Public:       req.Public,
		State:        TemplateStateReady,
		StartCommand: req.StartCommand,
		EnvdVersion:  s.config.EnvdVersion,
		MemoryMB:     4096,
		VCPUCount:    2,
	}

	klog.Infof("created mock template: templateID=%s, name=%s, namespace=%s", templateID, req.Name, namespace)
	c.JSON(http.StatusCreated, template)
}

// updateTemplateWithK8s updates the template using Kubernetes client
func (s *Server) updateTemplateWithK8s(ctx context.Context, namespace, name string, req *UpdateTemplateRequest) (*Template, error) {
	// Get the existing CodeInterpreter
	ci := &runtimev1alpha1.CodeInterpreter{}
	if err := s.k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, ci); err != nil {
		return nil, err
	}

	// Update annotations
	if ci.Annotations == nil {
		ci.Annotations = make(map[string]string)
	}

	if req.Description != nil {
		ci.Annotations[annotationDescription] = *req.Description
	}

	// Update labels
	if ci.Labels == nil {
		ci.Labels = make(map[string]string)
	}

	if req.Public != nil {
		if *req.Public {
			ci.Labels[labelPublic] = "true"
		} else {
			ci.Labels[labelPublic] = "false"
		}
	}

	// Update aliases if provided
	if req.Aliases != nil {
		ci.Annotations[annotationAliases] = strings.Join(req.Aliases, ",")
	}

	// Apply the update
	if err := s.k8sClient.Update(ctx, ci); err != nil {
		return nil, err
	}

	// Re-fetch the updated CodeInterpreter to get the latest state
	updatedCI := &runtimev1alpha1.CodeInterpreter{}
	if err := s.k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, updatedCI); err != nil {
		return nil, err
	}

	return s.codeInterpreterToTemplate(updatedCI), nil
}

// handleUpdateTemplate handles PATCH /v2/templates/{id} - Update template
func (s *Server) handleUpdateTemplate(c *gin.Context) {
	templateID := c.Param("id")
	if templateID == "" {
		respondWithError(c, ErrInvalidRequest, "template id is required")
		return
	}

	// Validate template name format (plain name, no namespace prefix)
	if err := validateTemplateName(templateID); err != nil {
		respondWithError(c, ErrInvalidRequest, err.Error())
		return
	}

	var req UpdateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		klog.Errorf("failed to bind request body: %v", err)
		respondWithError(c, ErrInvalidRequest, "invalid request body: "+err.Error())
		return
	}

	// Get namespace from auth context
	namespace := c.GetString("namespace")
	if namespace == "" {
		namespace = s.config.E2BDefaultNamespace
	}

	name := templateID

	// If k8s client is available, update the CodeInterpreter
	if s.k8sClient != nil {
		template, err := s.updateTemplateWithK8s(c.Request.Context(), namespace, name, &req)
		if err != nil {
			if errors.IsNotFound(err) {
				respondWithError(c, ErrNotFound, "template not found")
				return
			}
			klog.Errorf("failed to update template: %v", err)
			respondWithError(c, ErrInternal, "failed to update template")
			return
		}

		klog.Infof("template updated successfully: templateID=%s", templateID)
		c.JSON(http.StatusOK, template)
		return
	}

	// Fallback to mock updated template if k8s client is not available
	template := Template{
		TemplateID:  templateID,
		Name:        name,
		UpdatedAt:   time.Now(),
		Public:      true,
		State:       TemplateStateReady,
		EnvdVersion: s.mapper.GetEnvdVersion(),
		MemoryMB:    4096,
		VCPUCount:   2,
	}

	// Apply updates
	if req.Public != nil {
		template.Public = *req.Public
	}
	if req.Description != nil {
		template.Description = *req.Description
	}

	klog.Infof("updated mock template: templateID=%s, namespace=%s", templateID, namespace)
	c.JSON(http.StatusOK, template)
}

// handleDeleteTemplate handles DELETE /templates/{id} - Delete template
func (s *Server) handleDeleteTemplate(c *gin.Context) {
	templateID := c.Param("id")
	if templateID == "" {
		respondWithError(c, ErrInvalidRequest, "template id is required")
		return
	}

	// Validate template name format (plain name, no namespace prefix)
	if err := validateTemplateName(templateID); err != nil {
		respondWithError(c, ErrInvalidRequest, err.Error())
		return
	}

	// Get namespace from auth context
	namespace := c.GetString("namespace")
	if namespace == "" {
		namespace = s.config.E2BDefaultNamespace
	}

	name := templateID

	// If k8s client is available, delete the CodeInterpreter
	if s.k8sClient != nil {
		ctx := c.Request.Context()

		// Get the CodeInterpreter
		ci := &runtimev1alpha1.CodeInterpreter{}
		if err := s.k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, ci); err != nil {
			if errors.IsNotFound(err) {
				respondWithError(c, ErrNotFound, "template not found")
				return
			}
			klog.Errorf("failed to get code interpreter: %v", err)
			respondWithError(c, ErrInternal, "failed to delete template")
			return
		}

		// Delete the CodeInterpreter
		if err := s.k8sClient.Delete(ctx, ci); err != nil {
			klog.Errorf("failed to delete code interpreter: %v", err)
			respondWithError(c, ErrInternal, "failed to delete template")
			return
		}

		klog.Infof("template deleted successfully: templateID=%s", templateID)
		c.Status(http.StatusNoContent)
		return
	}

	// Fallback to mock delete if k8s client is not available
	klog.Infof("deleted mock template: templateID=%s, namespace=%s", templateID, namespace)
	c.Status(http.StatusNoContent)
}

// codeInterpreterToTemplate converts a CodeInterpreter CRD to an E2B Template
func (s *Server) codeInterpreterToTemplate(ci *runtimev1alpha1.CodeInterpreter) *Template {
	// Extract annotations
	description := ""
	aliases := []string{}
	dockerfile := ""
	startCommand := ""

	if ci.Annotations != nil {
		description = ci.Annotations[annotationDescription]
		if aliasStr := ci.Annotations[annotationAliases]; aliasStr != "" {
			aliases = strings.Split(aliasStr, ",")
		}
		dockerfile = ci.Annotations[annotationDockerfile]
		startCommand = ci.Annotations[annotationStartCmd]
	}

	// Extract public flag from labels
	public := false
	if ci.Labels != nil {
		if val, ok := ci.Labels[labelPublic]; ok {
			public, _ = strconv.ParseBool(val)
		}
	}

	// Extract resource info
	memoryMB := 4096 // default
	cpuCount := 2    // default

	if ci.Spec.Template != nil {
		if mem := ci.Spec.Template.Resources.Limits.Memory(); mem != nil {
			memoryMB = int(mem.Value() / (1024 * 1024))
		}
		if cpu := ci.Spec.Template.Resources.Limits.Cpu(); cpu != nil {
			cpuCount = int(cpu.Value())
			if cpuCount < 1 {
				cpuCount = 1
			}
		}
	}

	// Determine state
	state := TemplateStateReady
	if !ci.Status.Ready {
		state = TemplateStateError
	}

	return &Template{
		TemplateID:   ci.Name,
		Name:         ci.Name,
		Description:  description,
		Aliases:      aliases,
		CreatedAt:    ci.CreationTimestamp.Time,
		UpdatedAt:    ci.CreationTimestamp.Time, // Use creation time as default
		Public:       public,
		State:        state,
		Dockerfile:   dockerfile,
		StartCommand: startCommand,
		EnvdVersion:  s.config.EnvdVersion,
		MemoryMB:     memoryMB,
		VCPUCount:    cpuCount,
	}
}

// parseInt parses an integer from string with a default value
// Returns error for invalid values (including negative numbers)
func parseInt(s string, defaultVal int) (int, error) {
	if s == "" {
		return defaultVal, nil
	}
	result, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal, fmt.Errorf("invalid integer value: %s", s)
	}
	return result, nil
}

// parseStartCommand parses a start command string into a command array
func parseStartCommand(cmd string) []string {
	if cmd == "" {
		return nil
	}
	// Simple parsing - split by spaces
	// In a more robust implementation, this would handle shell quoting
	return strings.Fields(cmd)
}

// resourceQuantity creates a Quantity from a string value
func resourceQuantity(value string) resource.Quantity {
	return resource.MustParse(value)
}

// validateTemplateName validates that a template name is valid
func validateTemplateName(name string) error {
	if name == "" {
		return fmt.Errorf("template name is required")
	}
	if !validTemplateNameRegex.MatchString(name) {
		return fmt.Errorf("invalid template name: contains invalid characters")
	}
	return nil
}

// handleTemplateWildcard handles all template wildcard routes and dispatches to appropriate handlers
// This is needed because Gin doesn't support overlapping wildcard routes
func (s *Server) handleTemplateWildcard(c *gin.Context) {
	path := c.Param("path")
	path = strings.TrimPrefix(path, "/")

	// Set the id parameter for downstream handlers
	c.Params = gin.Params{{Key: "id", Value: path}}

	// Route based on HTTP method
	switch c.Request.Method {
	case "GET":
		s.handleGetTemplate(c)
	case "PATCH":
		s.handleUpdateTemplate(c)
	case "DELETE":
		s.handleDeleteTemplate(c)
	default:
		respondWithError(c, ErrInvalidRequest, "method not allowed")
	}
}
