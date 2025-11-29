#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
cd "${SCRIPT_ROOT}"

# Set up environment
export GO111MODULE=on

# Find code-generator
CODEGEN_PKG=""
CODEGEN_VERSION="v0.34.1"

# Try vendor directory first
if [ -d "vendor/k8s.io/code-generator" ]; then
	CODEGEN_PKG="${SCRIPT_ROOT}/vendor/k8s.io/code-generator"
	echo "Using code-generator from vendor directory"
else
	# Ensure code-generator is downloaded
	echo "Ensuring code-generator@${CODEGEN_VERSION} is available..."
	go get -d "k8s.io/code-generator@${CODEGEN_VERSION}" || true
	
	# Find code-generator in module cache
	CODEGEN_PKG=$(go list -m -f '{{.Dir}}' "k8s.io/code-generator@${CODEGEN_VERSION}" 2>/dev/null || echo "")
	
	if [ -z "${CODEGEN_PKG}" ] || [ ! -d "${CODEGEN_PKG}" ]; then
		# Try GOPATH/pkg/mod as fallback
		GOPATH=$(go env GOPATH)
		if [ -d "${GOPATH}/pkg/mod/k8s.io/code-generator@${CODEGEN_VERSION}" ]; then
			CODEGEN_PKG="${GOPATH}/pkg/mod/k8s.io/code-generator@${CODEGEN_VERSION}"
		else
			echo "Error: Could not find code-generator@${CODEGEN_VERSION}"
			echo "Please run: go get k8s.io/code-generator@${CODEGEN_VERSION}"
			exit 1
		fi
	fi
	echo "Using code-generator from: ${CODEGEN_PKG}"
fi

if [ ! -f "${CODEGEN_PKG}/kube_codegen.sh" ]; then
	echo "Error: kube_codegen.sh not found in ${CODEGEN_PKG}"
	exit 1
fi

# Source kube_codegen.sh to get the functions
source "${CODEGEN_PKG}/kube_codegen.sh"

# Generate the code
echo "Generating client-go code for runtime.agentcube.io/v1alpha1..."

# Generate helpers (deepcopy, defaulter, etc.)
# Note: input-dir must be a local path, not a Go package path
kube::codegen::gen_helpers \
  --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  "${SCRIPT_ROOT}/pkg/apis"

# Generate client code
# Note: input-dir must be a local path, not a Go package path
kube::codegen::gen_client \
  --with-watch \
  --output-dir "${SCRIPT_ROOT}/client-go" \
  --output-pkg github.com/volcano-sh/agentcube/client-go \
  --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  --one-input-api runtime/v1alpha1 \
  "${SCRIPT_ROOT}/pkg/apis"

# Fix lister-gen bug: Resource() returns GroupVersionResource but listers.New needs GroupResource
# This is a workaround for https://github.com/kubernetes/code-generator/issues/XXX
echo "Fixing lister-gen GroupResource issue..."
find "${SCRIPT_ROOT}/client-go/listers" -name "*.go" -type f -exec sed -i 's/runtimev1alpha1\.Resource("codeinterpreter")/runtimev1alpha1.Resource("codeinterpreter").GroupResource()/g' {} \;

echo "Client-go code generation completed!"
