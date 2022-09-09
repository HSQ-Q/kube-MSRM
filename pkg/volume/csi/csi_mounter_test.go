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

package csi

import (
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"reflect"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	authenticationv1 "k8s.io/api/authentication/v1"
	api "k8s.io/api/core/v1"
	storage "k8s.io/api/storage/v1"
	storagev1 "k8s.io/api/storage/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	fakeclient "k8s.io/client-go/kubernetes/fake"
	clitesting "k8s.io/client-go/testing"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	pkgauthenticationv1 "k8s.io/kubernetes/pkg/apis/authentication/v1"
	pkgcorev1 "k8s.io/kubernetes/pkg/apis/core/v1"
	pkgstoragev1 "k8s.io/kubernetes/pkg/apis/storage/v1"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/volume"
	fakecsi "k8s.io/kubernetes/pkg/volume/csi/fake"
	"k8s.io/kubernetes/pkg/volume/util"
	volumetypes "k8s.io/kubernetes/pkg/volume/util/types"
)

var (
	testDriver  = "test-driver"
	testVol     = "vol-123"
	testns      = "test-ns"
	testPod     = "test-pod"
	testPodUID  = types.UID("test-pod")
	testAccount = "test-service-account"
)

func TestMounterGetPath(t *testing.T) {
	plug, tmpDir := newTestPlugin(t, nil)
	defer os.RemoveAll(tmpDir)

	// TODO (vladimirvivien) specName with slashes will not work
	testCases := []struct {
		name           string
		specVolumeName string
		path           string
	}{
		{
			name:           "simple specName",
			specVolumeName: "spec-0",
			path:           filepath.Join(tmpDir, fmt.Sprintf("pods/%s/volumes/kubernetes.io~csi/%s/%s", testPodUID, "spec-0", "/mount")),
		},
		{
			name:           "specName with dots",
			specVolumeName: "test.spec.1",
			path:           filepath.Join(tmpDir, fmt.Sprintf("pods/%s/volumes/kubernetes.io~csi/%s/%s", testPodUID, "test.spec.1", "/mount")),
		},
	}
	for _, tc := range testCases {
		t.Logf("test case: %s", tc.name)
		registerFakePlugin(testDriver, "endpoint", []string{"1.0.0"}, t)
		pv := makeTestPV(tc.specVolumeName, 10, testDriver, testVol)
		spec := volume.NewSpecFromPersistentVolume(pv, pv.Spec.PersistentVolumeSource.CSI.ReadOnly)
		mounter, err := plug.NewMounter(
			spec,
			&api.Pod{ObjectMeta: meta.ObjectMeta{UID: testPodUID, Namespace: testns}},
			volume.VolumeOptions{},
		)
		if err != nil {
			t.Fatalf("Failed to make a new Mounter: %v", err)
		}
		csiMounter := mounter.(*csiMountMgr)

		mountPath := csiMounter.GetPath()

		if tc.path != mountPath {
			t.Errorf("expecting path %s, got %s", tc.path, mountPath)
		}
	}
}

