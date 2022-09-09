/*
Copyright 2016 The Kubernetes Authors.

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

package netpol

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"

	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/onsi/ginkgo"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	e2eskipper "k8s.io/kubernetes/test/e2e/framework/skipper"
	"k8s.io/kubernetes/test/e2e/network/common"
	utilnet "k8s.io/utils/net"
)

const (
	isVerbose = true

	// useFixedNamespaces is useful when working on these tests: instead of creating new pods and
	//   new namespaces for each test run, it creates a fixed set of namespaces and pods, and then
	//   reuses them for each test case.
	// The result: tests run much faster.  However, this should only be used as a convenience for
	//   working on the tests during development.  It should not be enabled in production.
	useFixedNamespaces = false

	// See https://github.com/kubernetes/kubernetes/issues/95879
	// The semantics of the effect of network policies on loopback calls may be undefined: should
	//   they always be ALLOWED; how do Services affect this?
	//   Calico, Cillium, Antrea seem to do different things.
	// Since different CNIs have different results, that causes tests including loopback to fail
	//   on some CNIs.  So let's just ignore loopback calls for the purposes of deciding test pass/fail.
	ignoreLoopback = true
)

var (
	protocolTCP  = v1.ProtocolTCP
	protocolUDP  = v1.ProtocolUDP
	protocolSCTP = v1.ProtocolSCTP

	// addSCTPContainers is a flag to enable SCTP containers on bootstrap.
	addSCTPContainers = false
)

/*
You might be wondering, why are there multiple namespaces used for each test case?

These tests are based on "truth tables" that compare the expected and actual connectivity of each pair of pods.
Since network policies live in namespaces, and peers can be selected by namespace,
howing the connectivity of pods in other namespaces is key information to show whether a network policy is working as intended or not.

We use 3 namespaces each with 3 pods, and probe all combinations ( 9 pods x 9 pods = 81 data points ) -- including cross-namespace calls.

Here's an example of a test run, showing the expected and actual connectivity, along with the differences.  Note how the
visual representation as a truth table greatly aids in understanding what a network policy is intended to do in theory
and what is happening in practice:

		Oct 19 10:34:16.907: INFO: expected:

		-	x/a	x/b	x/c	y/a	y/b	y/c	z/a	z/b	z/c
		x/a	X	.	.	.	.	.	.	.	.
		x/b	X	.	.	.	.	.	.	.	.
		x/c	X	.	.	.	.	.	.	.	.
		y/a	.	.	.	.	.	.	.	.	.
		y/b	.	.	.	.	.	.	.	.	.
		y/c	.	.	.	.	.	.	.	.	.
		z/a	X	.	.	.	.	.	.	.	.
		z/b	X	.	.	.	.	.	.	.	.
		z/c	X	.	.	.	.	.	.	.	.

		Oct 19 10:34:16.907: INFO: observed:

		-	x/a	x/b	x/c	y/a	y/b	y/c	z/a	z/b	z/c
		x/a	X	.	.	.	.	.	.	.	.
		x/b	X	.	.	.	.	.	.	.	.
		x/c	X	.	.	.	.	.	.	.	.
		y/a	.	.	.	.	.	.	.	.	.
		y/b	.	.	.	.	.	.	.	.	.
		y/c	.	.	.	.	.	.	.	.	.
		z/a	X	.	.	.	.	.	.	.	.
		z/b	X	.	.	.	.	.	.	.	.
		z/c	X	.	.	.	.	.	.	.	.

		Oct 19 10:34:16.907: INFO: comparison:

		-	x/a	x/b	x/c	y/a	y/b	y/c	z/a	z/b	z/c
		x/a	.	.	.	.	.	.	.	.	.
		x/b	.	.	.	.	.	.	.	.	.
		x/c	.	.	.	.	.	.	.	.	.
		y/a	.	.	.	.	.	.	.	.	.
		y/b	.	.	.	.	.	.	.	.	.
		y/c	.	.	.	.	.	.	.	.	.
		z/a	.	.	.	.	.	.	.	.	.
		z/b	.	.	.	.	.	.	.	.	.
		z/c	.	.	.	.	.	.	.	.	.
*/

