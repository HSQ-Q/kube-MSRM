/*
Copyright 2019 The Kubernetes Authors.

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

package image

import (
	"fmt"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
)

type result struct {
	result string
	err    error
}

var registryTests = []struct {
	in  string
	out result
}{
	{
		"docker.io/library/test:123",
		result{
			result: "test.io/library/test:123",
			err:    nil,
		},
	},
	{
		"docker.io/library/test",
		result{
			result: "test.io/library/test",
			err:    nil,
		},
	},
	{
		"test",
		result{
			result: "test.io/library/test",
			err:    nil,
		},
	},
	{
		"gcr.io/kubernetes-e2e-test-images/test:123",
		result{
			result: "test.io/kubernetes-e2e-test-images/test:123",
			err:    nil,
		},
	},
	{
		"k8s.gcr.io/test:123",
		result{
			result: "test.io/test:123",
			err:    nil,
		},
	},
	{
		"gcr.io/k8s-authenticated-test/test:123",
		result{
			result: "test.io/k8s-authenticated-test/test:123",
			err:    nil,
		},
	},
	{
		"gcr.io/google-samples/test:latest",
		result{
			result: "test.io/google-samples/test:latest",
			err:    nil,
		},
	},
	{
		"gcr.io/gke-release/test:latest",
		result{
			result: "test.io/gke-release/test:latest",
			err:    nil,
		},
	},
	{
		"k8s.gcr.io/sig-storage/test:latest",
		result{
			result: "test.io/sig-storage/test:latest",
			err:    nil,
		},
	},
	{
		"unknwon.io/google-samples/test:latest",
		result{
			result: "",
			err:    fmt.Errorf("Registry: unknwon.io/google-samples is missing in test/utils/image/manifest.go, please add the registry, otherwise the test will fail on air-gapped clusters"),
		},
	},
}

// ToDo Add Benchmark
func TestReplaceRegistryInImageURL(t *testing.T) {
	// Set custom registries
	dockerLibraryRegistry = "test.io/library"
	e2eRegistry = "test.io/kubernetes-e2e-test-images"
	gcRegistry = "test.io"
	gcrReleaseRegistry = "test.io/gke-release"
	PrivateRegistry = "test.io/k8s-authenticated-test"
	sampleRegistry = "test.io/google-samples"
	sigStorageRegistry = "test.io/sig-storage"

	for _, tt := range registryTests {
		t.Run(tt.in, func(t *testing.T) {
			s, err := ReplaceRegistryInImageURL(tt.in)

			if err != nil && err.Error() != tt.out.err.Error() {
				t.Errorf("got %q, want %q", err, tt.out.err)
			}

			if s != tt.out.result {
				t.Errorf("got %q, want %q", s, tt.out.result)
			}
		})
	}
}

func TestGetOriginalImageConfigs(t *testing.T) {
	if len(GetOriginalImageConfigs()) == 0 {
		t.Fatalf("original map should not be empty")
	}
}

func TestGetMappedImageConfigs(t *testing.T) {
	originals := map[int]Config{
		0: {registry: "docker.io", name: "source/repo", version: "1.0"},
	}
	mapping := GetMappedImageConfigs(originals, "quay.io/repo/for-test")

	actual := make(map[string]string)
	for i, mapping := range mapping {
		source := originals[i]
		actual[source.GetE2EImage()] = mapping.GetE2EImage()
	}
	expected := map[string]string{
		"docker.io/source/repo:1.0": "quay.io/repo/for-test:e2e-0-docker-io-source-repo-1-0-72R4aXm7YnxQ4_ekf1DrFA",
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatal(diff.ObjectReflectDiff(expected, actual))
	}
}