func TestMounterSetUp(t *testing.T) {
	tests := []struct {
		name                  string
		driver                string
		volumeContext         map[string]string
		expectedVolumeContext map[string]string
		csiInlineVolume       bool
	}{
		{
			name:                  "no pod info",
			driver:                "no-info",
			volumeContext:         nil,
			expectedVolumeContext: nil,
		},
		{
			name:                  "no CSIDriver -> no pod info",
			driver:                "unknown-driver",
			volumeContext:         nil,
			expectedVolumeContext: nil,
		},
		{
			name:                  "CSIDriver with PodInfoRequiredOnMount=nil -> no pod info",
			driver:                "nil",
			volumeContext:         nil,
			expectedVolumeContext: nil,
		},
		{
			name:                  "no pod info -> keep existing volumeContext",
			driver:                "no-info",
			volumeContext:         map[string]string{"foo": "bar"},
			expectedVolumeContext: map[string]string{"foo": "bar"},
		},
		{
			name:                  "add pod info",
			driver:                "info",
			volumeContext:         nil,
			expectedVolumeContext: map[string]string{"csi.storage.k8s.io/pod.uid": "test-pod", "csi.storage.k8s.io/serviceAccount.name": "test-service-account", "csi.storage.k8s.io/pod.name": "test-pod", "csi.storage.k8s.io/pod.namespace": "test-ns"},
		},
		{
			name:                  "add pod info -> keep existing volumeContext",
			driver:                "info",
			volumeContext:         map[string]string{"foo": "bar"},
			expectedVolumeContext: map[string]string{"foo": "bar", "csi.storage.k8s.io/pod.uid": "test-pod", "csi.storage.k8s.io/serviceAccount.name": "test-service-account", "csi.storage.k8s.io/pod.name": "test-pod", "csi.storage.k8s.io/pod.namespace": "test-ns"},
		},
		{
			name:                  "CSIInlineVolume pod info",
			driver:                "info",
			volumeContext:         nil,
			expectedVolumeContext: map[string]string{"csi.storage.k8s.io/pod.uid": "test-pod", "csi.storage.k8s.io/serviceAccount.name": "test-service-account", "csi.storage.k8s.io/pod.name": "test-pod", "csi.storage.k8s.io/pod.namespace": "test-ns", "csi.storage.k8s.io/ephemeral": "false"},
			csiInlineVolume:       true,
		},
	}

	noPodMountInfo := false
	currentPodInfoMount := true
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Modes must be set if (and only if) CSIInlineVolume is enabled.
			var modes []storage.VolumeLifecycleMode
			defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CSIInlineVolume, test.csiInlineVolume)()
			if test.csiInlineVolume {
				modes = append(modes, storage.VolumeLifecyclePersistent)
			}
			fakeClient := fakeclient.NewSimpleClientset(
				getTestCSIDriver("no-info", &noPodMountInfo, nil, modes),
				getTestCSIDriver("info", &currentPodInfoMount, nil, modes),
				getTestCSIDriver("nil", nil, nil, modes),
			)
			plug, tmpDir := newTestPlugin(t, fakeClient)
			defer os.RemoveAll(tmpDir)

			registerFakePlugin(test.driver, "endpoint", []string{"1.0.0"}, t)
			pv := makeTestPV("test-pv", 10, test.driver, testVol)
			pv.Spec.CSI.VolumeAttributes = test.volumeContext
			pv.Spec.MountOptions = []string{"foo=bar", "baz=qux"}
			pvName := pv.GetName()

			mounter, err := plug.NewMounter(
				volume.NewSpecFromPersistentVolume(pv, pv.Spec.PersistentVolumeSource.CSI.ReadOnly),
				&api.Pod{
					ObjectMeta: meta.ObjectMeta{UID: testPodUID, Namespace: testns, Name: testPod},
					Spec: api.PodSpec{
						ServiceAccountName: testAccount,
					},
				},
				volume.VolumeOptions{},
			)
			if err != nil {
				t.Fatalf("failed to make a new Mounter: %v", err)
			}

			if mounter == nil {
				t.Fatal("failed to create CSI mounter")
			}

			csiMounter := mounter.(*csiMountMgr)
			csiMounter.csiClient = setupClient(t, true)

			attachID := getAttachmentName(csiMounter.volumeID, string(csiMounter.driverName), string(plug.host.GetNodeName()))

			attachment := &storage.VolumeAttachment{
				ObjectMeta: meta.ObjectMeta{
					Name: attachID,
				},
				Spec: storage.VolumeAttachmentSpec{
					NodeName: "test-node",
					Attacher: CSIPluginName,
					Source: storage.VolumeAttachmentSource{
						PersistentVolumeName: &pvName,
					},
				},
				Status: storage.VolumeAttachmentStatus{
					Attached:    false,
					AttachError: nil,
					DetachError: nil,
				},
			}
			_, err = csiMounter.k8s.StorageV1().VolumeAttachments().Create(context.TODO(), attachment, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("failed to setup VolumeAttachment: %v", err)
			}

			// Mounter.SetUp()
			var mounterArgs volume.MounterArgs
			fsGroup := int64(2000)
			mounterArgs.FsGroup = &fsGroup
			if err := csiMounter.SetUp(mounterArgs); err != nil {
				t.Fatalf("mounter.Setup failed: %v", err)
			}

			//Test the default value of file system type is not overridden
			if len(csiMounter.spec.PersistentVolume.Spec.CSI.FSType) != 0 {
				t.Errorf("default value of file system type was overridden by type %s", csiMounter.spec.PersistentVolume.Spec.CSI.FSType)
			}

			mountPath := csiMounter.GetPath()
			if _, err := os.Stat(mountPath); err != nil {
				if os.IsNotExist(err) {
					t.Errorf("SetUp() failed, volume path not created: %s", mountPath)
				} else {
					t.Errorf("SetUp() failed: %v", err)
				}
			}

			// ensure call went all the way
			pubs := csiMounter.csiClient.(*fakeCsiDriverClient).nodeClient.GetNodePublishedVolumes()
			vol, ok := pubs[csiMounter.volumeID]
			if !ok {
				t.Error("csi server may not have received NodePublishVolume call")
			}
			if vol.Path != csiMounter.GetPath() {
				t.Errorf("csi server expected path %s, got %s", csiMounter.GetPath(), vol.Path)
			}
			if !reflect.DeepEqual(vol.MountFlags, pv.Spec.MountOptions) {
				t.Errorf("csi server expected mount options %v, got %v", pv.Spec.MountOptions, vol.MountFlags)
			}
			if !reflect.DeepEqual(vol.VolumeContext, test.expectedVolumeContext) {
				t.Errorf("csi server expected volumeContext %+v, got %+v", test.expectedVolumeContext, vol.VolumeContext)
			}
		})
	}
}