var _ = common.SIGDescribe("Netpol [LinuxOnly]", func() {
	f := framework.NewDefaultFramework("netpol")

	ginkgo.Context("NetworkPolicy between server and client", func() {
		ginkgo.BeforeEach(func() {
			initializeResourcesByFixedNS(f)
		})

		ginkgo.AfterEach(func() {
			if !useFixedNamespaces {
				_, _, _, model, k8s := getK8SModel(f)
				framework.ExpectNoError(k8s.deleteNamespaces(model.NamespaceNames), "unable to clean up netpol namespaces")
			}
		})

		ginkgo.It("should support a 'default-deny-ingress' policy [Feature:NetworkPolicy]", func() {
			nsX, _, _, model, k8s := getK8SModel(f)
			policy := GetDenyIngress("deny-ingress")
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{}, &Peer{Namespace: nsX}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should support a 'default-deny-all' policy [Feature:NetworkPolicy]", func() {
			policy := GetDenyAllWithEgressDNS()
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{}, &Peer{Namespace: nsX}, false)
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy to allow traffic from pods within server namespace based on PodSelector [Feature:NetworkPolicy]", func() {
			allowedPods := metav1.LabelSelector{
				MatchLabels: map[string]string{
					"pod": "b",
				},
			}
			policy := GetAllowIngressByPod("x-a-allows-x-b", map[string]string{"pod": "a"}, &allowedPods)
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			reachability.Expect(NewPodString(nsX, "b"), NewPodString(nsX, "a"), true)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy to allow ingress traffic for a target [Feature:NetworkPolicy] ", func() {
			nsX, _, _, model, k8s := getK8SModel(f)

			ginkgo.By("having a deny all ingress policy", func() {
				// Deny all Ingress traffic policy to pods on namespace nsX
				policy := GetDenyIngress("deny-all")
				CreatePolicy(k8s, policy, nsX)
			})

			// Allow Ingress traffic only to pod x/a from any pod
			allowPolicy := GetAllowIngressForTarget("allow-all-to-a", map[string]string{"pod": "a"})
			CreatePolicy(k8s, allowPolicy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "b"), false)
			reachability.ExpectAllIngress(NewPodString(nsX, "c"), false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy to allow ingress traffic from pods in all namespaces [Feature:NetworkPolicy]", func() {
			nsX, _, _, model, k8s := getK8SModel(f)

			emptyLabel := &metav1.LabelSelector{
				MatchLabels: map[string]string{},
			}
			policy := GetAllowIngressByNamespace("allow-from-another-ns", map[string]string{"pod": "a"}, emptyLabel)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy to allow traffic only from a different namespace, based on NamespaceSelector [Feature:NetworkPolicy]", func() {
			nsX, nsY, nsZ, model, k8s := getK8SModel(f)
			allowedLabels := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}
			policy := GetAllowIngressByNamespace("allow-client-a-via-ns-selector", map[string]string{"pod": "a"}, allowedLabels)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			// disallow all traffic from the x or z namespaces
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{Namespace: nsX, Pod: "a"}, false)
			reachability.ExpectPeer(&Peer{Namespace: nsZ}, &Peer{Namespace: nsX, Pod: "a"}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy based on PodSelector with MatchExpressions[Feature:NetworkPolicy]", func() {
			allowedPods := metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "pod",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{"b"},
				}},
			}
			policy := GetAllowIngressByPod("x-a-allows-x-b", map[string]string{"pod": "a"}, &allowedPods)
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			reachability.Expect(NewPodString(nsX, "b"), NewPodString(nsX, "a"), true)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy based on NamespaceSelector with MatchExpressions[Feature:NetworkPolicy]", func() {
			nsX, nsY, nsZ, model, k8s := getK8SModel(f)
			allowedNamespaces := &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "ns",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{nsY},
				}},
			}
			policy := GetAllowIngressByNamespace("allow-ns-y-match-selector", map[string]string{"pod": "a"}, allowedNamespaces)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			// disallow all traffic from the x or z namespaces
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{Namespace: nsX, Pod: "a"}, false)
			reachability.ExpectPeer(&Peer{Namespace: nsZ}, &Peer{Namespace: nsX, Pod: "a"}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy based on PodSelector or NamespaceSelector [Feature:NetworkPolicy]", func() {
			nsX, _, _, model, k8s := getK8SModel(f)
			allowedNamespaces := &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "ns",
					Operator: metav1.LabelSelectorOpNotIn,
					Values:   []string{nsX},
				}},
			}
			podBAllowlisting := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"pod": "b",
				},
			}
			policy := GetAllowIngressByNamespaceOrPod("allow-ns-y-match-selector", map[string]string{"pod": "a"}, allowedNamespaces, podBAllowlisting)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.Expect(NewPodString(nsX, "a"), NewPodString(nsX, "a"), false)
			reachability.Expect(NewPodString(nsX, "c"), NewPodString(nsX, "a"), false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy based on PodSelector and NamespaceSelector [Feature:NetworkPolicy]", func() {
			nsX, nsY, nsZ, model, k8s := getK8SModel(f)
			allowedNamespaces := &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "ns",
					Operator: metav1.LabelSelectorOpNotIn,
					Values:   []string{nsX},
				}},
			}
			allowedPod := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"pod": "b",
				},
			}
			policy := GetAllowIngressByNamespaceAndPod("allow-ns-y-podselector-and-nsselector", map[string]string{"pod": "a"}, allowedNamespaces, allowedPod)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			reachability.Expect(NewPodString(nsY, "b"), NewPodString(nsX, "a"), true)
			reachability.Expect(NewPodString(nsZ, "b"), NewPodString(nsX, "a"), true)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy based on Multiple PodSelectors and NamespaceSelectors [Feature:NetworkPolicy]", func() {
			nsX, nsY, nsZ, model, k8s := getK8SModel(f)
			allowedNamespaces := &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "ns",
					Operator: metav1.LabelSelectorOpNotIn,
					Values:   []string{nsX},
				}},
			}
			allowedPod := &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "pod",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{"b", "c"},
				}},
			}
			policy := GetAllowIngressByNamespaceAndPod("allow-ns-y-z-pod-b-c", map[string]string{"pod": "a"}, allowedNamespaces, allowedPod)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{Namespace: nsX, Pod: "a"}, false)
			reachability.Expect(NewPodString(nsY, "a"), NewPodString(nsX, "a"), false)
			reachability.Expect(NewPodString(nsZ, "a"), NewPodString(nsX, "a"), false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy based on any PodSelectors [Feature:NetworkPolicy]", func() {
			nsX, _, _, model, k8s := getK8SModel(f)

			peers := []map[string]string{{"pod": "b"}, {"pod": "c"}}
			policy := GetAllowIngressByAnyPod("allow-ns-x-pod-b-c", map[string]string{"pod": "a"}, peers)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)

			// Connect Pods b and c to pod a from namespace nsX
			reachability.Expect(NewPodString(nsX, "b"), NewPodString(nsX, "a"), true)
			reachability.Expect(NewPodString(nsX, "c"), NewPodString(nsX, "a"), true)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy to allow traffic only from a pod in a different namespace based on PodSelector and NamespaceSelector [Feature:NetworkPolicy]", func() {
			nsX, nsY, _, model, k8s := getK8SModel(f)
			allowedNamespaces := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}
			allowedPods := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"pod": "a",
				},
			}
			policy := GetAllowIngressByNamespaceAndPod("allow-ns-y-pod-a-via-namespace-pod-selector", map[string]string{"pod": "a"}, allowedNamespaces, allowedPods)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			reachability.Expect(NewPodString(nsY, "a"), NewPodString(nsX, "a"), true)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy based on Ports [Feature:NetworkPolicy]", func() {
			ginkgo.By("Creating a network allowPort81Policy which only allows allow listed namespaces (y) to connect on exactly one port (81)")
			nsX, nsY, nsZ, model, k8s := getK8SModel(f)
			allowedLabels := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}
			allowPort81Policy := GetAllowIngressByNamespaceAndPort("allow-client-a-via-ns-selector", map[string]string{"pod": "a"}, allowedLabels, &intstr.IntOrString{IntVal: 81}, &protocolTCP)
			CreatePolicy(k8s, allowPort81Policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{Namespace: nsX, Pod: "a"}, false)
			reachability.ExpectPeer(&Peer{Namespace: nsY}, &Peer{Namespace: nsX, Pod: "a"}, true)
			reachability.ExpectPeer(&Peer{Namespace: nsZ}, &Peer{Namespace: nsX, Pod: "a"}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce multiple, stacked policies with overlapping podSelectors [Feature:NetworkPolicy]", func() {
			ginkgo.By("Creating a network allowPort81Policy which only allows allow listed namespaces (y) to connect on exactly one port (81)")
			nsX, nsY, nsZ, model, k8s := getK8SModel(f)
			allowedLabels := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}
			allowPort81Policy := GetAllowIngressByNamespaceAndPort("allow-client-a-via-ns-selector", map[string]string{"pod": "a"}, allowedLabels, &intstr.IntOrString{IntVal: 81}, &protocolTCP)
			CreatePolicy(k8s, allowPort81Policy, nsX)

			reachabilityALLOW := NewReachability(model.AllPods(), true)
			reachabilityALLOW.ExpectPeer(&Peer{Namespace: nsX}, &Peer{Namespace: nsX, Pod: "a"}, false)
			reachabilityALLOW.ExpectPeer(&Peer{Namespace: nsY}, &Peer{Namespace: nsX, Pod: "a"}, true)
			reachabilityALLOW.ExpectPeer(&Peer{Namespace: nsZ}, &Peer{Namespace: nsX, Pod: "a"}, false)

			ginkgo.By("Verifying traffic on port 81.")
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachabilityALLOW})

			reachabilityDENY := NewReachability(model.AllPods(), true)
			reachabilityDENY.ExpectAllIngress(NewPodString(nsX, "a"), false)

			ginkgo.By("Verifying traffic on port 80.")
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityDENY})

			allowPort80Policy := GetAllowIngressByNamespaceAndPort("allow-client-a-via-ns-selector-80", map[string]string{"pod": "a"}, allowedLabels, &intstr.IntOrString{IntVal: 80}, &protocolTCP)
			CreatePolicy(k8s, allowPort80Policy, nsX)

			ginkgo.By("Verifying that we can add a policy to unblock port 80")
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityALLOW})
		})

		ginkgo.It("should support allow-all policy [Feature:NetworkPolicy]", func() {
			ginkgo.By("Creating a network policy which allows all traffic.")
			policy := GetAllowIngress("allow-all")
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)

			ginkgo.By("Testing pods can connect to both ports when an 'allow-all' policy is present.")
			reachability := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should allow ingress access on one named port [Feature:NetworkPolicy]", func() {
			policy := GetAllowIngressByPort("allow-all", &intstr.IntOrString{Type: intstr.String, StrVal: "serve-81-tcp"})
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)

			ginkgo.By("Blocking all ports other then 81 in the entire namespace")

			reachabilityPort81 := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachabilityPort81})

			// disallow all traffic to the x namespace
			reachabilityPort80 := NewReachability(model.AllPods(), true)
			reachabilityPort80.ExpectPeer(&Peer{}, &Peer{Namespace: nsX}, false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityPort80})
		})

		ginkgo.It("should allow ingress access from namespace on one named port [Feature:NetworkPolicy]", func() {
			nsX, nsY, nsZ, model, k8s := getK8SModel(f)
			allowedLabels := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}
			policy := GetAllowIngressByNamespaceAndPort("allow-client-a-via-ns-selector-80", map[string]string{"pod": "a"}, allowedLabels, &intstr.IntOrString{Type: intstr.String, StrVal: "serve-80-tcp"}, &protocolTCP)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			// disallow all traffic from the x or z namespaces
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{Namespace: nsX, Pod: "a"}, false)
			reachability.ExpectPeer(&Peer{Namespace: nsZ}, &Peer{Namespace: nsX, Pod: "a"}, false)

			ginkgo.By("Verify that port 80 is allowed for namespace y")
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})

			ginkgo.By("Verify that port 81 is blocked for all namespaces including y")
			reachabilityFAIL := NewReachability(model.AllPods(), true)
			reachabilityFAIL.ExpectAllIngress(NewPodString(nsX, "a"), false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachabilityFAIL})
		})

		ginkgo.It("should allow egress access on one named port [Feature:NetworkPolicy]", func() {
			ginkgo.By("validating egress from port 81 to port 80")
			policy := GetAllowEgressByPort("allow-egress", &intstr.IntOrString{Type: intstr.String, StrVal: "serve-80-tcp"})
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)

			reachabilityPort80 := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityPort80})

			// meanwhile no traffic over 81 should work, since our egress policy is on 80
			reachabilityPort81 := NewReachability(model.AllPods(), true)
			reachabilityPort81.ExpectPeer(&Peer{Namespace: nsX}, &Peer{}, false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachabilityPort81})
		})

		ginkgo.It("should enforce updated policy [Feature:NetworkPolicy]", func() {
			ginkgo.By("Using the simplest possible mutation: start with allow all, then switch to deny all")
			// part 1) allow all
			policy := GetAllowIngress("allow-all-mutate-to-deny-all")
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachability})

			// part 2) update the policy to deny all
			policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{}
			UpdatePolicy(k8s, policy, nsX)

			reachabilityDeny := NewReachability(model.AllPods(), true)
			reachabilityDeny.ExpectPeer(&Peer{}, &Peer{Namespace: nsX}, false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachabilityDeny})
		})

		ginkgo.It("should allow ingress access from updated namespace [Feature:NetworkPolicy]", func() {
			nsX, nsY, _, model, k8s := getK8SModel(f)
			defer ResetNamespaceLabels(k8s, nsY)

			allowedLabels := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns2": "updated",
				},
			}
			policy := GetAllowIngressByNamespace("allow-client-a-via-ns-selector", map[string]string{"pod": "a"}, allowedLabels)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})

			// add a new label, we'll remove it after this test is completed
			updatedLabels := map[string]string{
				"ns":  nsY,
				"ns2": "updated",
			}
			UpdateNamespaceLabels(k8s, nsY, updatedLabels)

			// anything from namespace 'y' should be able to get to x/a
			reachabilityWithLabel := NewReachability(model.AllPods(), true)
			reachabilityWithLabel.ExpectAllIngress(NewPodString(nsX, "a"), false)
			reachabilityWithLabel.ExpectPeer(&Peer{Namespace: nsY}, &Peer{}, true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityWithLabel})
		})

		ginkgo.It("should allow ingress access from updated pod [Feature:NetworkPolicy]", func() {
			nsX, _, _, model, k8s := getK8SModel(f)
			podXB, err := model.FindPod(nsX, "b")
			framework.ExpectNoError(err, "find pod x/b")
			defer ResetPodLabels(k8s, podXB)

			// add a new label, we'll remove it after this test is done
			matchLabels := map[string]string{"pod": "b", "pod2": "updated"}
			allowedLabels := &metav1.LabelSelector{MatchLabels: matchLabels}
			policy := GetAllowIngressByPod("allow-client-a-via-pod-selector", map[string]string{"pod": "a"}, allowedLabels)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})

			// now update label in x namespace and pod b
			AddPodLabels(k8s, podXB, matchLabels)

			ginkgo.By("x/b is able to reach x/a when label is updated")

			reachabilityWithLabel := NewReachability(model.AllPods(), true)
			reachabilityWithLabel.ExpectAllIngress(NewPodString(nsX, "a"), false)
			reachabilityWithLabel.Expect(NewPodString(nsX, "b"), NewPodString(nsX, "a"), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityWithLabel})
		})

		ginkgo.It("should deny ingress from pods on other namespaces [Feature:NetworkPolicy]", func() {
			nsX, nsY, nsZ, model, k8s := getK8SModel(f)

			policy := GetDenyIngressEmptyPeerSelector("deny-empty-policy")
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{Namespace: nsY}, &Peer{Namespace: nsX}, false)
			reachability.ExpectPeer(&Peer{Namespace: nsZ}, &Peer{Namespace: nsX}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should deny ingress access to updated pod [Feature:NetworkPolicy]", func() {
			nsX, _, _, model, k8s := getK8SModel(f)
			podXA, err := model.FindPod(nsX, "a")
			framework.ExpectNoError(err, "find pod x/a")
			defer ResetPodLabels(k8s, podXA)

			policy := GetDenyIngressForTarget(metav1.LabelSelector{MatchLabels: map[string]string{"target": "isolated"}})
			CreatePolicy(k8s, policy, nsX)

			ginkgo.By("Verify that everything can reach x/a")
			reachability := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})

			AddPodLabels(k8s, podXA, map[string]string{"target": "isolated"})

			reachabilityIsolated := NewReachability(model.AllPods(), true)
			reachabilityIsolated.ExpectAllIngress(NewPodString(nsX, "a"), false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityIsolated})
		})

		ginkgo.It("should deny egress from pods based on PodSelector [Feature:NetworkPolicy] ", func() {
			nsX, _, _, model, k8s := getK8SModel(f)
			policy := GetDenyEgressForTarget("deny-egress-pod-a", metav1.LabelSelector{MatchLabels: map[string]string{"pod": "a"}})
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllEgress(NewPodString(nsX, "a"), false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should deny egress from all pods in a namespace [Feature:NetworkPolicy] ", func() {
			nsX, _, _, model, k8s := getK8SModel(f)
			policy := GetDenyEgress("deny-egress-ns-x")
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should work with Ingress, Egress specified together [Feature:NetworkPolicy]", func() {
			allowedPodLabels := &metav1.LabelSelector{MatchLabels: map[string]string{"pod": "b"}}
			policy := GetAllowIngressByPod("allow-client-a-via-pod-selector", map[string]string{"pod": "a"}, allowedPodLabels)
			// add an egress rule on to it...

			policy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							// dont use named ports
							Port: &intstr.IntOrString{Type: intstr.Int, IntVal: 80},
						},
						{
							Protocol: &protocolUDP,
							Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 53},
						},
					},
				},
			}
			policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeEgress, networkingv1.PolicyTypeIngress}
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)

			reachabilityPort80 := NewReachability(model.AllPods(), true)
			reachabilityPort80.ExpectAllIngress(NewPodString(nsX, "a"), false)
			reachabilityPort80.Expect(NewPodString(nsX, "b"), NewPodString(nsX, "a"), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityPort80})

			ginkgo.By("validating that port 81 doesn't work")
			// meanwhile no egress traffic on 81 should work, since our egress policy is on 80
			reachabilityPort81 := NewReachability(model.AllPods(), true)
			reachabilityPort81.ExpectAllIngress(NewPodString(nsX, "a"), false)
			reachabilityPort81.ExpectAllEgress(NewPodString(nsX, "a"), false)
			reachabilityPort81.Expect(NewPodString(nsX, "b"), NewPodString(nsX, "a"), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachabilityPort81})
		})

		ginkgo.It("should support denying of egress traffic on the client side (even if the server explicitly allows this traffic) [Feature:NetworkPolicy]", func() {
			// x/a --> y/a and y/b
			// Egress allowed to y/a only. Egress to y/b should be blocked
			// Ingress on y/a and y/b allow traffic from x/a
			// Expectation: traffic from x/a to y/a allowed only, traffic from x/a to y/b denied by egress policy

			nsX, nsY, _, model, k8s := getK8SModel(f)

			// Building egress policy for x/a to y/a only
			allowedEgressNamespaces := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}
			allowedEgressPods := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"pod": "a",
				},
			}
			egressPolicy := GetAllowEgressByNamespaceAndPod("allow-to-ns-y-pod-a", map[string]string{"pod": "a"}, allowedEgressNamespaces, allowedEgressPods)
			CreatePolicy(k8s, egressPolicy, nsX)

			// Creating ingress policy to allow from x/a to y/a and y/b
			allowedIngressNamespaces := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsX,
				},
			}
			allowedIngressPods := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"pod": "a",
				},
			}
			allowIngressPolicyPodA := GetAllowIngressByNamespaceAndPod("allow-from-xa-on-ya-match-selector", map[string]string{"pod": "a"}, allowedIngressNamespaces, allowedIngressPods)
			allowIngressPolicyPodB := GetAllowIngressByNamespaceAndPod("allow-from-xa-on-yb-match-selector", map[string]string{"pod": "b"}, allowedIngressNamespaces, allowedIngressPods)

			CreatePolicy(k8s, allowIngressPolicyPodA, nsY)
			CreatePolicy(k8s, allowIngressPolicyPodB, nsY)

			// While applying the policies, traffic needs to be allowed by both egress and ingress rules.
			// Egress rules only
			// 	xa	xb	xc	ya	yb	yc	za	zb	zc
			// xa	X	X	X	.	*X*	X	X	X	X
			// xb	.	.	.	.	.	.	.	.	.
			// xc	.	.	.	.	.	.	.	.	.
			// ya	.	.	.	.	.	.	.	.	.
			// yb	.	.	.	.	.	.	.	.	.
			// yc	.	.	.	.	.	.	.	.	.
			// za	.	.	.	.	.	.	.	.	.
			// zb	.	.	.	.	.	.	.	.	.
			// zc	.	.	.	.	.	.	.	.	.
			// Ingress rules only
			// 	xa	xb	xc	ya	yb	yc	za	zb	zc
			// xa	.	.	.	*.*	.	.	.	.	.
			// xb	.	.	X	X	.	.	.	.	.
			// xc	.	.	X	X	.	.	.	.	.
			// ya	.	.	X	X	.	.	.	.	.
			// yb	.	.	X	X	.	.	.	.	.
			// yc	.	.	X	X	.	.	.	.	.
			// za	.	.	X	X	.	.	.	.	.
			// zb	.	.	X	X	.	.	.	.	.
			// zc	.	.	X	X	.	.	.	.	.
			// In the resulting truth table, connections from x/a should only be allowed to y/a. x/a to y/b should be blocked by the egress on x/a.
			// Expected results
			// 	xa	xb	xc	ya	yb	yc	za	zb	zc
			// xa	X	X	X	.	*X*	X	X	X	X
			// xb	.	.	.	X	X	.	.	.	.
			// xc	.	.	.	X	X	.	.	.	.
			// ya	.	.	.	X	X	.	.	.	.
			// yb	.	.	.	X	X	.	.	.	.
			// yc	.	.	.	X	X	.	.	.	.
			// za	.	.	.	X	X	.	.	.	.
			// zb	.	.	.	X	X	.	.	.	.
			// zc	.	.	.	X	X	.	.	.	.

			reachability := NewReachability(model.AllPods(), true)
			// Default all traffic flows.
			// Exception: x/a can only egress to y/a, others are false
			// Exception: y/a can only allow ingress from x/a, others are false
			// Exception: y/b has no allowed traffic (due to limit on x/a egress)

			reachability.ExpectPeer(&Peer{Namespace: nsX, Pod: "a"}, &Peer{}, false)
			reachability.ExpectPeer(&Peer{}, &Peer{Namespace: nsY, Pod: "a"}, false)
			reachability.ExpectPeer(&Peer{Namespace: nsX, Pod: "a"}, &Peer{Namespace: nsY, Pod: "a"}, true)
			reachability.ExpectPeer(&Peer{}, &Peer{Namespace: nsY, Pod: "b"}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce egress policy allowing traffic to a server in a different namespace based on PodSelector and NamespaceSelector [Feature:NetworkPolicy]", func() {
			nsX, nsY, _, model, k8s := getK8SModel(f)
			allowedNamespaces := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}
			allowedPods := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"pod": "a",
				},
			}
			policy := GetAllowEgressByNamespaceAndPod("allow-to-ns-y-pod-a", map[string]string{"pod": "a"}, allowedNamespaces, allowedPods)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllEgress(NewPodString(nsX, "a"), false)
			reachability.Expect(NewPodString(nsX, "a"), NewPodString(nsY, "a"), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce ingress policy allowing any port traffic to a server on a specific protocol [Feature:NetworkPolicy] [Feature:UDP]", func() {
			nsX, _, _, model, k8s := getK8SModel(f)

			policy := GetAllowIngressByProtocol("allow-ingress-by-proto", map[string]string{"pod": "a"}, &protocolTCP)
			CreatePolicy(k8s, policy, nsX)

			reachabilityTCP := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityTCP})

			reachabilityUDP := NewReachability(model.AllPods(), true)
			reachabilityUDP.ExpectPeer(&Peer{}, &Peer{Namespace: nsX, Pod: "a"}, false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolUDP, Reachability: reachabilityUDP})
		})

		ginkgo.It("should enforce multiple ingress policies with ingress allow-all policy taking precedence [Feature:NetworkPolicy]", func() {
			nsX, _, _, model, k8s := getK8SModel(f)
			policyAllowOnlyPort80 := GetAllowIngressByPort("allow-ingress-port-80", &intstr.IntOrString{Type: intstr.Int, IntVal: 80})
			CreatePolicy(k8s, policyAllowOnlyPort80, nsX)

			ginkgo.By("The policy targets port 80 -- so let's make sure traffic on port 81 is blocked")

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{}, &Peer{Namespace: nsX}, false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachability})

			ginkgo.By("Allowing all ports")

			policyAllowAll := GetAllowIngress("allow-ingress")
			CreatePolicy(k8s, policyAllowAll, nsX)

			reachabilityAll := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachabilityAll})
		})

		ginkgo.It("should enforce multiple egress policies with egress allow-all policy taking precedence [Feature:NetworkPolicy]", func() {
			policyAllowPort80 := GetAllowEgressByPort("allow-egress-port-80", &intstr.IntOrString{Type: intstr.Int, IntVal: 80})
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policyAllowPort80, nsX)

			ginkgo.By("Making sure ingress doesn't work other than port 80")

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{}, false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachability})

			ginkgo.By("Allowing all ports")

			policyAllowAll := GetAllowEgress()
			CreatePolicy(k8s, policyAllowAll, nsX)

			reachabilityAll := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachabilityAll})
		})

		ginkgo.It("should stop enforcing policies after they are deleted [Feature:NetworkPolicy]", func() {
			ginkgo.By("Creating a network policy for the server which denies all traffic.")

			// Deny all traffic into and out of "x".
			policy := GetDenyAll("deny-all")
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)
			reachability := NewReachability(model.AllPods(), true)

			// Expect all traffic into, and out of "x" to be False.
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{}, false)
			reachability.ExpectPeer(&Peer{}, &Peer{Namespace: nsX}, false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})

			err := k8s.cleanNetworkPolicies(model.NamespaceNames)
			time.Sleep(3 * time.Second) // TODO we can remove this eventually, its just a hack to keep CI stable.
			framework.ExpectNoError(err, "unable to clean network policies")

			// Now the policy is deleted, we expect all connectivity to work again.
			reachabilityAll := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityAll})
		})

		// TODO, figure out how the next 3 tests should work with dual stack : do we need a different abstraction then just "podIP"?

		ginkgo.It("should allow egress access to server in CIDR block [Feature:NetworkPolicy]", func() {
			// Getting podServer's status to get podServer's IP, to create the CIDR
			nsX, nsY, _, model, k8s := getK8SModel(f)
			podList, err := f.ClientSet.CoreV1().Pods(nsY).List(context.TODO(), metav1.ListOptions{LabelSelector: "pod=b"})
			framework.ExpectNoError(err, "Failing to list pods in namespace y")
			pod := podList.Items[0]

			hostMask := 32
			if utilnet.IsIPv6String(pod.Status.PodIP) {
				hostMask = 128
			}
			podServerCIDR := fmt.Sprintf("%s/%d", pod.Status.PodIP, hostMask)
			policyAllowCIDR := GetAllowEgressByCIDR("a", podServerCIDR)
			CreatePolicy(k8s, policyAllowCIDR, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllEgress(NewPodString(nsX, "a"), false)
			reachability.Expect(NewPodString(nsX, "a"), NewPodString(nsY, "b"), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should enforce except clause while egress access to server in CIDR block [Feature:NetworkPolicy]", func() {
			// Getting podServer's status to get podServer's IP, to create the CIDR with except clause
			nsX, _, _, model, k8s := getK8SModel(f)
			podList, err := f.ClientSet.CoreV1().Pods(nsX).List(context.TODO(), metav1.ListOptions{LabelSelector: "pod=a"})
			framework.ExpectNoError(err, "Failing to find pod x/a")
			podA := podList.Items[0]

			podServerAllowCIDR := fmt.Sprintf("%s/4", podA.Status.PodIP)

			podList, err = f.ClientSet.CoreV1().Pods(nsX).List(context.TODO(), metav1.ListOptions{LabelSelector: "pod=b"})
			framework.ExpectNoError(err, "Failing to find pod x/b")
			podB := podList.Items[0]

			hostMask := 32
			if utilnet.IsIPv6String(podB.Status.PodIP) {
				hostMask = 128
			}
			podServerExceptList := []string{fmt.Sprintf("%s/%d", podB.Status.PodIP, hostMask)}

			policyAllowCIDR := GetAllowEgressByCIDRExcept("a", podServerAllowCIDR, podServerExceptList)

			CreatePolicy(k8s, policyAllowCIDR, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.Expect(NewPodString(nsX, "a"), NewPodString(nsX, "b"), false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should ensure an IP overlapping both IPBlock.CIDR and IPBlock.Except is allowed [Feature:NetworkPolicy]", func() {
			// Getting podServer's status to get podServer's IP, to create the CIDR with except clause
			nsX, _, _, model, k8s := getK8SModel(f)
			podList, err := f.ClientSet.CoreV1().Pods(nsX).List(context.TODO(), metav1.ListOptions{LabelSelector: "pod=a"})
			framework.ExpectNoError(err, "Failing to find pod x/a")
			podA := podList.Items[0]

			podList, err = f.ClientSet.CoreV1().Pods(nsX).List(context.TODO(), metav1.ListOptions{LabelSelector: "pod=b"})
			framework.ExpectNoError(err, "Failing to find pod x/b")
			podB := podList.Items[0]

			// Exclude podServer's IP with an Except clause
			hostMask := 32
			if utilnet.IsIPv6String(podB.Status.PodIP) {
				hostMask = 128
			}

			podServerAllowCIDR := fmt.Sprintf("%s/4", podA.Status.PodIP)
			podServerExceptList := []string{fmt.Sprintf("%s/%d", podB.Status.PodIP, hostMask)}
			policyAllowCIDR := GetAllowEgressByCIDRExcept("a", podServerAllowCIDR, podServerExceptList)
			CreatePolicy(k8s, policyAllowCIDR, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.Expect(NewPodString(nsX, "a"), NewPodString(nsX, "b"), false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachability})

			podBIP := fmt.Sprintf("%s/%d", podB.Status.PodIP, hostMask)
			//// Create NetworkPolicy which allows access to the podServer using podServer's IP in allow CIDR.
			allowPolicy := GetAllowEgressByCIDR("a", podBIP)
			// SHOULD THIS BE UPDATE OR CREATE JAY TESTING 10/31
			UpdatePolicy(k8s, allowPolicy, nsX)

			reachabilityAllow := NewReachability(model.AllPods(), true)
			reachabilityAllow.ExpectAllEgress(NewPodString(nsX, "a"), false)
			reachabilityAllow.Expect(NewPodString(nsX, "a"), NewPodString(nsX, "b"), true)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: reachabilityAllow})
		})

		ginkgo.It("should enforce policies to check ingress and egress policies can be controlled independently based on PodSelector [Feature:NetworkPolicy]", func() {
			/*
					Test steps:
					1. Verify every pod in every namespace can talk to each other
				       - including a -> b and b -> a
					2. Create a policy to allow egress a -> b (target = a)
				    3. Create a policy to *deny* ingress b -> a (target = a)
					4. Verify a -> b allowed; b -> a blocked
			*/
			targetLabels := map[string]string{"pod": "a"}

			ginkgo.By("Creating a network policy for pod-a which allows Egress traffic to pod-b.")

			allowEgressPolicy := GetAllowEgressForTarget(metav1.LabelSelector{MatchLabels: targetLabels})
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, allowEgressPolicy, nsX)

			allowEgressReachability := NewReachability(model.AllPods(), true)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: allowEgressReachability})

			ginkgo.By("Creating a network policy for pod-a that denies traffic from pod-b.")

			denyAllIngressPolicy := GetDenyIngressForTarget(metav1.LabelSelector{MatchLabels: targetLabels})
			CreatePolicy(k8s, denyAllIngressPolicy, nsX)

			denyIngressToXReachability := NewReachability(model.AllPods(), true)
			denyIngressToXReachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolTCP, Reachability: denyIngressToXReachability})
		})

		ginkgo.It("should not allow access by TCP when a policy specifies only SCTP [Feature:NetworkPolicy] [Feature:SCTP]", func() {
			policy := GetAllowIngressOnProtocolByPort("allow-only-sctp-ingress-on-port-81", v1.ProtocolSCTP, map[string]string{"pod": "a"}, &intstr.IntOrString{IntVal: 81})
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)

			ginkgo.By("Creating a network policy for the server which allows traffic only via SCTP on port 81.")

			// Probing with TCP, so all traffic should be dropped.
			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})

		ginkgo.It("should not allow access by TCP when a policy specifies only UDP [Feature:NetworkPolicy] [Feature:UDP]", func() {
			policy := GetAllowIngressOnProtocolByPort("allow-only-udp-ingress-on-port-81", v1.ProtocolUDP, map[string]string{"pod": "a"}, &intstr.IntOrString{IntVal: 81})
			nsX, _, _, model, k8s := getK8SModel(f)
			CreatePolicy(k8s, policy, nsX)

			ginkgo.By("Creating a network policy for the server which allows traffic only via UDP on port 81.")

			// Probing with TCP, so all traffic should be dropped.
			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolTCP, Reachability: reachability})
		})
	})
})

