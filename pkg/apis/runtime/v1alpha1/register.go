package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// CodeInterpreter type metadata.
var (
	CodeInterpreterKind             = "CodeInterpreter"
	CodeInterpreterGroupKind        = GroupVersion.WithKind("CodeInterpreter")
	CodeInterpreterListKind         = "CodeInterpreterList"
	CodeInterpreterGroupVersionKind = GroupVersion.WithKind("CodeInterpreter")
)

// SchemeGroupVersion is group version used to register these objects
var SchemeGroupVersion = GroupVersion

// Resource takes an unqualified resource and returns a Group qualified GroupResource
func Resource(resource string) schema.GroupVersionResource {
	return GroupVersion.WithResource(resource)
}