func TestMounterSetUpSimple(t *testing.T) {
	fakeClient := fakeclient.NewSimpleClientset()
	plug, tmpDir := newTestPlugin(t, fakeClient)
	defer os.RemoveAll(tmpDir)

	testCases := []struct {
		name       string
		podUID     types.UID
		mode       storage.VolumeLifecycleMode
		fsType     string
		options    []string
		spec       func(string, []string) *volume.Spec
		shouldFail bool
	}{
		{
			name:       "setup with ephemeral source",
			podUID:     types.UID(fmt.Sprintf("%08X", rand.Uint64())),
			mode:       storage.VolumeLifecycleEphemeral,
			fsType:     "ext4",
			shouldFail: true,
			spec: func(fsType string, options []string) *volume.Spec {
				volSrc := makeTestVol("pv1", testDriver)
				volSrc.CSI.FSType = &fsType
				return volume.NewSpecFromVolume(volSrc)
			},
		},
		{
			name:   "setup with persistent source",
			podUID: types.UID(fmt.Sprintf("%08X", rand.Uint64())),
			mode:   storage.VolumeLifecyclePersistent,
			fsType: "zfs",
			spec: func(fsType string, options []string) *volume.Spec {
				pvSrc := makeTestPV("pv1", 20, testDriver, "vol1")
				pvSrc.Spec.CSI.FSType = fsType
				pvSrc.Spec.MountOptions = options
				return volume.NewSpecFromPersistentVolume(pvSrc, false)
			},
		},
		{
			name:   "setup with persistent source without unspecified fstype and options",
			podUID: types.UID(fmt.Sprintf("%08X", rand.Uint64())),
			mode:   storage.VolumeLifecyclePersistent,
			spec: func(fsType string, options []string) *volume.Spec {
				return volume.NewSpecFromPersistentVolume(makeTestPV("pv1", 20, testDriver, "vol2"), false)
			},
		},
		{
			name:       "setup with missing spec",
			shouldFail: true,
			spec:       func(fsType string, options []string) *volume.Spec { return nil },
		},
	}

	for _, tc := range testCases {
		registerFakePlugin(testDriver, "endpoint", []string{"1.0.0"}, t)
		t.Run(tc.name, func(t *testing.T) {
			mounter, err := plug.NewMounter(
				tc.spec(tc.fsType, tc.options),
				&api.Pod{ObjectMeta: meta.ObjectMeta{UID: tc.podUID, Namespace: testns}},
				volume.VolumeOptions{},
			)
			if tc.shouldFail && err != nil {
				t.Log(err)
				return
			}
			if !tc.shouldFail && err != nil {
				t.Fatal("unexpected error:", err)
			}
			if mounter == nil {
				t.Fatal("failed to create CSI mounter")
			}

			csiMounter := mounter.(*csiMountMgr)
			csiMounter.csiClient = setupClient(t, true)

			if csiMounter.volumeLifecycleMode != storage.VolumeLifecyclePersistent {
				t.Fatal("unexpected volume mode: ", csiMounter.volumeLifecycleMode)
			}

			attachID := getAttachmentName(csiMounter.volumeID, string(csiMounter.driverName), string(plug.host.GetNodeName()))
			attachment := makeTestAttachment(attachID, "test-node", csiMounter.spec.Name())
			_, err = csiMounter.k8s.StorageV1().VolumeAttachments().Create(context.TODO(), attachment, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("failed to setup VolumeAttachment: %v", err)
			}

			// Mounter.SetUp()
			if err := csiMounter.SetUp(volume.MounterArgs{}); err != nil {
				t.Fatalf("mounter.Setup failed: %v", err)
			}

			// ensure call went all the way
			pubs := csiMounter.csiClient.(*fakeCsiDriverClient).nodeClient.GetNodePublishedVolumes()
			vol, ok := pubs[csiMounter.volumeID]
			if !ok {
				t.Error("csi server may not have received NodePublishVolume call")
			}
			if vol.VolumeHandle != csiMounter.volumeID {
				t.Error("volumeHandle not sent to CSI driver properly")
			}

			devicePath, err := makeDeviceMountPath(plug, csiMounter.spec)
			if err != nil {
				t.Fatal(err)
			}
			if vol.DeviceMountPath != devicePath {
				t.Errorf("DeviceMountPath not sent properly to CSI driver: %s, %s", vol.DeviceMountPath, devicePath)
			}

			if !reflect.DeepEqual(vol.MountFlags, csiMounter.spec.PersistentVolume.Spec.MountOptions) {
				t.Errorf("unexpected mount flags passed to driver: %+v", vol.MountFlags)
			}

			if vol.FSType != tc.fsType {
				t.Error("unexpected FSType sent to driver:", vol.FSType)
			}

			if vol.Path != csiMounter.GetPath() {
				t.Error("csi server may not have received NodePublishVolume call")
			}
		})
	}
}

