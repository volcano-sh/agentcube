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
	"cmp"
	"slices"
	"time"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

const expiredClaimLease = 60 * time.Second

const toUTC14Lua = `
local function to_utc14(ts)
	if type(ts) ~= "string" then
		return nil
	end
	local y, m, d, hh, mm, ss = string.match(ts, "^(%d%d%d%d)%-(%d%d)%-(%d%d)T(%d%d):(%d%d):(%d%d)")
	if not y then
		return nil
	end
	return y .. m .. d .. hh .. mm .. ss
end
`

const updateSandboxExpiryLua = toUTC14Lua + `
local raw = redis.call("GET", KEYS[1])
if not raw then
	return {0, "", 0}
end
local obj = cjson.decode(raw)
if obj["status"] == "deleting" then
	return {0, "", 0}
end
local cur14 = to_utc14(obj["expiresAt"])
if ARGV[3] == "1" and cur14 and cur14 >= ARGV[2] then
	return {1, raw, 0}
end
obj["expiresAt"] = ARGV[1]
local updated = cjson.encode(obj)
redis.call("SET", KEYS[1], updated)
return {1, updated, 1}
`

const claimExpiredSandboxLua = toUTC14Lua + `
local raw = redis.call("GET", KEYS[1])
if not raw then
	return -1
end
local obj = cjson.decode(raw)
if obj["status"] == "deleting" then
	local claimUntil = tonumber(obj["claimUntil"]) or 0
	if claimUntil > tonumber(ARGV[2]) then
		return 0
	end
	obj["claimUntil"] = tonumber(ARGV[3])
	redis.call("SET", KEYS[1], cjson.encode(obj))
	return 1
end
local cur14 = to_utc14(obj["expiresAt"])
if cur14 and cur14 > ARGV[1] then
	return 0
end
obj["status"] = "deleting"
obj["claimUntil"] = tonumber(ARGV[3])
redis.call("SET", KEYS[1], cjson.encode(obj))
return 1
`

func ownerSessionsKey(ownerID string) string {
	return "owner:" + ownerID + ":sessions"
}

func filterAndSortActiveSandboxes(sandboxes []*types.SandboxInfo, namespace string, now time.Time) []*types.SandboxInfo {
	active := make([]*types.SandboxInfo, 0, len(sandboxes))
	for _, s := range sandboxes {
		if s == nil {
			continue
		}
		if s.Status == "deleting" || !s.ExpiresAt.After(now) {
			continue
		}
		if namespace != "" && s.SandboxNamespace != namespace {
			continue
		}
		active = append(active, s)
	}
	slices.SortFunc(active, func(a, b *types.SandboxInfo) int {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			if a.CreatedAt.After(b.CreatedAt) {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.SessionID, b.SessionID)
	})
	return active
}
