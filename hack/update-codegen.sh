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
CODEGEN_VERSION="v0.36.2"

# Try vendor directory first
if [ -d "vendor/k8s.io/code-generator" ]; then
	CODEGEN_PKG="${SCRIPT_ROOT}/vendor/k8s.io/code-generator"
	echo "Using code-generator from vendor directory"
else
	# Ensure code-generator is downloaded
	echo "Ensuring code-generator@${CODEGEN_VERSION} is available..."
	CODEGEN_JSON=$(go mod download -json "k8s.io/code-generator@${CODEGEN_VERSION}")
	
	# Find code-generator in module cache
	CODEGEN_PKG=$(printf '%s\n' "${CODEGEN_JSON}" | sed -n 's/^[[:space:]]*"Dir": "\([^"]*\)".*/\1/p')
	
	if [ -z "${CODEGEN_PKG}" ] || [ ! -d "${CODEGEN_PKG}" ]; then
		# Try GOPATH/pkg/mod as fallback
		GOPATH=$(go env GOPATH)
		if [ -d "${GOPATH}/pkg/mod/k8s.io/code-generator@${CODEGEN_VERSION}" ]; then
			CODEGEN_PKG="${GOPATH}/pkg/mod/k8s.io/code-generator@${CODEGEN_VERSION}"
		else
			echo "Error: Could not find code-generator@${CODEGEN_VERSION}"
			echo "Please run: go mod download k8s.io/code-generator@${CODEGEN_VERSION}"
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
echo "Generating client-go code for runtime.agentcube.volcano.sh/v1alpha1..."

# Note: We skip gen_helpers because controller-gen in 'make generate' already generates
# the deepcopy code. Using gen_helpers here would delete and regenerate it, causing conflicts.

# Derive install directory for codegen binaries (GOBIN or GOPATH/bin)
CODEGEN_BIN_DIR="$(go env GOBIN)"
if [ -z "${CODEGEN_BIN_DIR}" ]; then
	CODEGEN_BIN_DIR="$(go env GOPATH)/bin"
fi

TMP_CODEGEN="${SCRIPT_ROOT}/.codegen_tmp"
rm -rf "${TMP_CODEGEN}"
mkdir -p "${TMP_CODEGEN}"
cp -r "${CODEGEN_PKG}"/* "${TMP_CODEGEN}/"
chmod -R u+w "${TMP_CODEGEN}"

# Patch informer-gen Windows path bug where path.Base is called on filepath.Join backslash paths
# Use a temporary file instead of sed -i for macOS compatibility, and a targeted non-greedy regex
sed 's/path\.Base(\([^)]*\))/path.Base(filepath.ToSlash(\1))/g' "${TMP_CODEGEN}/cmd/informer-gen/generators/targets.go" > "${TMP_CODEGEN}/cmd/informer-gen/generators/targets.go.tmp"
mv "${TMP_CODEGEN}/cmd/informer-gen/generators/targets.go.tmp" "${TMP_CODEGEN}/cmd/informer-gen/generators/targets.go"

(
  cd "${TMP_CODEGEN}"
  GO111MODULE=on go install ./cmd/client-gen ./cmd/lister-gen ./cmd/informer-gen
)
rm -rf "${TMP_CODEGEN}"

# Validate binaries were installed successfully before cleaning existing client-go
if [ ! -f "${CODEGEN_BIN_DIR}/client-gen" ] || [ ! -f "${CODEGEN_BIN_DIR}/lister-gen" ] || [ ! -f "${CODEGEN_BIN_DIR}/informer-gen" ]; then
	echo "Error: Could not find installed codegen binaries in ${CODEGEN_BIN_DIR}"
	exit 1
fi

# Clean client-go output directory before generation
rm -rf "${SCRIPT_ROOT}/client-go"

NORMALIZED_ROOT=$(echo "${SCRIPT_ROOT}" | sed 's:\\:/:g')

# 1. client-gen
"${CODEGEN_BIN_DIR}/client-gen" \
  --go-header-file "${NORMALIZED_ROOT}/hack/boilerplate.go.txt" \
  --output-dir "./client-go/clientset" \
  --output-pkg "github.com/volcano-sh/agentcube/client-go/clientset" \
  --clientset-name "versioned" \
  --input-base "github.com/volcano-sh/agentcube/pkg/apis" \
  --input "runtime/v1alpha1"

# 2. lister-gen
"${CODEGEN_BIN_DIR}/lister-gen" \
  --go-header-file "${NORMALIZED_ROOT}/hack/boilerplate.go.txt" \
  --output-dir "./client-go/listers" \
  --output-pkg "github.com/volcano-sh/agentcube/client-go/listers" \
  "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"

# 3. informer-gen
"${CODEGEN_BIN_DIR}/informer-gen" \
  --go-header-file "${NORMALIZED_ROOT}/hack/boilerplate.go.txt" \
  --output-dir "./client-go/informers" \
  --output-pkg "github.com/volcano-sh/agentcube/client-go/informers" \
  --versioned-clientset-package "github.com/volcano-sh/agentcube/client-go/clientset/versioned" \
  --listers-package "github.com/volcano-sh/agentcube/client-go/listers" \
  "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"

echo "Patching generated listers to use .GroupResource() on the Resource() helper..."
for f in ./client-go/listers/runtime/v1alpha1/*.go; do
  sed 's/Resource(\([^)]*\)))/Resource(\1).GroupResource())/g' "$f" > "$f.tmp"
  mv "$f.tmp" "$f"
done

echo "Client-go code generation completed!"