func TestMounterSetupWithStatusTracking(t *testing.T) {
	fakeClient := fakeclient.NewSimpleClientset()
	plug, tmpDir := newTestPlugin(t, fakeClient)
	defer os.RemoveAll(tmpDir)
	nonFinalError := volumetypes.NewUncertainProgressError("non-final-error")
	transientError := volumetypes.NewTransientOperationFailure("transient-error")

	testCases := []struct {
		name             string
		podUID           types.UID
		spec             func(string, []string) *volume.Spec
		shouldFail       bool
		exitError        error
		createAttachment bool
	}{
		{
			name:   "setup with correct persistent volume source should result in finish exit status",
			podUID: types.UID(fmt.Sprintf("%08X", rand.Uint64())),
			spec: func(fsType string, options []string) *volume.Spec {
				pvSrc := makeTestPV("pv1", 20, testDriver, "vol1")
				pvSrc.Spec.CSI.FSType = fsType
				pvSrc.Spec.MountOptions = options
				return volume.NewSpecFromPersistentVolume(pvSrc, false)
			},
			createAttachment: true,
		},
		{
			name:   "setup with missing attachment should result in nochange",
			podUID: types.UID(fmt.Sprintf("%08X", rand.Uint64())),
			spec: func(fsType string, options []string) *volume.Spec {
				return volume.NewSpecFromPersistentVolume(makeTestPV("pv3", 20, testDriver, "vol4"), false)
			},
			exitError:        transientError,
			createAttachment: false,
			shouldFail:       true,
		},
		{
			name:   "setup with timeout errors on NodePublish",
			podUID: types.UID(fmt.Sprintf("%08X", rand.Uint64())),
			spec: func(fsType string, options []string) *volume.Spec {
				return volume.NewSpecFromPersistentVolume(makeTestPV("pv4", 20, testDriver, fakecsi.NodePublishTimeOut_VolumeID), false)
			},
			createAttachment: true,
			exitError:        nonFinalError,
			shouldFail:       true,
		},
		{
			name:   "setup with missing secrets should result in nochange exit",
			podUID: types.UID(fmt.Sprintf("%08X", rand.Uint64())),
			spec: func(fsType string, options []string) *volume.Spec {
				pv := makeTestPV("pv5", 20, testDriver, "vol6")
				pv.Spec.PersistentVolumeSource.CSI.NodePublishSecretRef = &api.SecretReference{
					Name:      "foo",
					Namespace: "default",
				}
				return volume.NewSpecFromPersistentVolume(pv, false)
			},
			exitError:        transientError,
			createAttachment: true,
			shouldFail:       true,
		},
	}

	for _, tc := range testCases {
		registerFakePlugin(testDriver, "endpoint", []string{"1.0.0"}, t)
		t.Run(tc.name, func(t *testing.T) {
			mounter, err := plug.NewMounter(
				tc.spec("ext4", []string{}),
				&api.Pod{ObjectMeta: meta.ObjectMeta{UID: tc.podUID, Namespace: testns}},
				volume.VolumeOptions{},
			)
			if err != nil {
				t.Fatalf("failed to create CSI mounter: %v", err)
			}

			csiMounter := mounter.(*csiMountMgr)
			csiMounter.csiClient = setupClient(t, true)

			if csiMounter.volumeLifecycleMode != storage.VolumeLifecyclePersistent {
				t.Fatal("unexpected volume mode: ", csiMounter.volumeLifecycleMode)
			}

			if tc.createAttachment {
				attachID := getAttachmentName(csiMounter.volumeID, string(csiMounter.driverName), string(plug.host.GetNodeName()))
				attachment := makeTestAttachment(attachID, "test-node", csiMounter.spec.Name())
				_, err = csiMounter.k8s.StorageV1().VolumeAttachments().Create(context.TODO(), attachment, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("failed to setup VolumeAttachment: %v", err)
				}
			}
			err = csiMounter.SetUp(volume.MounterArgs{})

			if tc.exitError != nil && reflect.TypeOf(tc.exitError) != reflect.TypeOf(err) {
				t.Fatalf("expected exitError: %+v got: %+v", tc.exitError, err)
			}

			if tc.shouldFail && err == nil {
				t.Fatalf("expected failure but Setup succeeded")
			}

			if !tc.shouldFail && err != nil {
				t.Fatalf("expected success got mounter.Setup failed with: %v", err)
			}
		})
	}
}

