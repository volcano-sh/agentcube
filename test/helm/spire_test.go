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

package helm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// TestSPIREStartup renders real Helm output and checks the certificate startup gate.
func TestSPIREStartup(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required for chart rendering tests")
	}
	for _, tc := range []struct {
		name    string
		enabled bool
		custom  bool
	}{
		{name: "disabled"}, {name: "enabled", enabled: true}, {name: "custom paths", enabled: true, custom: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"template", "agentcube", "../../manifests/charts/base", "--namespace", "agentcube", "--set", fmt.Sprintf("spire.enabled=%t", tc.enabled)}
			dir, cert, key, bundle := "/run/spire/certs", "svid.pem", "svid_key.pem", "svid_bundle.pem"
			if tc.custom {
				dir, cert, key, bundle = "/custom/certs", "cert.pem", "key.pem", "ca.pem"
				args = append(args, "--set", "spire.spiffeHelper.healthPort=18081", "--set", "spire.spiffeHelper.certDir="+dir, "--set", "spire.spiffeHelper.certFileName="+cert, "--set", "spire.spiffeHelper.keyFileName="+key, "--set", "spire.spiffeHelper.bundleFileName="+bundle)
			}
			output, err := exec.Command("helm", args...).CombinedOutput()
			require.NoError(t, err, "%s", output)
			deployments, config := decodeChart(t, output)
			for _, name := range []string{"agentcube-router", "workloadmanager"} {
				d, ok := deployments[name]
				require.True(t, ok, "missing deployment %s", name)
				spec := d.Spec.Template.Spec
				require.Len(t, spec.Containers, 1, "%s must only have the application as a regular container", name)
				app := spec.Containers[0]
				require.Equal(t, name, app.Name)
				if !tc.enabled {
					require.Empty(t, spec.InitContainers)
					require.Empty(t, spec.Volumes)
					require.Empty(t, app.VolumeMounts)
					for _, arg := range app.Args {
						require.NotContains(t, arg, "--tls-")
						require.NotContains(t, arg, "--mtls-")
					}
					continue
				}
				healthPort := int32(8081)
				if tc.custom {
					healthPort = 18081
				}
				assertSPIREStartup(t, spec, name, dir, cert, key, bundle, healthPort)
			}
			if !tc.enabled {
				require.Empty(t, config.Name)
				return
			}
			conf := config.Data["spiffe-helper.conf"]
			healthPort := 8081
			if tc.custom {
				healthPort = 18081
			}
			require.Contains(t, conf, fmt.Sprintf("bind_port = %d", healthPort))
			for _, value := range []string{"health_checks {", "listener_enabled = true", `readiness_path = "/ready"`, `liveness_path = "/live"`, `key_file_mode = "0640"`, `cert_dir = "` + dir + `"`, `svid_file_name = "` + cert + `"`, `svid_key_file_name = "` + key + `"`, `svid_bundle_file_name = "` + bundle + `"`} {
				require.Contains(t, conf, value)
			}
		})
	}
}

func decodeChart(t *testing.T, output []byte) (map[string]appsv1.Deployment, corev1.ConfigMap) {
	t.Helper()
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(output), 4096)
	deployments := map[string]appsv1.Deployment{}
	var config corev1.ConfigMap
	for {
		var doc struct {
			Kind     string
			Metadata struct{ Name string }
		}
		var raw map[string]interface{}
		err := decoder.Decode(&raw)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := json.Marshal(raw)
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(data, &doc))
		if doc.Kind == "Deployment" {
			var d appsv1.Deployment
			require.NoError(t, yaml.Unmarshal(data, &d))
			deployments[d.Name] = d
		}
		if doc.Kind == "ConfigMap" && doc.Metadata.Name == "spiffe-helper-config" {
			require.NoError(t, yaml.Unmarshal(data, &config))
		}
	}

	return deployments, config
}

func assertSPIREStartup(t *testing.T, spec corev1.PodSpec, name, dir, cert, key, bundle string, healthPort int32) {
	t.Helper()
	app := spec.Containers[0]
	require.Len(t, spec.InitContainers, 1)
	helper := spec.InitContainers[0]
	require.Equal(t, "spiffe-helper", helper.Name)
	require.NotNil(t, helper.RestartPolicy)
	require.Equal(t, corev1.ContainerRestartPolicyAlways, *helper.RestartPolicy)
	require.Equal(t, []string{"-config", "/etc/spiffe-helper/spiffe-helper.conf"}, helper.Args)
	require.NotNil(t, helper.StartupProbe)
	require.NotNil(t, helper.StartupProbe.HTTPGet)
	require.Equal(t, "/ready", helper.StartupProbe.HTTPGet.Path)
	require.EqualValues(t, healthPort, helper.StartupProbe.HTTPGet.Port.IntVal)
	require.GreaterOrEqual(t, helper.StartupProbe.PeriodSeconds*helper.StartupProbe.FailureThreshold, int32(300))
	require.Contains(t, helper.VolumeMounts, corev1.VolumeMount{Name: "spire-certs", MountPath: dir})
	require.Contains(t, app.VolumeMounts, corev1.VolumeMount{Name: "spire-certs", MountPath: dir, ReadOnly: true})
	require.NotNil(t, spec.SecurityContext)
	require.EqualValues(t, 1000, *spec.SecurityContext.FSGroup)
	var shared bool
	for _, v := range spec.Volumes {
		if v.Name == "spire-certs" {
			require.NotNil(t, v.EmptyDir)
			shared = true
		}
	}
	require.True(t, shared)
	prefix := "--tls-"
	if name == "agentcube-router" {
		prefix = "--mtls-"
	}
	for flag, file := range map[string]string{"cert": cert, "key": key, "ca": bundle} {
		require.Contains(t, app.Args, prefix+flag+"="+dir+"/"+file)
	}
}
