/*
Copyright 2015 The Kubernetes Authors.

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

package podautoscaler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2beta1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta/testrestmapper"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	restclient "k8s.io/client-go/rest"
	scalefake "k8s.io/client-go/scale/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	"k8s.io/kubernetes/pkg/controller"
	"k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"

	heapster "k8s.io/heapster/metrics/api/v1/types"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1alpha1"

	"github.com/stretchr/testify/assert"

	_ "k8s.io/kubernetes/pkg/apis/apps/install"
	_ "k8s.io/kubernetes/pkg/apis/autoscaling/install"
	_ "k8s.io/kubernetes/pkg/apis/core/install"
)

func (w fakeResponseWrapper) DoRaw(context.Context) ([]byte, error) {
	return w.raw, nil
}

func (w fakeResponseWrapper) Stream(context.Context) (io.ReadCloser, error) {
	return nil, nil
}

func newFakeResponseWrapper(raw []byte) fakeResponseWrapper {
	return fakeResponseWrapper{raw: raw}
}

type fakeResponseWrapper struct {
	raw []byte
}

type legacyTestCase struct {
	sync.Mutex
	minReplicas     int32
	maxReplicas     int32
	initialReplicas int32
	desiredReplicas int32

	// CPU target utilization as a percentage of the requested resources.
	CPUTarget            int32
	CPUCurrent           int32
	verifyCPUCurrent     bool
	reportedLevels       []uint64
	reportedCPURequests  []resource.Quantity
	reportedPodReadiness []v1.ConditionStatus
	scaleUpdated         bool
	statusUpdated        bool
	eventCreated         bool
	verifyEvents         bool
	useMetricsAPI        bool
	metricsTarget        []autoscalingv2.MetricSpec
	// Channel with names of HPA objects which we have reconciled.
	processed chan string

	// Target resource information.
	resource *fakeResource

	// Last scale time
	lastScaleTime   *metav1.Time
	recommendations []timestampedRecommendation

	finished bool
}

// Needs to be called under a lock.
func (tc *legacyTestCase) computeCPUCurrent() {
	if len(tc.reportedLevels) != len(tc.reportedCPURequests) || len(tc.reportedLevels) == 0 {
		return
	}
	reported := 0
	for _, r := range tc.reportedLevels {
		reported += int(r)
	}
	requested := 0
	for _, req := range tc.reportedCPURequests {
		requested += int(req.MilliValue())
	}
	tc.CPUCurrent = int32(100 * reported / requested)
}

func (tc *legacyTestCase) prepareTestClient(t *testing.T) (*fake.Clientset, *scalefake.FakeScaleClient) {
	namespace := "test-namespace"
	hpaName := "test-hpa"
	podNamePrefix := "test-pod"
	labelSet := map[string]string{"name": podNamePrefix}
	selector := labels.SelectorFromSet(labelSet).String()

	tc.Lock()

	tc.scaleUpdated = false
	tc.statusUpdated = false
	tc.eventCreated = false
	tc.processed = make(chan string, 100)
	if tc.CPUCurrent == 0 {
		tc.computeCPUCurrent()
	}

	if tc.resource == nil {
		tc.resource = &fakeResource{
			name:       "test-rc",
			apiVersion: "v1",
			kind:       "ReplicationController",
		}
	}
	tc.Unlock()

	fakeClient := &fake.Clientset{}
	fakeClient.AddReactor("list", "horizontalpodautoscalers", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		tc.Lock()
		defer tc.Unlock()

		obj := &autoscalingv2.HorizontalPodAutoscalerList{
			Items: []autoscalingv2.HorizontalPodAutoscaler{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hpaName,
						Namespace: namespace,
						SelfLink:  "experimental/v1/namespaces/" + namespace + "/horizontalpodautoscalers/" + hpaName,
					},
					Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
						ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
							Kind:       tc.resource.kind,
							Name:       tc.resource.name,
							APIVersion: tc.resource.apiVersion,
						},
						MinReplicas: &tc.minReplicas,
						MaxReplicas: tc.maxReplicas,
					},
					Status: autoscalingv2.HorizontalPodAutoscalerStatus{
						CurrentReplicas: tc.initialReplicas,
						DesiredReplicas: tc.initialReplicas,
					},
				},
			},
		}

		if tc.CPUTarget > 0.0 {
			obj.Items[0].Spec.Metrics = []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name:                     v1.ResourceCPU,
						TargetAverageUtilization: &tc.CPUTarget,
					},
				},
			}
		}
		if len(tc.metricsTarget) > 0 {
			obj.Items[0].Spec.Metrics = append(obj.Items[0].Spec.Metrics, tc.metricsTarget...)
		}

		if len(obj.Items[0].Spec.Metrics) == 0 {
			// manually add in the defaulting logic
			obj.Items[0].Spec.Metrics = []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: v1.ResourceCPU,
					},
				},
			}
		}

		// and... convert to autoscaling v1 to return the right type
		objv1, err := unsafeConvertToVersionVia(obj, autoscalingv1.SchemeGroupVersion)
		if err != nil {
			return true, nil, err
		}

		return true, objv1, nil
	})

	fakeClient.AddReactor("list", "pods", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		tc.Lock()
		defer tc.Unlock()

		obj := &v1.PodList{}
		for i := 0; i < len(tc.reportedCPURequests); i++ {
			podReadiness := v1.ConditionTrue
			if tc.reportedPodReadiness != nil {
				podReadiness = tc.reportedPodReadiness[i]
			}
			podName := fmt.Sprintf("%s-%d", podNamePrefix, i)
			pod := v1.Pod{
				Status: v1.PodStatus{
					StartTime: &metav1.Time{Time: time.Now().Add(-3 * time.Minute)},
					Phase:     v1.PodRunning,
					Conditions: []v1.PodCondition{
						{
							Type:   v1.PodReady,
							Status: podReadiness,
						},
					},
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: namespace,
					Labels: map[string]string{
						"name": podNamePrefix,
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceCPU: tc.reportedCPURequests[i],
								},
							},
						},
					},
				},
			}
			obj.Items = append(obj.Items, pod)
		}
		return true, obj, nil
	})

	fakeClient.AddProxyReactor("services", func(action core.Action) (handled bool, ret restclient.ResponseWrapper, err error) {
		tc.Lock()
		defer tc.Unlock()

		var heapsterRawMemResponse []byte

		if tc.useMetricsAPI {
			metrics := metricsapi.PodMetricsList{}
			for i, cpu := range tc.reportedLevels {
				podMetric := metricsapi.PodMetrics{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-%d", podNamePrefix, i),
						Namespace: namespace,
					},
					Timestamp: metav1.Time{Time: time.Now()},
					Containers: []metricsapi.ContainerMetrics{
						{
							Name: "container",
							Usage: v1.ResourceList{
								v1.ResourceCPU: *resource.NewMilliQuantity(
									int64(cpu),
									resource.DecimalSI),
								v1.ResourceMemory: *resource.NewQuantity(
									int64(1024*1024),
									resource.BinarySI),
							},
						},
					},
				}
				metrics.Items = append(metrics.Items, podMetric)
			}
			heapsterRawMemResponse, _ = json.Marshal(&metrics)
		} else {
			// only return the pods that we actually asked for
			proxyAction := action.(core.ProxyGetAction)
			pathParts := strings.Split(proxyAction.GetPath(), "/")
			// pathParts should look like [ api, v1, model, namespaces, $NS, pod-list, $PODS, metrics, $METRIC... ]
			if len(pathParts) < 9 {
				return true, nil, fmt.Errorf("invalid heapster path %q", proxyAction.GetPath())
			}

			podNames := strings.Split(pathParts[7], ",")
			podPresent := make([]bool, len(tc.reportedLevels))
			for _, name := range podNames {
				if len(name) <= len(podNamePrefix)+1 {
					return true, nil, fmt.Errorf("unknown pod %q", name)
				}
				num, err := strconv.Atoi(name[len(podNamePrefix)+1:])
				if err != nil {
					return true, nil, fmt.Errorf("unknown pod %q", name)
				}
				podPresent[num] = true
			}

			timestamp := time.Now()
			metrics := heapster.MetricResultList{}
			for i, level := range tc.reportedLevels {
				if !podPresent[i] {
					continue
				}

				metric := heapster.MetricResult{
					Metrics:         []heapster.MetricPoint{{Timestamp: timestamp, Value: level, FloatValue: nil}},
					LatestTimestamp: timestamp,
				}
				metrics.Items = append(metrics.Items, metric)
			}
			heapsterRawMemResponse, _ = json.Marshal(&metrics)
		}

		return true, newFakeResponseWrapper(heapsterRawMemResponse), nil
	})

	fakeClient.AddReactor("update", "horizontalpodautoscalers", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		obj := func() *autoscalingv1.HorizontalPodAutoscaler {
			tc.Lock()
			defer tc.Unlock()

			obj := action.(core.UpdateAction).GetObject().(*autoscalingv1.HorizontalPodAutoscaler)
			assert.Equal(t, namespace, obj.Namespace, "the HPA namespace should be as expected")
			assert.Equal(t, hpaName, obj.Name, "the HPA name should be as expected")
			assert.Equal(t, tc.desiredReplicas, obj.Status.DesiredReplicas, "the desired replica count reported in the object status should be as expected")
			if tc.verifyCPUCurrent {
				if assert.NotNil(t, obj.Status.CurrentCPUUtilizationPercentage, "the reported CPU utilization percentage should be non-nil") {
					assert.Equal(t, tc.CPUCurrent, *obj.Status.CurrentCPUUtilizationPercentage, "the report CPU utilization percentage should be as expected")
				}
			}
			tc.statusUpdated = true
			return obj
		}()
		// Every time we reconcile HPA object we are updating status.
		tc.processed <- obj.Name
		return true, obj, nil
	})

	fakeScaleClient := &scalefake.FakeScaleClient{}
	fakeScaleClient.AddReactor("get", "replicationcontrollers", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		tc.Lock()
		defer tc.Unlock()

		obj := &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tc.resource.name,
				Namespace: namespace,
			},
			Spec: autoscalingv1.ScaleSpec{
				Replicas: tc.initialReplicas,
			},
			Status: autoscalingv1.ScaleStatus{
				Replicas: tc.initialReplicas,
				Selector: selector,
			},
		}
		return true, obj, nil
	})

	fakeScaleClient.AddReactor("get", "deployments", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		tc.Lock()
		defer tc.Unlock()

		obj := &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tc.resource.name,
				Namespace: namespace,
			},
			Spec: autoscalingv1.ScaleSpec{
				Replicas: tc.initialReplicas,
			},
			Status: autoscalingv1.ScaleStatus{
				Replicas: tc.initialReplicas,
				Selector: selector,
			},
		}
		return true, obj, nil
	})

	fakeScaleClient.AddReactor("get", "replicasets", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		tc.Lock()
		defer tc.Unlock()

		obj := &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tc.resource.name,
				Namespace: namespace,
			},
			Spec: autoscalingv1.ScaleSpec{
				Replicas: tc.initialReplicas,
			},
			Status: autoscalingv1.ScaleStatus{
				Replicas: tc.initialReplicas,
				Selector: selector,
			},
		}
		return true, obj, nil
	})

	fakeScaleClient.AddReactor("update", "replicationcontrollers", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		tc.Lock()
		defer tc.Unlock()

		obj := action.(core.UpdateAction).GetObject().(*autoscalingv1.Scale)
		replicas := action.(core.UpdateAction).GetObject().(*autoscalingv1.Scale).Spec.Replicas
		assert.Equal(t, tc.desiredReplicas, replicas, "the replica count of the RC should be as expected")
		tc.scaleUpdated = true
		return true, obj, nil
	})

	fakeScaleClient.AddReactor("update", "deployments", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		tc.Lock()
		defer tc.Unlock()

		obj := action.(core.UpdateAction).GetObject().(*autoscalingv1.Scale)
		replicas := action.(core.UpdateAction).GetObject().(*autoscalingv1.Scale).Spec.Replicas
		assert.Equal(t, tc.desiredReplicas, replicas, "the replica count of the deployment should be as expected")
		tc.scaleUpdated = true
		return true, obj, nil
	})

	fakeScaleClient.AddReactor("update", "replicasets", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		tc.Lock()
		defer tc.Unlock()

		obj := action.(core.UpdateAction).GetObject().(*autoscalingv1.Scale)
		replicas := action.(core.UpdateAction).GetObject().(*autoscalingv1.Scale).Spec.Replicas
		assert.Equal(t, tc.desiredReplicas, replicas, "the replica count of the replicaset should be as expected")
		tc.scaleUpdated = true
		return true, obj, nil
	})

	fakeWatch := watch.NewFake()
	fakeClient.AddWatchReactor("*", core.DefaultWatchReactor(fakeWatch, nil))

	return fakeClient, fakeScaleClient
}

func (tc *legacyTestCase) verifyResults(t *testing.T) {
	tc.Lock()
	defer tc.Unlock()

	assert.Equal(t, tc.initialReplicas != tc.desiredReplicas, tc.scaleUpdated, "the scale should only be updated if we expected a change in replicas")
	assert.True(t, tc.statusUpdated, "the status should have been updated")
	if tc.verifyEvents {
		assert.Equal(t, tc.initialReplicas != tc.desiredReplicas, tc.eventCreated, "an event should have been created only if we expected a change in replicas")
	}
}

func (tc *legacyTestCase) runTest(t *testing.T) {
	testClient, testScaleClient := tc.prepareTestClient(t)
	metricsClient := metrics.NewHeapsterMetricsClient(testClient, metrics.DefaultHeapsterNamespace, metrics.DefaultHeapsterScheme, metrics.DefaultHeapsterService, metrics.DefaultHeapsterPort)
	eventClient := &fake.Clientset{}
	eventClient.AddReactor("*", "events", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		tc.Lock()
		defer tc.Unlock()

		if tc.finished {
			return true, &v1.Event{}, nil
		}
		create, ok := action.(core.CreateAction)
		if !ok {
			return false, nil, nil
		}
		obj := create.GetObject().(*v1.Event)
		if tc.verifyEvents {
			switch obj.Reason {
			case "SuccessfulRescale":
				assert.Equal(t, fmt.Sprintf("New size: %d; reason: cpu resource utilization (percentage of request) above target", tc.desiredReplicas), obj.Message)
			case "DesiredReplicasComputed":
				assert.Equal(t, fmt.Sprintf(
					"Computed the desired num of replicas: %d (avgCPUutil: %d, current replicas: %d)",
					tc.desiredReplicas,
					(int64(tc.reportedLevels[0])*100)/tc.reportedCPURequests[0].MilliValue(), tc.initialReplicas), obj.Message)
			default:
				assert.False(t, true, fmt.Sprintf("Unexpected event: %s / %s", obj.Reason, obj.Message))
			}
		}
		tc.eventCreated = true
		return true, obj, nil
	})

	informerFactory := informers.NewSharedInformerFactory(testClient, controller.NoResyncPeriodFunc())
	defaultDownscaleStabilisationWindow := 5 * time.Minute

	hpaController := NewHorizontalController(
		eventClient.CoreV1(),
		testScaleClient,
		testClient.AutoscalingV1(),
		testrestmapper.TestOnlyStaticRESTMapper(legacyscheme.Scheme),
		metricsClient,
		informerFactory.Autoscaling().V1().HorizontalPodAutoscalers(),
		informerFactory.Core().V1().Pods(),
		controller.NoResyncPeriodFunc(),
		defaultDownscaleStabilisationWindow,
		defaultTestingTolerance,
		defaultTestingCPUInitializationPeriod,
		defaultTestingDelayOfInitialReadinessStatus,
	)
	hpaController.hpaListerSynced = alwaysReady

	if tc.recommendations != nil {
		hpaController.recommendations["test-namespace/test-hpa"] = tc.recommendations
	}

	stop := make(chan struct{})
	defer close(stop)
	informerFactory.Start(stop)
	go hpaController.Run(stop)

	// Wait for HPA to be processed.
	<-tc.processed
	tc.Lock()
	tc.finished = true
	if tc.verifyEvents {
		tc.Unlock()
		// We need to wait for events to be broadcasted (sleep for longer than record.sleepDuration).
		time.Sleep(2 * time.Second)
	} else {
		tc.Unlock()
	}
	tc.verifyResults(t)

}

func TestLegacyScaleUp(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:         2,
		maxReplicas:         6,
		initialReplicas:     3,
		desiredReplicas:     5,
		CPUTarget:           30,
		verifyCPUCurrent:    true,
		reportedLevels:      []uint64{300, 500, 700},
		reportedCPURequests: []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
		useMetricsAPI:       true,
	}
	tc.runTest(t)
}

func TestLegacyScaleUpUnreadyLessScale(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:          2,
		maxReplicas:          6,
		initialReplicas:      3,
		desiredReplicas:      4,
		CPUTarget:            30,
		verifyCPUCurrent:     false,
		reportedLevels:       []uint64{300, 500, 700},
		reportedCPURequests:  []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
		reportedPodReadiness: []v1.ConditionStatus{v1.ConditionFalse, v1.ConditionTrue, v1.ConditionTrue},
		useMetricsAPI:        true,
	}
	tc.runTest(t)
}

func TestLegacyScaleUpUnreadyNoScale(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:          2,
		maxReplicas:          6,
		initialReplicas:      3,
		desiredReplicas:      3,
		CPUTarget:            30,
		CPUCurrent:           40,
		verifyCPUCurrent:     true,
		reportedLevels:       []uint64{400, 500, 700},
		reportedCPURequests:  []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
		reportedPodReadiness: []v1.ConditionStatus{v1.ConditionTrue, v1.ConditionFalse, v1.ConditionFalse},
		useMetricsAPI:        true,
	}
	tc.runTest(t)
}

func TestLegacyScaleUpDeployment(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:         2,
		maxReplicas:         6,
		initialReplicas:     3,
		desiredReplicas:     5,
		CPUTarget:           30,
		verifyCPUCurrent:    true,
		reportedLevels:      []uint64{300, 500, 700},
		reportedCPURequests: []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
		useMetricsAPI:       true,
		resource: &fakeResource{
			name:       "test-dep",
			apiVersion: "apps/v1",
			kind:       "Deployment",
		},
	}
	tc.runTest(t)
}

func TestLegacyScaleUpReplicaSet(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:         2,
		maxReplicas:         6,
		initialReplicas:     3,
		desiredReplicas:     5,
		CPUTarget:           30,
		verifyCPUCurrent:    true,
		reportedLevels:      []uint64{300, 500, 700},
		reportedCPURequests: []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
		useMetricsAPI:       true,
		resource: &fakeResource{
			name:       "test-replicaset",
			apiVersion: "apps/v1",
			kind:       "ReplicaSet",
		},
	}
	tc.runTest(t)
}

func TestLegacyScaleUpCM(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:     2,
		maxReplicas:     6,
		initialReplicas: 3,
		desiredReplicas: 4,
		CPUTarget:       0,
		metricsTarget: []autoscalingv2.MetricSpec{
			{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{
					MetricName:         "qps",
					TargetAverageValue: resource.MustParse("15.0"),
				},
			},
		},
		reportedLevels:      []uint64{20, 10, 30},
		reportedCPURequests: []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
	}
	tc.runTest(t)
}

func TestLegacyScaleUpCMUnreadyNoLessScale(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:     2,
		maxReplicas:     6,
		initialReplicas: 3,
		desiredReplicas: 6,
		CPUTarget:       0,
		metricsTarget: []autoscalingv2.MetricSpec{
			{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{
					MetricName:         "qps",
					TargetAverageValue: resource.MustParse("15.0"),
				},
			},
		},
		reportedLevels:       []uint64{50, 10, 30},
		reportedPodReadiness: []v1.ConditionStatus{v1.ConditionTrue, v1.ConditionTrue, v1.ConditionFalse},
		reportedCPURequests:  []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
	}
	tc.runTest(t)
}

func TestLegacyScaleUpCMUnreadyNoScaleWouldScaleDown(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:     2,
		maxReplicas:     6,
		initialReplicas: 3,
		desiredReplicas: 6,
		CPUTarget:       0,
		metricsTarget: []autoscalingv2.MetricSpec{
			{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{
					MetricName:         "qps",
					TargetAverageValue: resource.MustParse("15.0"),
				},
			},
		},
		reportedLevels:       []uint64{50, 15, 30},
		reportedPodReadiness: []v1.ConditionStatus{v1.ConditionFalse, v1.ConditionTrue, v1.ConditionFalse},
		reportedCPURequests:  []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
	}
	tc.runTest(t)
}

func TestLegacyScaleDown(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:         2,
		maxReplicas:         6,
		initialReplicas:     5,
		desiredReplicas:     3,
		CPUTarget:           50,
		verifyCPUCurrent:    true,
		reportedLevels:      []uint64{100, 300, 500, 250, 250},
		reportedCPURequests: []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
		useMetricsAPI:       true,
		recommendations:     []timestampedRecommendation{},
	}
	tc.runTest(t)
}

func TestLegacyScaleDownCM(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:     2,
		maxReplicas:     6,
		initialReplicas: 5,
		desiredReplicas: 3,
		CPUTarget:       0,
		metricsTarget: []autoscalingv2.MetricSpec{
			{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{
					MetricName:         "qps",
					TargetAverageValue: resource.MustParse("20.0"),
				},
			},
		},
		reportedLevels:      []uint64{12, 12, 12, 12, 12},
		reportedCPURequests: []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
		recommendations:     []timestampedRecommendation{},
	}
	tc.runTest(t)
}

func TestLegacyScaleDownIgnoresUnreadyPods(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:          2,
		maxReplicas:          6,
		initialReplicas:      5,
		desiredReplicas:      2,
		CPUTarget:            50,
		CPUCurrent:           30,
		verifyCPUCurrent:     true,
		reportedLevels:       []uint64{100, 300, 500, 250, 250},
		reportedCPURequests:  []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
		useMetricsAPI:        true,
		reportedPodReadiness: []v1.ConditionStatus{v1.ConditionTrue, v1.ConditionTrue, v1.ConditionTrue, v1.ConditionFalse, v1.ConditionFalse},
		recommendations:      []timestampedRecommendation{},
	}
	tc.runTest(t)
}

func TestLegacySuperfluousMetrics(t *testing.T) {
	tc := legacyTestCase{
		minReplicas:         2,
		maxReplicas:         6,
		initialReplicas:     4,
		desiredReplicas:     6,
		CPUTarget:           100,
		reportedLevels:      []uint64{4000, 9500, 3000, 7000, 3200, 2000},
		reportedCPURequests: []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
		useMetricsAPI:       true,
	}
	tc.runTest(t)
}

func TestLegacyScaleUpRCImmediately(t *testing.T) {
	time := metav1.Time{Time: time.Now()}
	tc := legacyTestCase{
		minReplicas:         2,
		maxReplicas:         6,
		initialReplicas:     1,
		desiredReplicas:     2,
		verifyCPUCurrent:    false,
		reportedLevels:      []uint64{0, 0, 0, 0},
		reportedCPURequests: []resource.Quantity{resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0"), resource.MustParse("1.0")},
		useMetricsAPI:       true,
		lastScaleTime:       &time,
	}
	tc.runTest(t)
}

func TestLegacyScaleDownRCImmediately(t *testing.T) {
	time := metav1.Time{Time: time.Now()}
	tc := legacyTestCase{
		minReplicas:         2,
		maxReplicas:         5,
		initialReplicas:     6,
		desiredReplicas:     5,
		CPUTarget:           50,
		reportedLevels:      []uint64{8000, 9500, 1000},
		reportedCPURequests: []resource.Quantity{resource.MustParse("0.9"), resource.MustParse("1.0"), resource.MustParse("1.1")},
		useMetricsAPI:       true,
		lastScaleTime:       &time,
	}
	tc.runTest(t)
}

// TODO: add more tests