func TestMounterSetUpWithInline(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CSIInlineVolume, true)()

	testCases := []struct {
		name       string
		podUID     types.UID
		mode       storage.VolumeLifecycleMode
		fsType     string
		options    []string
		spec       func(string, []string) *volume.Spec
		shouldFail bool
	}{
		{
			name:   "setup with vol source",
			podUID: types.UID(fmt.Sprintf("%08X", rand.Uint64())),
			mode:   storage.VolumeLifecycleEphemeral,
			fsType: "ext4",
			spec: func(fsType string, options []string) *volume.Spec {
				volSrc := makeTestVol("pv1", testDriver)
				volSrc.CSI.FSType = &fsType
				return volume.NewSpecFromVolume(volSrc)
			},
		},
		{
			name:   "setup with persistent source",
			podUID: types.UID(fmt.Sprintf("%08X", rand.Uint64())),
			mode:   storage.VolumeLifecyclePersistent,
			fsType: "zfs",
			spec: func(fsType string, options []string) *volume.Spec {
				pvSrc := makeTestPV("pv1", 20, testDriver, "vol1")
				pvSrc.Spec.CSI.FSType = fsType
				pvSrc.Spec.MountOptions = options
				return volume.NewSpecFromPersistentVolume(pvSrc, false)
			},
		},
		{
			name:   "setup with persistent source without unspecified fstype and options",
			podUID: types.UID(fmt.Sprintf("%08X", rand.Uint64())),
			mode:   storage.VolumeLifecyclePersistent,
			spec: func(fsType string, options []string) *volume.Spec {
				return volume.NewSpecFromPersistentVolume(makeTestPV("pv1", 20, testDriver, "vol2"), false)
			},
		},
		{
			name:       "setup with missing spec",
			shouldFail: true,
			spec:       func(fsType string, options []string) *volume.Spec { return nil },
		},
	}

	for _, tc := range testCases {
		// The fake driver currently supports all modes.
		volumeLifecycleModes := []storage.VolumeLifecycleMode{
			storage.VolumeLifecycleEphemeral,
			storage.VolumeLifecyclePersistent,
		}
		driver := getTestCSIDriver(testDriver, nil, nil, volumeLifecycleModes)
		fakeClient := fakeclient.NewSimpleClientset(driver)
		plug, tmpDir := newTestPlugin(t, fakeClient)
		defer os.RemoveAll(tmpDir)
		registerFakePlugin(testDriver, "endpoint", []string{"1.0.0"}, t)
		t.Run(tc.name, func(t *testing.T) {
			mounter, err := plug.NewMounter(
				tc.spec(tc.fsType, tc.options),
				&api.Pod{ObjectMeta: meta.ObjectMeta{UID: tc.podUID, Namespace: testns}},
				volume.VolumeOptions{},
			)
			if tc.shouldFail && err != nil {
				t.Log(err)
				return
			}
			if !tc.shouldFail && err != nil {
				t.Fatal("unexpected error:", err)
			}
			if mounter == nil {
				t.Fatal("failed to create CSI mounter")
			}

			csiMounter := mounter.(*csiMountMgr)
			csiMounter.csiClient = setupClient(t, true)

			if csiMounter.volumeLifecycleMode != tc.mode {
				t.Fatal("unexpected volume mode: ", csiMounter.volumeLifecycleMode)
			}

			if csiMounter.volumeLifecycleMode == storage.VolumeLifecycleEphemeral && csiMounter.volumeID != makeVolumeHandle(string(tc.podUID), csiMounter.specVolumeID) {
				t.Fatal("unexpected generated volumeHandle:", csiMounter.volumeID)
			}

			if csiMounter.volumeLifecycleMode == storage.VolumeLifecyclePersistent {
				attachID := getAttachmentName(csiMounter.volumeID, string(csiMounter.driverName), string(plug.host.GetNodeName()))
				attachment := makeTestAttachment(attachID, "test-node", csiMounter.spec.Name())
				_, err = csiMounter.k8s.StorageV1().VolumeAttachments().Create(context.TODO(), attachment, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("failed to setup VolumeAttachment: %v", err)
				}
			}

			// Mounter.SetUp()
			if err := csiMounter.SetUp(volume.MounterArgs{}); err != nil {
				t.Fatalf("mounter.Setup failed: %v", err)
			}

			// ensure call went all the way
			pubs := csiMounter.csiClient.(*fakeCsiDriverClient).nodeClient.GetNodePublishedVolumes()
			vol, ok := pubs[csiMounter.volumeID]
			if !ok {
				t.Error("csi server may not have received NodePublishVolume call")
			}
			if vol.VolumeHandle != csiMounter.volumeID {
				t.Error("volumeHandle not sent to CSI driver properly")
			}

			// validate stagingTargetPath
			if tc.mode == storage.VolumeLifecycleEphemeral && vol.DeviceMountPath != "" {
				t.Errorf("unexpected devicePathTarget sent to driver: %s", vol.DeviceMountPath)
			}
			if tc.mode == storage.VolumeLifecyclePersistent {
				devicePath, err := makeDeviceMountPath(plug, csiMounter.spec)
				if err != nil {
					t.Fatal(err)
				}
				if vol.DeviceMountPath != devicePath {
					t.Errorf("DeviceMountPath not sent properly to CSI driver: %s, %s", vol.DeviceMountPath, devicePath)
				}

				if !reflect.DeepEqual(vol.MountFlags, csiMounter.spec.PersistentVolume.Spec.MountOptions) {
					t.Errorf("unexpected mount flags passed to driver: %+v", vol.MountFlags)
				}
			}

			if vol.FSType != tc.fsType {
				t.Error("unexpected FSType sent to driver:", vol.FSType)
			}

			if vol.Path != csiMounter.GetPath() {
				t.Error("csi server may not have received NodePublishVolume call")
			}
		})
	}
}