var _ = common.SIGDescribe("Netpol [Feature:UDPConnectivity][LinuxOnly]", func() {
	f := framework.NewDefaultFramework("udp-network-policy")

	ginkgo.BeforeEach(func() {
		// Windows does not support UDP testing via agnhost.
		e2eskipper.SkipIfNodeOSDistroIs("windows")
	})

	ginkgo.Context("NetworkPolicy between server and client using UDP", func() {
		ginkgo.BeforeEach(func() {
			initializeResourcesByFixedNS(f)
		})

		ginkgo.AfterEach(func() {
			if !useFixedNamespaces {
				_, _, _, model, k8s := getK8SModel(f)
				framework.ExpectNoError(k8s.deleteNamespaces(model.NamespaceNames), "unable to clean up UDP netpol namespaces")
			}
		})

		ginkgo.It("should support a 'default-deny-ingress' policy [Feature:NetworkPolicy]", func() {
			nsX, _, _, model, k8s := getK8SModel(f)
			policy := GetDenyIngress("deny-all")
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{}, &Peer{Namespace: nsX}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolUDP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy based on Ports [Feature:NetworkPolicy]", func() {
			ginkgo.By("Creating a network policy allowPort81Policy which only allows allow listed namespaces (y) to connect on exactly one port (81)")
			nsX, nsY, nsZ, model, k8s := getK8SModel(f)
			allowedLabels := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}

			allowPort81Policy := GetAllowIngressByNamespaceAndPort("allow-ingress-on-port-81-ns-x", map[string]string{"pod": "a"}, allowedLabels, &intstr.IntOrString{IntVal: 81}, &protocolUDP)
			CreatePolicy(k8s, allowPort81Policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{Namespace: nsX, Pod: "a"}, false)
			reachability.ExpectPeer(&Peer{Namespace: nsZ}, &Peer{Namespace: nsX, Pod: "a"}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolUDP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy to allow traffic only from a pod in a different namespace based on PodSelector and NamespaceSelector [Feature:NetworkPolicy]", func() {
			nsX, nsY, _, model, k8s := getK8SModel(f)
			allowedNamespaces := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}
			allowedPods := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"pod": "a",
				},
			}
			policy := GetAllowIngressByNamespaceAndPod("allow-ns-y-pod-a-via-namespace-pod-selector", map[string]string{"pod": "a"}, allowedNamespaces, allowedPods)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			reachability.Expect(NewPodString(nsY, "a"), NewPodString(nsX, "a"), true)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolUDP, Reachability: reachability})
		})
	})
})

