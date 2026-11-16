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

package store

import (
	"fmt"
	"strconv"
	"time"
)

// inactiveIDsFromScan decodes the member/score pairs returned by ZSCAN.
// Filtering here avoids loading JSON for active sessions, while continuation
// always comes from the raw scan, even when no members pass the filter.
func inactiveIDsFromScan(elements []string, before time.Time) ([]string, error) {
	if len(elements)%2 != 0 {
		return nil, fmt.Errorf("ZSCAN returned an incomplete member/score pair")
	}
	ids := make([]string, 0, len(elements)/2)
	for i := 0; i < len(elements); i += 2 {
		score, err := strconv.ParseFloat(elements[i+1], 64)
		if err != nil {
			return nil, fmt.Errorf("ZSCAN score for session %s: %w", elements[i], err)
		}
		if score <= float64(before.Unix()) {
			ids = append(ids, elements[i])
		}
	}
	return ids, nil
}