func TestMounterSetUpWithFSGroup(t *testing.T) {
	fakeClient := fakeclient.NewSimpleClientset()
	plug, tmpDir := newTestPlugin(t, fakeClient)
	defer os.RemoveAll(tmpDir)

	testCases := []struct {
		name                string
		accessModes         []api.PersistentVolumeAccessMode
		readOnly            bool
		fsType              string
		setFsGroup          bool
		fsGroup             int64
		driverFSGroupPolicy bool
		supportMode         storage.FSGroupPolicy
	}{
		{
			name: "default fstype, with no fsgroup (should not apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteOnce,
			},
			readOnly: false,
			fsType:   "",
		},
		{
			name: "default fstype  with fsgroup (should not apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteOnce,
			},
			readOnly:   false,
			fsType:     "",
			setFsGroup: true,
			fsGroup:    3000,
		},
		{
			name: "fstype, fsgroup, RWM, ROM provided (should not apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteMany,
				api.ReadOnlyMany,
			},
			fsType:     "ext4",
			setFsGroup: true,
			fsGroup:    3000,
		},
		{
			name: "fstype, fsgroup, RWO, but readOnly (should not apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteOnce,
			},
			readOnly:   true,
			fsType:     "ext4",
			setFsGroup: true,
			fsGroup:    3000,
		},
		{
			name: "fstype, fsgroup, RWO provided (should apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteOnce,
			},
			fsType:     "ext4",
			setFsGroup: true,
			fsGroup:    3000,
		},
		{
			name: "fstype, fsgroup, RWO provided, FSGroupPolicy ReadWriteOnceWithFSType (should apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteOnce,
			},
			fsType:              "ext4",
			setFsGroup:          true,
			fsGroup:             3000,
			driverFSGroupPolicy: true,
			supportMode:         storage.ReadWriteOnceWithFSTypeFSGroupPolicy,
		},
		{
			name: "default fstype with no fsgroup, FSGroupPolicy ReadWriteOnceWithFSType (should not apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteOnce,
			},
			readOnly:            false,
			fsType:              "",
			driverFSGroupPolicy: true,
			supportMode:         storage.ReadWriteOnceWithFSTypeFSGroupPolicy,
		},
		{
			name: "default fstype with fsgroup, FSGroupPolicy ReadWriteOnceWithFSType (should not apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteOnce,
			},
			readOnly:            false,
			fsType:              "",
			setFsGroup:          true,
			fsGroup:             3000,
			driverFSGroupPolicy: true,
			supportMode:         storage.ReadWriteOnceWithFSTypeFSGroupPolicy,
		},
		{
			name: "fstype, fsgroup, RWO provided, readonly, FSGroupPolicy ReadWriteOnceWithFSType (should not apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteOnce,
			},
			readOnly:            true,
			fsType:              "ext4",
			setFsGroup:          true,
			fsGroup:             3000,
			driverFSGroupPolicy: true,
			supportMode:         storage.ReadWriteOnceWithFSTypeFSGroupPolicy,
		},
		{
			name: "fstype, fsgroup, RWX provided, FSGroupPolicy ReadWriteOnceWithFSType (should not apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteMany,
			},
			readOnly:            false,
			fsType:              "ext4",
			setFsGroup:          true,
			fsGroup:             3000,
			driverFSGroupPolicy: true,
			supportMode:         storage.ReadWriteOnceWithFSTypeFSGroupPolicy,
		},
		{
			name: "fstype, fsgroup, RWO provided, FSGroupPolicy None (should not apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteOnce,
			},
			fsType:              "ext4",
			setFsGroup:          true,
			fsGroup:             3000,
			driverFSGroupPolicy: true,
			supportMode:         storage.NoneFSGroupPolicy,
		},
		{
			name: "fstype, fsgroup, RWO provided, readOnly, FSGroupPolicy File (should apply fsgroup)",
			accessModes: []api.PersistentVolumeAccessMode{
				api.ReadWriteOnce,
			},
			readOnly:            true,
			fsType:              "ext4",
			setFsGroup:          true,
			fsGroup:             3000,
			driverFSGroupPolicy: true,
			supportMode:         storage.FileFSGroupPolicy,
		},
	}

	for i, tc := range testCases {
		t.Logf("Running test %s", tc.name)

		defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CSIVolumeFSGroupPolicy, tc.driverFSGroupPolicy)()

		volName := fmt.Sprintf("test-vol-%d", i)
		registerFakePlugin(testDriver, "endpoint", []string{"1.0.0"}, t)
		pv := makeTestPV("test-pv", 10, testDriver, volName)
		pv.Spec.AccessModes = tc.accessModes
		pvName := pv.GetName()

		spec := volume.NewSpecFromPersistentVolume(pv, tc.readOnly)

		if tc.fsType != "" {
			spec.PersistentVolume.Spec.CSI.FSType = tc.fsType
		}

		mounter, err := plug.NewMounter(
			spec,
			&api.Pod{ObjectMeta: meta.ObjectMeta{UID: testPodUID, Namespace: testns}},
			volume.VolumeOptions{},
		)
		if err != nil {
			t.Fatalf("Failed to make a new Mounter: %v", err)
		}

		if mounter == nil {
			t.Fatal("failed to create CSI mounter")
		}

		csiMounter := mounter.(*csiMountMgr)
		if tc.driverFSGroupPolicy {
			csiMounter.fsGroupPolicy = tc.supportMode
		}
		csiMounter.csiClient = setupClient(t, true)

		attachID := getAttachmentName(csiMounter.volumeID, string(csiMounter.driverName), string(plug.host.GetNodeName()))
		attachment := makeTestAttachment(attachID, "test-node", pvName)

		_, err = csiMounter.k8s.StorageV1().VolumeAttachments().Create(context.TODO(), attachment, metav1.CreateOptions{})
		if err != nil {
			t.Errorf("failed to setup VolumeAttachment: %v", err)
			continue
		}

		// Mounter.SetUp()
		var mounterArgs volume.MounterArgs
		var fsGroupPtr *int64
		if tc.setFsGroup {
			fsGroup := tc.fsGroup
			fsGroupPtr = &fsGroup
		}
		mounterArgs.FsGroup = fsGroupPtr
		if err := csiMounter.SetUp(mounterArgs); err != nil {
			t.Fatalf("mounter.Setup failed: %v", err)
		}

		//Test the default value of file system type is not overridden
		if len(csiMounter.spec.PersistentVolume.Spec.CSI.FSType) != len(tc.fsType) {
			t.Errorf("file system type was overridden by type %s", csiMounter.spec.PersistentVolume.Spec.CSI.FSType)
		}

		// ensure call went all the way
		pubs := csiMounter.csiClient.(*fakeCsiDriverClient).nodeClient.GetNodePublishedVolumes()
		if pubs[csiMounter.volumeID].Path != csiMounter.GetPath() {
			t.Error("csi server may not have received NodePublishVolume call")
		}
	}
}