var _ = common.SIGDescribe("Netpol [Feature:SCTPConnectivity][LinuxOnly][Disruptive]", func() {
	f := framework.NewDefaultFramework("sctp-network-policy")

	ginkgo.BeforeEach(func() {
		// Windows does not support network policies.
		e2eskipper.SkipIfNodeOSDistroIs("windows")
	})

	ginkgo.Context("NetworkPolicy between server and client using SCTP", func() {
		ginkgo.BeforeEach(func() {
			addSCTPContainers = true
			initializeResourcesByFixedNS(f)
		})

		ginkgo.AfterEach(func() {
			if !useFixedNamespaces {
				_, _, _, model, k8s := getK8SModel(f)
				framework.ExpectNoError(k8s.deleteNamespaces(model.NamespaceNames), "unable to clean up SCTP netpol namespaces")
			}
		})

		ginkgo.It("should support a 'default-deny-ingress' policy [Feature:NetworkPolicy]", func() {
			nsX, _, _, model, k8s := getK8SModel(f)
			policy := GetDenyIngress("deny-all")
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{}, &Peer{Namespace: nsX}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolSCTP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy based on Ports [Feature:NetworkPolicy]", func() {
			ginkgo.By("Creating a network allowPort81Policy which only allows allow listed namespaces (y) to connect on exactly one port (81)")
			nsX, nsY, nsZ, model, k8s := getK8SModel(f)
			allowedLabels := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}

			allowPort81Policy := GetAllowIngressByNamespaceAndPort("allow-ingress-on-port-81-ns-x", map[string]string{"pod": "a"}, allowedLabels, &intstr.IntOrString{IntVal: 81}, &protocolSCTP)
			CreatePolicy(k8s, allowPort81Policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectPeer(&Peer{Namespace: nsX}, &Peer{Namespace: nsX, Pod: "a"}, false)
			reachability.ExpectPeer(&Peer{Namespace: nsZ}, &Peer{Namespace: nsX, Pod: "a"}, false)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 81, Protocol: v1.ProtocolSCTP, Reachability: reachability})
		})

		ginkgo.It("should enforce policy to allow traffic only from a pod in a different namespace based on PodSelector and NamespaceSelector [Feature:NetworkPolicy]", func() {
			nsX, nsY, _, model, k8s := getK8SModel(f)
			allowedNamespaces := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ns": nsY,
				},
			}
			allowedPods := &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"pod": "a",
				},
			}
			policy := GetAllowIngressByNamespaceAndPod("allow-ns-y-pod-a-via-namespace-pod-selector", map[string]string{"pod": "a"}, allowedNamespaces, allowedPods)
			CreatePolicy(k8s, policy, nsX)

			reachability := NewReachability(model.AllPods(), true)
			reachability.ExpectAllIngress(NewPodString(nsX, "a"), false)
			reachability.Expect(NewPodString(nsY, "a"), NewPodString(nsX, "a"), true)

			ValidateOrFail(k8s, model, &TestCase{ToPort: 80, Protocol: v1.ProtocolSCTP, Reachability: reachability})
		})
	})
})

