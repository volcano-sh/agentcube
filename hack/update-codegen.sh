#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

# Prerequisite: ensure `go` is available on PATH before running this script.

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
cd "${SCRIPT_ROOT}"

# Set up environment
export GO111MODULE=on

# Find code-generator
CODEGEN_PKG=""
CODEGEN_VERSION="v0.35.4"

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

# Ensure codegen binaries are installed in a known bin dir (honor GOBIN if set)
GOPATH_BIN="$(go env GOBIN)"
if [ -z "${GOPATH_BIN}" ]; then
	GOPATH_RAW="$(go env GOPATH)"
	GOPATH_FIRST="${GOPATH_RAW%%[:;]*}"
	GOPATH_BIN="${GOPATH_FIRST}/bin"
fi
mkdir -p "${GOPATH_BIN}"
GOEXE="$(go env GOEXE)"
TMP_CODEGEN="${SCRIPT_ROOT}/.codegen_tmp"
rm -rf "${TMP_CODEGEN}"
mkdir -p "${TMP_CODEGEN}"
trap 'rm -rf "${TMP_CODEGEN}"' EXIT
cp -r "${CODEGEN_PKG}"/* "${TMP_CODEGEN}/"
chmod -R u+w "${TMP_CODEGEN}"

# Patch informer-gen Windows path bug where path.Base is called on filepath.Join backslash paths
TARGETS_GO="${TMP_CODEGEN}/cmd/informer-gen/generators/targets.go"
if [ -f "${TARGETS_GO}" ]; then
	if [[ "${OSTYPE:-}" == "darwin"* ]]; then
		sed -i '' 's/path\.Base(\(.*\))/path.Base(filepath.ToSlash(\1))/g' "${TARGETS_GO}"
	else
		sed -i 's/path\.Base(\(.*\))/path.Base(filepath.ToSlash(\1))/g' "${TARGETS_GO}"
	fi
else
	echo "Warning: ${TARGETS_GO} not found; skipping informer-gen Windows path patch" >&2
fi

(
  cd "${TMP_CODEGEN}"
  GO111MODULE=on GOBIN="${GOPATH_BIN}" go install ./cmd/client-gen ./cmd/lister-gen ./cmd/informer-gen
)
rm -rf "${TMP_CODEGEN}"

# Clean client-go output directory before generation
rm -rf "${SCRIPT_ROOT}/client-go"

NORMALIZED_ROOT=$(echo "${SCRIPT_ROOT}" | sed 's:\\:/:g')

# 1. client-gen
"${GOPATH_BIN}/client-gen${GOEXE}" \
  --go-header-file "${NORMALIZED_ROOT}/hack/boilerplate.go.txt" \
  --output-dir "./client-go/clientset" \
  --output-pkg "github.com/volcano-sh/agentcube/client-go/clientset" \
  --clientset-name "versioned" \
  --input-base "github.com/volcano-sh/agentcube/pkg/apis" \
  --input "runtime/v1alpha1"

# 2. lister-gen
"${GOPATH_BIN}/lister-gen${GOEXE}" \
  --go-header-file "${NORMALIZED_ROOT}/hack/boilerplate.go.txt" \
  --output-dir "./client-go/listers" \
  --output-pkg "github.com/volcano-sh/agentcube/client-go/listers" \
  "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"

# 3. informer-gen
"${GOPATH_BIN}/informer-gen${GOEXE}" \
  --go-header-file "${NORMALIZED_ROOT}/hack/boilerplate.go.txt" \
  --output-dir "./client-go/informers" \
  --output-pkg "github.com/volcano-sh/agentcube/client-go/informers" \
  --versioned-clientset-package "github.com/volcano-sh/agentcube/client-go/clientset/versioned" \
  --listers-package "github.com/volcano-sh/agentcube/client-go/listers" \
  "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"

echo "Client-go code generation completed!"