func TestUnmounterTeardown(t *testing.T) {
	plug, tmpDir := newTestPlugin(t, nil)
	defer os.RemoveAll(tmpDir)
	registerFakePlugin(testDriver, "endpoint", []string{"1.0.0"}, t)
	pv := makeTestPV("test-pv", 10, testDriver, testVol)

	// save the data file prior to unmount
	dir := filepath.Join(getTargetPath(testPodUID, pv.ObjectMeta.Name, plug.host), "/mount")
	if err := os.MkdirAll(dir, 0755); err != nil && !os.IsNotExist(err) {
		t.Errorf("failed to create dir [%s]: %v", dir, err)
	}

	// do a fake local mount
	diskMounter := util.NewSafeFormatAndMountFromHost(plug.GetPluginName(), plug.host)
	if err := diskMounter.FormatAndMount("/fake/device", dir, "testfs", nil); err != nil {
		t.Errorf("failed to mount dir [%s]: %v", dir, err)
	}

	if err := saveVolumeData(
		path.Dir(dir),
		volDataFileName,
		map[string]string{
			volDataKey.specVolID:  pv.ObjectMeta.Name,
			volDataKey.driverName: testDriver,
			volDataKey.volHandle:  testVol,
		},
	); err != nil {
		t.Fatalf("failed to save volume data: %v", err)
	}

	unmounter, err := plug.NewUnmounter(pv.ObjectMeta.Name, testPodUID)
	if err != nil {
		t.Fatalf("failed to make a new Unmounter: %v", err)
	}

	csiUnmounter := unmounter.(*csiMountMgr)
	csiUnmounter.csiClient = setupClient(t, true)
	err = csiUnmounter.TearDownAt(dir)
	if err != nil {
		t.Fatal(err)
	}

	// ensure csi client call
	pubs := csiUnmounter.csiClient.(*fakeCsiDriverClient).nodeClient.GetNodePublishedVolumes()
	if _, ok := pubs[csiUnmounter.volumeID]; ok {
		t.Error("csi server may not have received NodeUnpublishVolume call")
	}

}

func TestIsCorruptedDir(t *testing.T) {
	existingMountPath, err := ioutil.TempDir(os.TempDir(), "blobfuse-csi-mount-test")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(existingMountPath)

	tests := []struct {
		desc           string
		dir            string
		expectedResult bool
	}{
		{
			desc:           "NotExist dir",
			dir:            "/tmp/NotExist",
			expectedResult: false,
		},
		{
			desc:           "Existing dir",
			dir:            existingMountPath,
			expectedResult: false,
		},
	}

	for i, test := range tests {
		isCorruptedDir := isCorruptedDir(test.dir)
		assert.Equal(t, test.expectedResult, isCorruptedDir, "TestCase[%d]: %s", i, test.desc)
	}
}

