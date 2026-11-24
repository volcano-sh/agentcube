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
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"

	cubelisters "github.com/volcano-sh/agentcube/client-go/listers/runtime/v1alpha1"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
)

const (
	labelE2BEnabled       = "e2b.agentcube.io/enabled"
	annotationAliases     = "e2b.agentcube.io/aliases"
	annotationDescription = "e2b.agentcube.io/description"
	annotationPublic      = "e2b.agentcube.io/public"

	defaultTemplateCPUCount   = 2
	defaultTemplateMemoryMB   = 2048
	defaultTemplateDiskSizeMB = 5120
	templateBuildStatusDone   = "uploaded"
)

var (
	errTemplateNotFound      = errors.New("template not found")
	errTemplateAliasConflict = errors.New("ambiguous template alias")
)

// TemplateResolver resolves and lists opt-in E2B CodeInterpreter templates.
type TemplateResolver struct {
	lister      cubelisters.CodeInterpreterLister
	envdVersion string
}

// NewTemplateResolver creates a TemplateResolver backed by a CodeInterpreter lister.
func NewTemplateResolver(lister cubelisters.CodeInterpreterLister, envdVersion string) *TemplateResolver {
	if envdVersion == "" {
		envdVersion = defaultEnvdVersion
	}
	return &TemplateResolver{lister: lister, envdVersion: envdVersion}
}

// ResolvedTemplate contains minimal metadata resolved from an opt-in CodeInterpreter CRD.
type ResolvedTemplate struct {
	Name         string
	Namespace    string
	PrimaryAlias string
	CRD          *runtimev1alpha1.CodeInterpreter
}

// List returns all opt-in E2B templates in the given namespace.
func (r *TemplateResolver) List(namespace string) ([]Template, error) {
	if r == nil || r.lister == nil {
		return []Template{}, nil
	}
	items, err := r.lister.CodeInterpreters(namespace).List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("list CodeInterpreters in namespace %s: %w", namespace, err)
	}
	out := make([]Template, 0, len(items))
	for _, ci := range items {
		if isE2BEnabled(ci) {
			out = append(out, r.toE2BTemplate(ci))
		}
	}
	return out, nil
}

// Resolve resolves a templateID or alias deterministically within namespace.
func (r *TemplateResolver) Resolve(namespace, idOrAlias string) (*ResolvedTemplate, error) {
	if r == nil || r.lister == nil {
		return nil, errTemplateNotFound
	}
	target := strings.TrimSpace(idOrAlias)
	if target == "" {
		return nil, errTemplateNotFound
	}

	// 1. Exact CRD name match first.
	ci, err := r.lister.CodeInterpreters(namespace).Get(target)
	if err == nil && ci != nil && isE2BEnabled(ci) {
		return toResolvedTemplate(ci), nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get CodeInterpreter %s/%s: %w", namespace, target, err)
	}

	// 2. Scan opt-in CodeInterpreters in namespace for matching alias.
	items, err := r.lister.CodeInterpreters(namespace).List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("list CodeInterpreters in namespace %s: %w", namespace, err)
	}

	var matches []*runtimev1alpha1.CodeInterpreter
	for _, item := range items {
		if !isE2BEnabled(item) {
			continue
		}
		for _, alias := range parseAliases(item) {
			if alias == target {
				matches = append(matches, item)
				break
			}
		}
	}
	if len(matches) == 0 {
		return nil, errTemplateNotFound
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("%w: alias %q matches multiple templates in namespace %s", errTemplateAliasConflict, target, namespace)
	}
	return toResolvedTemplate(matches[0]), nil
}

func (r *TemplateResolver) toE2BTemplate(ci *runtimev1alpha1.CodeInterpreter) Template {
	aliases := parseAliases(ci)
	if aliases == nil {
		aliases = []string{}
	}
	createdAt := ci.CreationTimestamp.Time
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	buildID := string(ci.UID)
	if buildID == "" {
		buildID = ci.Name
	}
	var desc string
	var public bool
	if ci.Annotations != nil {
		desc = ci.Annotations[annotationDescription]
		public = strings.EqualFold(strings.TrimSpace(ci.Annotations[annotationPublic]), "true")
	}
	return Template{
		TemplateID:  ci.Name,
		BuildID:     buildID,
		CPUCount:    defaultTemplateCPUCount,
		MemoryMB:    defaultTemplateMemoryMB,
		DiskSizeMB:  defaultTemplateDiskSizeMB,
		Public:      public,
		Aliases:     aliases,
		Names:       append([]string{ci.Name}, aliases...),
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
		EnvdVersion: r.envdVersion,
		BuildStatus: templateBuildStatusDone,
		Description: desc,
	}
}

func toResolvedTemplate(ci *runtimev1alpha1.CodeInterpreter) *ResolvedTemplate {
	aliases := parseAliases(ci)
	primary := ""
	if len(aliases) > 0 {
		primary = aliases[0]
	}
	return &ResolvedTemplate{
		Name:         ci.Name,
		Namespace:    ci.Namespace,
		PrimaryAlias: primary,
		CRD:          ci,
	}
}

func isE2BEnabled(ci *runtimev1alpha1.CodeInterpreter) bool {
	return ci != nil && ci.Labels != nil && strings.EqualFold(strings.TrimSpace(ci.Labels[labelE2BEnabled]), "true")
}

func parseAliases(ci *runtimev1alpha1.CodeInterpreter) []string {
	if ci == nil || ci.Annotations == nil {
		return nil
	}
	raw := strings.TrimSpace(ci.Annotations[annotationAliases])
	if raw == "" {
		return nil
	}
	var aliases []string
	seen := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		alias := strings.TrimSpace(part)
		if alias == "" {
			continue
		}
		if _, dup := seen[alias]; dup {
			continue
		}
		seen[alias] = struct{}{}
		aliases = append(aliases, alias)
	}
	return aliases
}