// getNamespaces returns the canonical set of namespaces used by this test, taking a root ns as input.  This allows this test to run in parallel.
func getNamespaces(rootNs string) (string, string, string, []string) {
	if useFixedNamespaces {
		rootNs = ""
	} else {
		rootNs = rootNs + "-"
	}
	nsX := fmt.Sprintf("%sx", rootNs)
	nsY := fmt.Sprintf("%sy", rootNs)
	nsZ := fmt.Sprintf("%sz", rootNs)
	return nsX, nsY, nsZ, []string{nsX, nsY, nsZ}
}

// defaultModel creates a new "model" pod system under namespaces (x,y,z) which has pods a, b, and c.  Thus resulting in the
// truth table matrix that is identical for all tests, comprising 81 total connections between 9 pods (x/a, x/b, x/c, ..., z/c).
func defaultModel(namespaces []string, dnsDomain string) *Model {
	protocols := []v1.Protocol{v1.ProtocolTCP, v1.ProtocolUDP}
	if addSCTPContainers {
		protocols = append(protocols, v1.ProtocolSCTP)
	}
	return NewModel(namespaces, []string{"a", "b", "c"}, []int32{80, 81}, protocols, dnsDomain)
}

// getK8sModel generates a network policy model using the framework's root namespace and cluster DNS domain.
// This function is deterministic and has no side effects, so may be safely called multiple times.
func getK8SModel(f *framework.Framework) (string, string, string, *Model, *kubeManager) {
	k8s := newKubeManager(f)
	rootNs := f.Namespace.GetName()
	nsX, nsY, nsZ, namespaces := getNamespaces(rootNs)

	model := defaultModel(namespaces, framework.TestContext.ClusterDNSDomain)

	return nsX, nsY, nsZ, model, k8s
}