func TestPodServiceAccountTokenAttrs(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CSIServiceAccountToken, true)()
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CSIInlineVolume, true)()
	scheme := runtime.NewScheme()
	utilruntime.Must(pkgauthenticationv1.RegisterDefaults(scheme))
	utilruntime.Must(pkgstoragev1.RegisterDefaults(scheme))
	utilruntime.Must(pkgcorev1.RegisterDefaults(scheme))

	gcp := "gcp"

	tests := []struct {
		desc              string
		driver            *storagev1.CSIDriver
		volumeContext     map[string]string
		wantVolumeContext map[string]string
	}{
		{
			desc: "csi driver has no ServiceAccountToken",
			driver: &storagev1.CSIDriver{
				ObjectMeta: meta.ObjectMeta{
					Name: testDriver,
				},
				Spec: storagev1.CSIDriverSpec{},
			},
			wantVolumeContext: nil,
		},
		{
			desc: "one token with empty string as audience",
			driver: &storagev1.CSIDriver{
				ObjectMeta: meta.ObjectMeta{
					Name: testDriver,
				},
				Spec: storagev1.CSIDriverSpec{
					TokenRequests: []storagev1.TokenRequest{
						{
							Audience: "",
						},
					},
				},
			},
			wantVolumeContext: map[string]string{"csi.storage.k8s.io/serviceAccount.tokens": `{"":{"token":"test-ns:test-service-account:3600:[api]","expirationTimestamp":"1970-01-01T00:00:01Z"}}`},
		},
		{
			desc: "one token with non-empty string as audience",
			driver: &storagev1.CSIDriver{
				ObjectMeta: meta.ObjectMeta{
					Name: testDriver,
				},
				Spec: storagev1.CSIDriverSpec{
					TokenRequests: []storagev1.TokenRequest{
						{
							Audience: gcp,
						},
					},
				},
			},
			wantVolumeContext: map[string]string{"csi.storage.k8s.io/serviceAccount.tokens": `{"gcp":{"token":"test-ns:test-service-account:3600:[gcp]","expirationTimestamp":"1970-01-01T00:00:01Z"}}`},
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			registerFakePlugin(testDriver, "endpoint", []string{"1.0.0"}, t)
			client := fakeclient.NewSimpleClientset()
			if test.driver != nil {
				test.driver.Spec.VolumeLifecycleModes = []storage.VolumeLifecycleMode{
					storage.VolumeLifecycleEphemeral,
					storage.VolumeLifecyclePersistent,
				}
				scheme.Default(test.driver)
				client = fakeclient.NewSimpleClientset(test.driver)
			}
			client.PrependReactor("create", "serviceaccounts", clitesting.ReactionFunc(func(action clitesting.Action) (bool, runtime.Object, error) {
				tr := action.(clitesting.CreateAction).GetObject().(*authenticationv1.TokenRequest)
				scheme.Default(tr)
				if len(tr.Spec.Audiences) == 0 {
					tr.Spec.Audiences = []string{"api"}
				}
				tr.Status.Token = fmt.Sprintf("%v:%v:%d:%v", action.GetNamespace(), testAccount, *tr.Spec.ExpirationSeconds, tr.Spec.Audiences)
				tr.Status.ExpirationTimestamp = metav1.NewTime(time.Unix(1, 1))
				return true, tr, nil
			}))
			plug, tmpDir := newTestPlugin(t, client)
			defer os.RemoveAll(tmpDir)
			mounter, err := plug.NewMounter(
				volume.NewSpecFromVolume(makeTestVol("test", testDriver)),
				&api.Pod{
					ObjectMeta: meta.ObjectMeta{UID: testPodUID, Namespace: testns, Name: testPod},
					Spec: api.PodSpec{
						ServiceAccountName: testAccount,
					},
				},
				volume.VolumeOptions{},
			)
			if err != nil {
				t.Fatalf("Failed to create a csi mounter, err: %v", err)
			}

			csiMounter := mounter.(*csiMountMgr)
			csiMounter.csiClient = setupClient(t, false)
			if err := csiMounter.SetUp(volume.MounterArgs{}); err != nil {
				t.Fatalf("mounter.Setup failed: %v", err)
			}

			pubs := csiMounter.csiClient.(*fakeCsiDriverClient).nodeClient.GetNodePublishedVolumes()
			vol, ok := pubs[csiMounter.volumeID]
			if !ok {
				t.Error("csi server may not have received NodePublishVolume call")
			}
			if vol.Path != csiMounter.GetPath() {
				t.Errorf("csi server expected path %s, got %s", csiMounter.GetPath(), vol.Path)
			}
			if diff := cmp.Diff(test.wantVolumeContext, vol.VolumeContext); diff != "" {
				t.Errorf("podServiceAccountTokenAttrs() = diff (-want +got):\n%s", diff)
			}
		})
	}
}
