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

package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
)

const ts = "2026-11-24T01:02:03.000000001Z"

func pod(name string, restarts int32, started time.Time) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "pay", UID: "uid-1", Labels: map[string]string{"app": "api"}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "api", RestartCount: restarts, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(started)}}},
			{Name: "proxy", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(started)}}},
		}},
	}
}

func alert(name string, gen int64, mutate func(*rifflev1alpha1.LogAlertSpec)) rifflev1alpha1.LogAlert {
	la := rifflev1alpha1.LogAlert{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "pay", UID: types.UID("la-" + name), Generation: gen},
		Spec: rifflev1alpha1.LogAlertSpec{
			Selector:  metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Container: "api",
			Where:     "level=error",
		},
	}
	if mutate != nil {
		mutate(&la.Spec)
	}
	return la
}

func write(t *testing.T, path string, msgs ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, m := range msgs {
		if _, err := f.WriteString(ts + " stdout F " + m + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func eventually(t *testing.T, want float64, got func() float64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for got() != want {
		if time.Now().After(deadline) {
			t.Fatalf("got %v, want %v", got(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSync(t *testing.T) {
	dir := t.TempDir()
	a := New(t.Context(), dir, prometheus.NewRegistry())
	a.Poll = 5 * time.Millisecond
	matches := func(rule string) func() float64 {
		return func() float64 { return testutil.ToFloat64(a.Metrics.Matches.WithLabelValues("pay", rule, "api")) }
	}
	file := func(restarts string) string { return filepath.Join(dir, "pay_p1_uid-1", "api", restarts+".log") }

	// Container started before the agent: existing lines are skipped.
	write(t, file("0"), `{"level":"error"}`)
	p := pod("p1", 0, a.Started.Add(-time.Minute))
	a.Sync([]corev1.Pod{p}, []rifflev1alpha1.LogAlert{alert("errors", 1, nil)})
	if len(a.followers) != 1 {
		t.Fatalf("followers = %d, want 1 (api only, not proxy)", len(a.followers))
	}
	time.Sleep(20 * time.Millisecond)
	write(t, file("0"), `{"level":"error"}`, `{"level":"info"}`, `not json`)
	eventually(t, 1, matches("errors"))
	eventually(t, 3, func() float64 { return testutil.ToFloat64(a.Metrics.Lines) })

	// A second rule on the same file is picked up without restarting the follower.
	a.Sync([]corev1.Pod{p}, []rifflev1alpha1.LogAlert{
		alert("errors", 1, nil),
		alert("any", 1, func(s *rifflev1alpha1.LogAlertSpec) { s.Where, s.Match = "", "." }),
	})
	write(t, file("0"), `{"level":"error"}`)
	eventually(t, 2, matches("errors"))
	eventually(t, 1, matches("any"))

	// Restart: new file, read from its start because the container started after the agent.
	write(t, file("1"), `{"level":"error"}`)
	p = pod("p1", 1, a.Started.Add(time.Minute))
	a.Sync([]corev1.Pod{p}, []rifflev1alpha1.LogAlert{alert("errors", 1, nil)})
	eventually(t, 3, matches("errors"))
	if _, ok := a.followers[file("0")]; ok {
		t.Error("follower for 0.log still running after restart")
	}

	// Suspended, invalid, or no alerts: no followers.
	for _, alerts := range [][]rifflev1alpha1.LogAlert{
		{alert("errors", 2, func(s *rifflev1alpha1.LogAlertSpec) { s.Suspend = true })},
		{alert("errors", 3, func(s *rifflev1alpha1.LogAlertSpec) { s.Where = "latency>fast" })},
		nil,
	} {
		a.Sync([]corev1.Pod{p}, alerts)
		if len(a.followers) != 0 {
			t.Errorf("alerts %v: followers = %d, want 0", alerts, len(a.followers))
		}
	}
	if len(a.compiled) != 0 {
		t.Errorf("compiled cache = %d, want 0 after alerts are gone", len(a.compiled))
	}
}

func TestOtherNamespaceNotSelected(t *testing.T) {
	a := New(t.Context(), t.TempDir(), prometheus.NewRegistry())
	la := alert("errors", 1, nil)
	la.Namespace = "other"
	a.Sync([]corev1.Pod{pod("p1", 0, time.Now())}, []rifflev1alpha1.LogAlert{la})
	if len(a.followers) != 0 {
		t.Errorf("followers = %d, want 0", len(a.followers))
	}
}