// initializeResourcesByFixedNS uses the e2e framework to create all necessary namespace resources, cleaning up
// network policies from the namespace if useFixedNamespace is set true, avoiding policies overlap of new tests.
func initializeResourcesByFixedNS(f *framework.Framework) {
	if useFixedNamespaces {
		_ = initializeResources(f)
		_, _, _, model, k8s := getK8SModel(f)
		framework.ExpectNoError(k8s.cleanNetworkPolicies(model.NamespaceNames), "unable to clean network policies")
		err := wait.Poll(waitInterval, waitTimeout, func() (done bool, err error) {
			for _, ns := range model.NamespaceNames {
				netpols, err := k8s.clientSet.NetworkingV1().NetworkPolicies(ns).List(context.TODO(), metav1.ListOptions{})
				framework.ExpectNoError(err, "get network policies from ns %s", ns)
				if len(netpols.Items) > 0 {
					return false, nil
				}
			}
			return true, nil
		})
		framework.ExpectNoError(err, "unable to wait for network policy deletion")
	} else {
		framework.Logf("Using %v as the default dns domain for this cluster... ", framework.TestContext.ClusterDNSDomain)
		framework.ExpectNoError(initializeResources(f), "unable to initialize resources")
	}
}

// initializeResources uses the e2e framework to create all necessary namespace resources, based on the network policy
// model derived from the framework.  It then waits for the resources described by the model to be up and running
// (i.e. all pods are ready and running in their namespaces).
func initializeResources(f *framework.Framework) error {
	_, _, _, model, k8s := getK8SModel(f)

	framework.Logf("initializing cluster: ensuring namespaces, deployments, and pods exist and are ready")

	err := k8s.initializeCluster(model)
	if err != nil {
		return err
	}

	framework.Logf("finished initializing cluster state")

	return k8s.waitForHTTPServers(model)
}
