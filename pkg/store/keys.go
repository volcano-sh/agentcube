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

// Package store defines shared key-namespace constants used by all store
// backends (Redis and Valkey). Centralizing them here ensures that the
// key prefixes cannot drift between backends when one implementation is
// modified independently of the other.
package store

// sessionPrivKeyPrefix is the key prefix for per-session ECDSA private keys.
// Both the Redis and Valkey backends use this prefix so that a key written by
// one backend can be read (or deleted) consistently across the codebase.
const sessionPrivKeyPrefix = "session_key:"
