/*
Copyright 2017 The Kubernetes Authors.

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

package persistentvolumeclaim

import (
	"fmt"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/features"
)

func TestDropDisabledSnapshotDataSource(t *testing.T) {
	pvcWithoutDataSource := func() *core.PersistentVolumeClaim {
		return &core.PersistentVolumeClaim{
			Spec: core.PersistentVolumeClaimSpec{
				DataSource: nil,
			},
		}
	}
	apiGroup := "snapshot.storage.k8s.io"
	pvcWithDataSource := func() *core.PersistentVolumeClaim {
		return &core.PersistentVolumeClaim{
			Spec: core.PersistentVolumeClaimSpec{
				DataSource: &core.TypedLocalObjectReference{
					APIGroup: &apiGroup,
					Kind:     "VolumeSnapshot",
					Name:     "test_snapshot",
				},
			},
		}
	}

	pvcInfo := []struct {
		description string
		pvc         func() *core.PersistentVolumeClaim
	}{
		{
			description: "pvc without DataSource",
			pvc:         pvcWithoutDataSource,
		},
		{
			description: "pvc with DataSource",
			pvc:         pvcWithDataSource,
		},
		{
			description: "is nil",
			pvc:         func() *core.PersistentVolumeClaim { return nil },
		},
	}

	// Ensure that any data sources aren't enabled for this test
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.AnyVolumeDataSource, false)()

	for _, oldpvcInfo := range pvcInfo {
		for _, newpvcInfo := range pvcInfo {
			oldpvc := oldpvcInfo.pvc()
			newpvc := newpvcInfo.pvc()
			if newpvc == nil {
				continue
			}

			t.Run(fmt.Sprintf("old pvc %v, new pvc %v", oldpvcInfo.description, newpvcInfo.description), func(t *testing.T) {
				var oldpvcSpec *core.PersistentVolumeClaimSpec
				if oldpvc != nil {
					oldpvcSpec = &oldpvc.Spec
				}
				DropDisabledFields(&newpvc.Spec, oldpvcSpec)

				// old pvc should never be changed
				if !reflect.DeepEqual(oldpvc, oldpvcInfo.pvc()) {
					t.Errorf("old pvc changed: %v", diff.ObjectReflectDiff(oldpvc, oldpvcInfo.pvc()))
				}

				// new pvc should not be changed
				if !reflect.DeepEqual(newpvc, newpvcInfo.pvc()) {
					t.Errorf("new pvc changed: %v", diff.ObjectReflectDiff(newpvc, newpvcInfo.pvc()))
				}
			})
		}
	}
}

// TestPVCDataSourceSpecFilter checks to ensure the DropDisabledFields function behaves correctly for PVCDataSource featuregate
func TestPVCDataSourceSpecFilter(t *testing.T) {
	apiGroup := ""
	validSpec := core.PersistentVolumeClaimSpec{
		DataSource: &core.TypedLocalObjectReference{
			APIGroup: &apiGroup,
			Kind:     "PersistentVolumeClaim",
			Name:     "test_clone",
		},
	}
	validSpecNilAPIGroup := core.PersistentVolumeClaimSpec{
		DataSource: &core.TypedLocalObjectReference{
			Kind: "PersistentVolumeClaim",
			Name: "test_clone",
		},
	}

	invalidAPIGroup := "invalid.pvc.api.group"
	invalidSpec := core.PersistentVolumeClaimSpec{
		DataSource: &core.TypedLocalObjectReference{
			APIGroup: &invalidAPIGroup,
			Kind:     "PersistentVolumeClaim",
			Name:     "test_clone_invalid",
		},
	}

	var tests = map[string]struct {
		spec core.PersistentVolumeClaimSpec
		want *core.TypedLocalObjectReference
	}{
		"enabled with empty ds": {
			spec: core.PersistentVolumeClaimSpec{},
			want: nil,
		},
		"enabled with invalid spec": {
			spec: invalidSpec,
			want: nil,
		},
		"enabled with valid spec": {
			spec: validSpec,
			want: validSpec.DataSource,
		},
		"enabled with valid spec but nil APIGroup": {
			spec: validSpecNilAPIGroup,
			want: validSpecNilAPIGroup.DataSource,
		},
	}

	// Ensure that any data sources aren't enabled for this test
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.AnyVolumeDataSource, false)()

	for testName, test := range tests {
		t.Run(testName, func(t *testing.T) {
			DropDisabledFields(&test.spec, nil)
			if test.spec.DataSource != test.want {
				t.Errorf("expected drop datasource condition was not met, test: %s, spec: %v, expected: %v", testName, test.spec, test.want)
			}

		})

	}

}

// TestAnyDataSourceFilter checks to ensure the AnyVolumeDataSource feature gate works
func TestAnyDataSourceFilter(t *testing.T) {
	makeDataSource := func(apiGroup, kind, name string) *core.TypedLocalObjectReference {
		return &core.TypedLocalObjectReference{
			APIGroup: &apiGroup,
			Kind:     kind,
			Name:     name,
		}
	}

	volumeDataSource := makeDataSource("", "PersistentVolumeClaim", "my-vol")
	snapshotDataSource := makeDataSource("snapshot.storage.k8s.io", "VolumeSnapshot", "my-snap")
	genericDataSource := makeDataSource("generic.storage.k8s.io", "Generic", "my-foo")

	var tests = map[string]struct {
		spec       core.PersistentVolumeClaimSpec
		anyEnabled bool
		want       *core.TypedLocalObjectReference
	}{
		"any disabled with empty ds": {
			spec: core.PersistentVolumeClaimSpec{},
			want: nil,
		},
		"any disabled with volume ds": {
			spec: core.PersistentVolumeClaimSpec{DataSource: volumeDataSource},
			want: volumeDataSource,
		},
		"any disabled with snapshot ds": {
			spec: core.PersistentVolumeClaimSpec{DataSource: snapshotDataSource},
			want: snapshotDataSource,
		},
		"any disabled with generic ds": {
			spec: core.PersistentVolumeClaimSpec{DataSource: genericDataSource},
			want: nil,
		},
		"any enabled with empty ds": {
			spec:       core.PersistentVolumeClaimSpec{},
			anyEnabled: true,
			want:       nil,
		},
		"any enabled with volume ds": {
			spec:       core.PersistentVolumeClaimSpec{DataSource: volumeDataSource},
			anyEnabled: true,
			want:       volumeDataSource,
		},
		"any enabled with snapshot ds": {
			spec:       core.PersistentVolumeClaimSpec{DataSource: snapshotDataSource},
			anyEnabled: true,
			want:       snapshotDataSource,
		},
		"any enabled with generic ds": {
			spec:       core.PersistentVolumeClaimSpec{DataSource: genericDataSource},
			anyEnabled: true,
			want:       genericDataSource,
		},
	}

	for testName, test := range tests {
		t.Run(testName, func(t *testing.T) {
			defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.AnyVolumeDataSource, test.anyEnabled)()
			DropDisabledFields(&test.spec, nil)
			if test.spec.DataSource != test.want {
				t.Errorf("expected condition was not met, test: %s, anyEnabled: %v, spec: %v, expected: %v",
					testName, test.anyEnabled, test.spec, test.want)
			}
		})
	}
}
