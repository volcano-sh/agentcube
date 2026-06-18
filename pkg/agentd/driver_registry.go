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

package agentd

import "fmt"

// DriverFactory is a constructor function for a SnapshotDriver. Each driver
// file calls RegisterDriverFactory in its init() to enroll itself so that
// BuildDefaultRegistry can instantiate all drivers without main knowing their names.
type DriverFactory func() SnapshotDriver

var defaultFactories []DriverFactory

// RegisterDriverFactory enrolls a factory in the package-level default list.
// Call this from driver init() functions; never call it after BuildDefaultRegistry.
func RegisterDriverFactory(f DriverFactory) {
	defaultFactories = append(defaultFactories, f)
}

// BuildDefaultRegistry instantiates every factory registered via RegisterDriverFactory
// and returns a ready-to-use DriverRegistry. Returns an error if two factories
// produce drivers with the same name.
func BuildDefaultRegistry() (*DriverRegistry, error) {
	r := NewDriverRegistry()
	for _, f := range defaultFactories {
		if err := r.Register(f()); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// DriverRegistry holds the set of SnapshotDrivers available on this node.
// Use BuildDefaultRegistry to create one from self-registered drivers, or
// NewDriverRegistry + Register for explicit construction (e.g. in tests).
type DriverRegistry struct {
	drivers map[string]SnapshotDriver
}

// NewDriverRegistry returns an empty registry.
func NewDriverRegistry() *DriverRegistry {
	return &DriverRegistry{drivers: make(map[string]SnapshotDriver)}
}

// Register adds a driver to the registry. Returns an error if a driver with
// the same name has already been registered.
func (r *DriverRegistry) Register(driver SnapshotDriver) error {
	name := driver.Name()
	if _, exists := r.drivers[name]; exists {
		return fmt.Errorf("snapshot driver %q already registered", name)
	}
	r.drivers[name] = driver
	return nil
}

// Get returns the driver registered under the given name, or false if absent.
func (r *DriverRegistry) Get(name string) (SnapshotDriver, bool) {
	d, ok := r.drivers[name]
	return d, ok
}

// Drivers returns a shallow copy of the internal driver map. Callers may
// iterate or pass the result to other components without affecting the registry.
func (r *DriverRegistry) Drivers() map[string]SnapshotDriver {
	out := make(map[string]SnapshotDriver, len(r.drivers))
	for k, v := range r.drivers {
		out[k] = v
	}
	return out
}
