/*
Copyright 2020 The Kubernetes Authors.

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

package validation

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
)

var (
	ignoreBadValueDetail = cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail")
)

func TestValidateDefaultPreemptionArgs(t *testing.T) {
	cases := map[string]struct {
		args     config.DefaultPreemptionArgs
		wantErrs field.ErrorList
	}{
		"valid args (default)": {
			args: config.DefaultPreemptionArgs{
				MinCandidateNodesPercentage: 10,
				MinCandidateNodesAbsolute:   100,
			},
		},
		"negative minCandidateNodesPercentage": {
			args: config.DefaultPreemptionArgs{
				MinCandidateNodesPercentage: -1,
				MinCandidateNodesAbsolute:   100,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "minCandidateNodesPercentage",
				},
			},
		},
		"minCandidateNodesPercentage over 100": {
			args: config.DefaultPreemptionArgs{
				MinCandidateNodesPercentage: 900,
				MinCandidateNodesAbsolute:   100,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "minCandidateNodesPercentage",
				},
			},
		},
		"negative minCandidateNodesAbsolute": {
			args: config.DefaultPreemptionArgs{
				MinCandidateNodesPercentage: 20,
				MinCandidateNodesAbsolute:   -1,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "minCandidateNodesAbsolute",
				},
			},
		},
		"all zero": {
			args: config.DefaultPreemptionArgs{
				MinCandidateNodesPercentage: 0,
				MinCandidateNodesAbsolute:   0,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "minCandidateNodesPercentage",
				}, &field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "minCandidateNodesAbsolute",
				},
			},
		},
		"both negative": {
			args: config.DefaultPreemptionArgs{
				MinCandidateNodesPercentage: -1,
				MinCandidateNodesAbsolute:   -1,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "minCandidateNodesPercentage",
				}, &field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "minCandidateNodesAbsolute",
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateDefaultPreemptionArgs(tc.args)
			if diff := cmp.Diff(tc.wantErrs.ToAggregate(), err, ignoreBadValueDetail); diff != "" {
				t.Errorf("ValidateDefaultPreemptionArgs returned err (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestValidateInterPodAffinityArgs(t *testing.T) {
	cases := map[string]struct {
		args    config.InterPodAffinityArgs
		wantErr error
	}{
		"valid args": {
			args: config.InterPodAffinityArgs{
				HardPodAffinityWeight: 10,
			},
		},
		"hardPodAffinityWeight less than min": {
			args: config.InterPodAffinityArgs{
				HardPodAffinityWeight: -1,
			},
			wantErr: &field.Error{
				Type:  field.ErrorTypeInvalid,
				Field: "hardPodAffinityWeight",
			},
		},
		"hardPodAffinityWeight more than max": {
			args: config.InterPodAffinityArgs{
				HardPodAffinityWeight: 101,
			},
			wantErr: &field.Error{
				Type:  field.ErrorTypeInvalid,
				Field: "hardPodAffinityWeight",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateInterPodAffinityArgs(tc.args)
			if diff := cmp.Diff(tc.wantErr, err, ignoreBadValueDetail); diff != "" {
				t.Errorf("ValidateInterPodAffinityArgs returned err (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestValidateNodeLabelArgs(t *testing.T) {
	cases := map[string]struct {
		args     config.NodeLabelArgs
		wantErrs field.ErrorList
	}{
		"valid config": {
			args: config.NodeLabelArgs{
				PresentLabels:           []string{"present"},
				AbsentLabels:            []string{"absent"},
				PresentLabelsPreference: []string{"present-preference"},
				AbsentLabelsPreference:  []string{"absent-preference"},
			},
		},
		"labels conflict": {
			args: config.NodeLabelArgs{
				PresentLabels: []string{"label"},
				AbsentLabels:  []string{"label"},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "presentLabels[0]",
				},
			},
		},
		"multiple labels conflict": {
			args: config.NodeLabelArgs{
				PresentLabels: []string{"label", "label3"},
				AbsentLabels:  []string{"label", "label2", "label3"},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "presentLabels[0]",
				},
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "presentLabels[1]",
				},
			},
		},
		"labels preference conflict": {
			args: config.NodeLabelArgs{
				PresentLabelsPreference: []string{"label"},
				AbsentLabelsPreference:  []string{"label"},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "presentLabelsPreference[0]",
				},
			},
		},
		"multiple labels preference conflict": {
			args: config.NodeLabelArgs{
				PresentLabelsPreference: []string{"label", "label3"},
				AbsentLabelsPreference:  []string{"label", "label2", "label3"},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "presentLabelsPreference[0]",
				},
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "presentLabelsPreference[1]",
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateNodeLabelArgs(tc.args)
			if diff := cmp.Diff(tc.wantErrs.ToAggregate(), err, ignoreBadValueDetail); diff != "" {
				t.Errorf("ValidateNodeLabelArgs returned err (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestValidatePodTopologySpreadArgs(t *testing.T) {
	cases := map[string]struct {
		args     *config.PodTopologySpreadArgs
		wantErrs field.ErrorList
	}{
		"valid config": {
			args: &config.PodTopologySpreadArgs{
				DefaultConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       "node",
						WhenUnsatisfiable: v1.DoNotSchedule,
					},
					{
						MaxSkew:           2,
						TopologyKey:       "zone",
						WhenUnsatisfiable: v1.ScheduleAnyway,
					},
				},
				DefaultingType: config.ListDefaulting,
			},
		},
		"maxSkew less than zero": {
			args: &config.PodTopologySpreadArgs{
				DefaultConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           -1,
						TopologyKey:       "node",
						WhenUnsatisfiable: v1.DoNotSchedule,
					},
				},
				DefaultingType: config.ListDefaulting,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "defaultConstraints[0].maxSkew",
				},
			},
		},
		"empty topology key": {
			args: &config.PodTopologySpreadArgs{
				DefaultConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       "",
						WhenUnsatisfiable: v1.DoNotSchedule,
					},
				},
				DefaultingType: config.ListDefaulting,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeRequired,
					Field: "defaultConstraints[0].topologyKey",
				},
			},
		},
		"whenUnsatisfiable is empty": {
			args: &config.PodTopologySpreadArgs{
				DefaultConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       "node",
						WhenUnsatisfiable: "",
					},
				},
				DefaultingType: config.ListDefaulting,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeRequired,
					Field: "defaultConstraints[0].whenUnsatisfiable",
				},
			},
		},
		"whenUnsatisfiable contains unsupported action": {
			args: &config.PodTopologySpreadArgs{
				DefaultConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       "node",
						WhenUnsatisfiable: "unknown action",
					},
				},
				DefaultingType: config.ListDefaulting,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeNotSupported,
					Field: "defaultConstraints[0].whenUnsatisfiable",
				},
			},
		},
		"duplicated constraints": {
			args: &config.PodTopologySpreadArgs{
				DefaultConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       "node",
						WhenUnsatisfiable: v1.DoNotSchedule,
					},
					{
						MaxSkew:           2,
						TopologyKey:       "node",
						WhenUnsatisfiable: v1.DoNotSchedule,
					},
				},
				DefaultingType: config.ListDefaulting,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeDuplicate,
					Field: "defaultConstraints[1]",
				},
			},
		},
		"label selector present": {
			args: &config.PodTopologySpreadArgs{
				DefaultConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       "key",
						WhenUnsatisfiable: v1.DoNotSchedule,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"a": "b",
							},
						},
					},
				},
				DefaultingType: config.ListDefaulting,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeForbidden,
					Field: "defaultConstraints[0].labelSelector",
				},
			},
		},
		"list default constraints, no constraints": {
			args: &config.PodTopologySpreadArgs{
				DefaultingType: config.ListDefaulting,
			},
		},
		"system default constraints": {
			args: &config.PodTopologySpreadArgs{
				DefaultingType: config.SystemDefaulting,
			},
		},
		"wrong constraints": {
			args: &config.PodTopologySpreadArgs{
				DefaultingType: "unknown",
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeNotSupported,
					Field: "defaultingType",
				},
			},
		},
		"system default constraints, but has constraints": {
			args: &config.PodTopologySpreadArgs{
				DefaultConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       "key",
						WhenUnsatisfiable: v1.DoNotSchedule,
					},
				},
				DefaultingType: config.SystemDefaulting,
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "defaultingType",
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidatePodTopologySpreadArgs(tc.args)
			if diff := cmp.Diff(tc.wantErrs.ToAggregate(), err, ignoreBadValueDetail); diff != "" {
				t.Errorf("ValidatePodTopologySpreadArgs returned err (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestValidateRequestedToCapacityRatioArgs(t *testing.T) {
	cases := map[string]struct {
		args     config.RequestedToCapacityRatioArgs
		wantErrs field.ErrorList
	}{
		"valid config": {
			args: config.RequestedToCapacityRatioArgs{
				Shape: []config.UtilizationShapePoint{
					{
						Utilization: 20,
						Score:       5,
					},
					{
						Utilization: 30,
						Score:       3,
					},
					{
						Utilization: 50,
						Score:       2,
					},
				},
				Resources: []config.ResourceSpec{
					{
						Name:   "custom-resource",
						Weight: 5,
					},
				},
			},
		},
		"no shape points": {
			args: config.RequestedToCapacityRatioArgs{
				Shape: []config.UtilizationShapePoint{},
				Resources: []config.ResourceSpec{
					{
						Name:   "custom",
						Weight: 5,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeRequired,
					Field: "shape",
				},
			},
		},
		"utilization less than min": {
			args: config.RequestedToCapacityRatioArgs{
				Shape: []config.UtilizationShapePoint{
					{
						Utilization: -10,
						Score:       3,
					},
					{
						Utilization: 10,
						Score:       2,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "shape[0].utilization",
				},
			},
		},
		"utilization greater than max": {
			args: config.RequestedToCapacityRatioArgs{
				Shape: []config.UtilizationShapePoint{
					{
						Utilization: 10,
						Score:       3,
					},
					{
						Utilization: 110,
						Score:       2,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "shape[1].utilization",
				},
			},
		},
		"utilization values in non-increasing order": {
			args: config.RequestedToCapacityRatioArgs{
				Shape: []config.UtilizationShapePoint{
					{
						Utilization: 30,
						Score:       3,
					},
					{
						Utilization: 20,
						Score:       2,
					},
					{
						Utilization: 10,
						Score:       1,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "shape[1].utilization",
				},
			},
		},
		"duplicated utilization values": {
			args: config.RequestedToCapacityRatioArgs{
				Shape: []config.UtilizationShapePoint{
					{
						Utilization: 10,
						Score:       3,
					},
					{
						Utilization: 20,
						Score:       2,
					},
					{
						Utilization: 20,
						Score:       1,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "shape[2].utilization",
				},
			},
		},
		"score less than min": {
			args: config.RequestedToCapacityRatioArgs{
				Shape: []config.UtilizationShapePoint{
					{
						Utilization: 10,
						Score:       -1,
					},
					{
						Utilization: 20,
						Score:       2,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "shape[0].score",
				},
			},
		},
		"score greater than max": {
			args: config.RequestedToCapacityRatioArgs{
				Shape: []config.UtilizationShapePoint{
					{
						Utilization: 10,
						Score:       3,
					},
					{
						Utilization: 20,
						Score:       11,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "shape[1].score",
				},
			},
		},
		"resources weight less than 1": {
			args: config.RequestedToCapacityRatioArgs{
				Shape: []config.UtilizationShapePoint{
					{
						Utilization: 10,
						Score:       1,
					},
				},
				Resources: []config.ResourceSpec{
					{
						Name:   "custom",
						Weight: 0,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "resources[0].weight",
				},
			},
		},
		"multiple errors": {
			args: config.RequestedToCapacityRatioArgs{
				Shape: []config.UtilizationShapePoint{
					{
						Utilization: 20,
						Score:       -1,
					},
					{
						Utilization: 10,
						Score:       2,
					},
				},
				Resources: []config.ResourceSpec{
					{
						Name:   "custom",
						Weight: 0,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "shape[1].utilization",
				},
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "shape[0].score",
				},
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "resources[0].weight",
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateRequestedToCapacityRatioArgs(tc.args)
			if diff := cmp.Diff(tc.wantErrs.ToAggregate(), err, ignoreBadValueDetail); diff != "" {
				t.Errorf("ValidateRequestedToCapacityRatioArgs returned err (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestValidateNodeResourcesLeastAllocatedArgs(t *testing.T) {
	cases := map[string]struct {
		args     *config.NodeResourcesLeastAllocatedArgs
		wantErrs field.ErrorList
	}{
		"valid config": {
			args: &config.NodeResourcesLeastAllocatedArgs{
				Resources: []config.ResourceSpec{
					{
						Name:   "cpu",
						Weight: 50,
					},
					{
						Name:   "memory",
						Weight: 30,
					},
				},
			},
		},
		"weight less than min": {
			args: &config.NodeResourcesLeastAllocatedArgs{
				Resources: []config.ResourceSpec{
					{
						Name:   "cpu",
						Weight: 0,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "resources[0].weight",
				},
			},
		},
		"weight more than max": {
			args: &config.NodeResourcesLeastAllocatedArgs{
				Resources: []config.ResourceSpec{
					{
						Name:   "memory",
						Weight: 101,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "resources[0].weight",
				},
			},
		},
		"multiple error": {
			args: &config.NodeResourcesLeastAllocatedArgs{
				Resources: []config.ResourceSpec{
					{
						Name:   "memory",
						Weight: 0,
					},
					{
						Name:   "cpu",
						Weight: 101,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "resources[0].weight",
				},
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "resources[1].weight",
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateNodeResourcesLeastAllocatedArgs(tc.args)
			if diff := cmp.Diff(tc.wantErrs.ToAggregate(), err, ignoreBadValueDetail); diff != "" {
				t.Errorf("ValidateNodeResourcesLeastAllocatedArgs returned err (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestValidateNodeResourcesMostAllocatedArgs(t *testing.T) {
	cases := map[string]struct {
		args     *config.NodeResourcesMostAllocatedArgs
		wantErrs field.ErrorList
	}{
		"valid config": {
			args: &config.NodeResourcesMostAllocatedArgs{
				Resources: []config.ResourceSpec{
					{
						Name:   "cpu",
						Weight: 70,
					},
					{
						Name:   "memory",
						Weight: 40,
					},
				},
			},
		},
		"weight less than min": {
			args: &config.NodeResourcesMostAllocatedArgs{
				Resources: []config.ResourceSpec{
					{
						Name:   "cpu",
						Weight: -1,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "resources[0].weight",
				},
			},
		},
		"weight more than max": {
			args: &config.NodeResourcesMostAllocatedArgs{
				Resources: []config.ResourceSpec{
					{
						Name:   "memory",
						Weight: 110,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "resources[0].weight",
				},
			},
		},
		"multiple error": {
			args: &config.NodeResourcesMostAllocatedArgs{
				Resources: []config.ResourceSpec{
					{
						Name:   "memory",
						Weight: -1,
					},
					{
						Name:   "cpu",
						Weight: 110,
					},
				},
			},
			wantErrs: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "resources[0].weight",
				},
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "resources[1].weight",
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateNodeResourcesMostAllocatedArgs(tc.args)
			if diff := cmp.Diff(tc.wantErrs.ToAggregate(), err, ignoreBadValueDetail); diff != "" {
				t.Errorf("ValidateNodeResourcesLeastAllocatedArgs returned err (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestValidateNodeAffinityArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    config.NodeAffinityArgs
		wantErr error
	}{
		{
			name: "empty",
		},
		{
			name: "valid added affinity",
			args: config.NodeAffinityArgs{
				AddedAffinity: &v1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{
							{
								MatchExpressions: []v1.NodeSelectorRequirement{
									{
										Key:      "label-1",
										Operator: v1.NodeSelectorOpIn,
										Values:   []string{"label-1-val"},
									},
								},
							},
						},
					},
					PreferredDuringSchedulingIgnoredDuringExecution: []v1.PreferredSchedulingTerm{
						{
							Weight: 1,
							Preference: v1.NodeSelectorTerm{
								MatchFields: []v1.NodeSelectorRequirement{
									{
										Key:      "metadata.name",
										Operator: v1.NodeSelectorOpIn,
										Values:   []string{"node-1"},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "invalid added affinity",
			args: config.NodeAffinityArgs{
				AddedAffinity: &v1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{
							{
								MatchExpressions: []v1.NodeSelectorRequirement{
									{
										Key:      "invalid/label/key",
										Operator: v1.NodeSelectorOpIn,
										Values:   []string{"label-1-val"},
									},
								},
							},
						},
					},
					PreferredDuringSchedulingIgnoredDuringExecution: []v1.PreferredSchedulingTerm{
						{
							Weight: 1,
							Preference: v1.NodeSelectorTerm{
								MatchFields: []v1.NodeSelectorRequirement{
									{
										Key:      "metadata.name",
										Operator: v1.NodeSelectorOpIn,
										Values:   []string{"node-1", "node-2"},
									},
								},
							},
						},
					},
				},
			},
			wantErr: field.ErrorList{
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "addedAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].key",
				},
				&field.Error{
					Type:  field.ErrorTypeInvalid,
					Field: "addedAffinity.preferredDuringSchedulingIgnoredDuringExecution[0].matchFields[0].values",
				},
			}.ToAggregate(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNodeAffinityArgs(&tc.args)
			if diff := cmp.Diff(tc.wantErr, err, ignoreBadValueDetail); diff != "" {
				t.Errorf("ValidatedNodeAffinityArgs returned err (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestValidateVolumeBindingArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    config.VolumeBindingArgs
		wantErr error
	}{
		{
			name: "zero is a valid config",
			args: config.VolumeBindingArgs{
				BindTimeoutSeconds: 0,
			},
		},
		{
			name: "positive value is valid config",
			args: config.VolumeBindingArgs{
				BindTimeoutSeconds: 10,
			},
		},
		{
			name: "negative value is invalid config ",
			args: config.VolumeBindingArgs{
				BindTimeoutSeconds: -10,
			},
			wantErr: &field.Error{
				Type:  field.ErrorTypeInvalid,
				Field: "bindTimeoutSeconds",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateVolumeBindingArgs(&tc.args)
			if diff := cmp.Diff(tc.wantErr, err, ignoreBadValueDetail); diff != "" {
				t.Errorf("ValidateVolumeBindingArgs returned err (-want,+got):\n%s", diff)
			}
		})
	}
}
