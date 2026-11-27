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
	"errors"
	"time"
)

// updateSessionIdleExpiryLua only advances the deadline of an existing session.
// In cluster mode its keys must share a hash slot, so it assumes the currently
// supported non-cluster Redis and Valkey deployments.
const updateSessionIdleExpiryLua = `
if redis.call("EXISTS", KEYS[1]) == 0 then
	return 0
end
local current = redis.call("ZSCORE", KEYS[2], ARGV[1])
if not current or tonumber(ARGV[2]) > tonumber(current) then
	redis.call("ZADD", KEYS[2], ARGV[2], ARGV[1])
end
return 1
`

func validateSessionActivity(sessionID string, at time.Time) error {
	if sessionID == "" {
		return errors.New("sessionID is empty")
	}
	if at.IsZero() {
		return errors.New("last activity is zero")
	}
	return nil
}
