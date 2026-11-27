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

// sandboxStartupGracePeriod is the temporary idle timeout used while a sandbox
// is starting up (from StoreSandbox until it becomes ready).
// It covers the worst-case startup time (2m creation timeout + 15s entrypoint
// probe + buffer) so GC does not reclaim a starting sandbox early, while still
// cleaning up orphaned records if workload-manager crashes mid-creation.
const sandboxStartupGracePeriod = 3 * time.Minute

// updateSessionIdleExpiryLua updates the idle deadline of an existing session.
// When ARGV[3] == "1", the deadline is overwritten unconditionally; otherwise it
// only advances forward.
// In cluster mode its keys must share a hash slot, so it assumes the currently
// supported non-cluster Redis and Valkey deployments.
const updateSessionIdleExpiryLua = `
if redis.call("EXISTS", KEYS[1]) == 0 then
	return 0
end
if ARGV[3] == "1" then
	redis.call("ZADD", KEYS[2], ARGV[2], ARGV[1])
	return 1
end
local current = redis.call("ZSCORE", KEYS[2], ARGV[1])
if not current or tonumber(ARGV[2]) > tonumber(current) then
	redis.call("ZADD", KEYS[2], ARGV[2], ARGV[1])
end
return 1
`

func forceFlag(force bool) string {
	if force {
		return "1"
	}
	return "0"
}

func validateSessionActivity(sessionID string, at time.Time) error {
	if sessionID == "" {
		return errors.New("sessionID is empty")
	}
	if at.IsZero() {
		return errors.New("last activity is zero")
	}
	return nil
}
