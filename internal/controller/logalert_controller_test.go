/*
Copyright 2026.

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

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
)

var _ = Describe("LogAlert controller", func() {
	var (
		ns string
		r  *LogAlertReconciler
	)

	BeforeEach(func() {
		n := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "la-"}}
		Expect(k8sClient.Create(ctx, n)).To(Succeed())
		ns = n.Name
		r = &LogAlertReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	})

	alert := func(name string, mutate func(*rifflev1alpha1.LogAlertSpec)) *rifflev1alpha1.LogAlert {
		la := &rifflev1alpha1.LogAlert{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: rifflev1alpha1.LogAlertSpec{
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
				Match:    "timeout",
				Where:    "level=error AND latency_ms>500",
				Redact:   []string{`card=\d+`},
				Threshold: rifflev1alpha1.Threshold{
					Count:  20,
					Window: metav1.Duration{Duration: 5 * time.Minute},
				},
			},
		}
		if mutate != nil {
			mutate(&la.Spec)
		}
		return la
	}

	pod := func(name, app string, phase corev1.PodPhase) {
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{"app": app}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox"}}},
		}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
		p.Status.Phase = phase
		Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
	}

	reconcileReady := func(la *rifflev1alpha1.LogAlert) (*rifflev1alpha1.LogAlert, *metav1.Condition) {
		Expect(k8sClient.Create(ctx, la)).To(Succeed())
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(la)})
		Expect(err).NotTo(HaveOccurred())
		got := &rifflev1alpha1.LogAlert{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(la), got)).To(Succeed())
		c := meta.FindStatusCondition(got.Status.Conditions, rifflev1alpha1.ConditionReady)
		Expect(c).NotTo(BeNil())
		return got, c
	}

	It("marks a valid rule Ready and counts running selected pods", func() {
		pod("api-1", "api", corev1.PodRunning)
		pod("api-2", "api", corev1.PodRunning)
		pod("api-3", "api", corev1.PodPending)
		pod("web-1", "web", corev1.PodRunning)

		got, c := reconcileReady(alert("ok", nil))
		Expect(c.Status).To(Equal(metav1.ConditionTrue))
		Expect(c.Reason).To(Equal(rifflev1alpha1.ReasonValid))
		Expect(got.Status.MatchedPods).To(BeEquivalentTo(2))
		Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
	})

	DescribeTable("reports InvalidSpec",
		func(mutate func(*rifflev1alpha1.LogAlertSpec), msg string) {
			_, c := reconcileReady(alert("bad", mutate))
			Expect(c.Status).To(Equal(metav1.ConditionFalse))
			Expect(c.Reason).To(Equal(rifflev1alpha1.ReasonInvalidSpec))
			Expect(c.Message).To(ContainSubstring(msg))
		},
		Entry("bad match", func(s *rifflev1alpha1.LogAlertSpec) { s.Match = "(" }, "match:"),
		Entry("bad where", func(s *rifflev1alpha1.LogAlertSpec) { s.Where = "latency_ms>fast" }, "where:"),
		Entry("bad redact", func(s *rifflev1alpha1.LogAlertSpec) { s.Redact = []string{"["} }, "redact[0]:"),
	)

	It("reports Suspended", func() {
		_, c := reconcileReady(alert("off", func(s *rifflev1alpha1.LogAlertSpec) { s.Suspend = true }))
		Expect(c.Status).To(Equal(metav1.ConditionFalse))
		Expect(c.Reason).To(Equal(rifflev1alpha1.ReasonSuspended))
	})

	It("ignores a deleted LogAlert", func() {
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: ns, Name: "gone"}})
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("CRD rejects",
		func(mutate func(*rifflev1alpha1.LogAlertSpec), msg string) {
			err := k8sClient.Create(ctx, alert("rejected", mutate))
			Expect(err).To(MatchError(ContainSubstring(msg)))
		},
		Entry("no match or where", func(s *rifflev1alpha1.LogAlertSpec) { s.Match, s.Where = "", "" }, "match or where is required"),
		Entry("window under 1m", func(s *rifflev1alpha1.LogAlertSpec) { s.Threshold.Window.Duration = 30 * time.Second }, "window must be between 1m and 1h"),
		Entry("window over 1h", func(s *rifflev1alpha1.LogAlertSpec) { s.Threshold.Window.Duration = 2 * time.Hour }, "window must be between 1m and 1h"),
		Entry("zero count", func(s *rifflev1alpha1.LogAlertSpec) { s.Threshold.Count = 0 }, "spec.threshold.count"),
	)

	It("maps a pod to the LogAlerts that select it", func() {
		Expect(k8sClient.Create(ctx, alert("api", nil))).To(Succeed())
		Expect(k8sClient.Create(ctx, alert("web", func(s *rifflev1alpha1.LogAlertSpec) {
			s.Selector.MatchLabels = map[string]string{"app": "web"}
		}))).To(Succeed())
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: ns, Labels: map[string]string{"app": "api"}}}

		reqs := r.alertsForPod(ctx, p)
		Expect(reqs).To(ConsistOf(reconcile.Request{NamespacedName: client.ObjectKey{Namespace: ns, Name: "api"}}))
	})
})
