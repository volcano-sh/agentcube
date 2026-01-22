#!/usr/bin/env bash

# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -e
set -o pipefail

# TARGET_COVERAGE is the minimum required coverage percentage
TARGET_COVERAGE=70
COVERAGE_FILE="coverage.out"

echo "Running tests with coverage..."
go test -v -coverprofile=${COVERAGE_FILE} ./pkg/...

echo "Coverage Summary:"
go tool cover -func=${COVERAGE_FILE} | tail -n 1

# Extract total coverage percentage
TOTAL_COVERAGE=$(go tool cover -func=${COVERAGE_FILE} | grep total | awk '{print $3}' | sed 's/%//')

echo "Total Coverage: ${TOTAL_COVERAGE}%"
echo "Target Coverage: ${TARGET_COVERAGE}%"

# Compare using bc for floating point comparison if available, otherwise integer
if command -v bc > /dev/null; then
    COMPARE=$(echo "${TOTAL_COVERAGE} >= ${TARGET_COVERAGE}" | bc)
else
    # Fallback to integer comparison
    COMPARE=$(echo "${TOTAL_COVERAGE%.*} ${TARGET_COVERAGE}" | awk '{if ($1 >= $2) print 1; else print 0}')
fi

if [ "$COMPARE" -eq 1 ]; then
    echo "SUCCESS: Coverage is above target."
    exit 0
else
    echo "FAILURE: Coverage is below target!"
    exit 1
fi
