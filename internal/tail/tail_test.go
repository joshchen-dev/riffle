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

package tail

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/joshchen-dev/riffle/pkg/filter"
)

func TestParseLine(t *testing.T) {
	ts, msg := parseLine("2026-11-24T01:02:03.123456789Z level=error boom")
	if msg != "level=error boom" || ts.Nanosecond() != 123456789 {
		t.Errorf("got %v %q", ts, msg)
	}
	before := time.Now()
	ts, msg = parseLine("no timestamp here")
	if msg != "no timestamp here" || ts.Before(before) {
		t.Errorf("got %v %q", ts, msg)
	}
}

func TestMergerOrdersWithinDelay(t *testing.T) {
	base := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	arrive := base.Add(time.Hour)
	m := &merger{delay: 250 * time.Millisecond}
	// Pod b's earlier line arrives 100ms after pod a's later line.
	m.push(Line{Time: base.Add(2 * time.Second), Msg: "a2", arrived: arrive})
	m.push(Line{Time: base.Add(1 * time.Second), Msg: "b1", arrived: arrive.Add(100 * time.Millisecond)})

	if got := m.pop(arrive.Add(200 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("released early: %v", got)
	}
	got := m.pop(arrive.Add(400 * time.Millisecond))
	if len(got) != 2 || got[0].Msg != "b1" || got[1].Msg != "a2" {
		t.Fatalf("got %v, want b1 then a2", got)
	}
}

func runningPod(name, containerID string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Labels: map[string]string{"app": "api"}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "app", ContainerID: containerID, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			{Name: "sidecar", ContainerID: containerID + "-s", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
		}},
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestRunStreamsMatchingContainers(t *testing.T) {
	cs := fake.NewClientset()
	fw := watch.NewFake()
	cs.PrependWatchReactor("pods", k8stesting.DefaultWatchReactor(fw, nil))

	f, err := filter.Compile("fake", "", []string{"logs"}) // fake GetLogs body is "fake logs"
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut syncBuf
	done := make(chan error)
	go func() {
		o := Options{Namespace: "ns", Container: "app", Filter: f, Delay: 10 * time.Millisecond}
		o.RedactFor = func(l map[string]string) *filter.Filter {
			if l["app"] != "api" {
				return nil
			}
			r, _ := filter.Compile("", "", []string{"^fake"})
			return r
		}
		done <- Run(ctx, cs, o, &out, &errOut)
	}()

	fw.Add(runningPod("p1", "id-1"))
	fw.Modify(runningPod("p1", "id-1")) // same container instance: no second stream
	fw.Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: "ns"}})

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(out.String(), "p1/app") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if got, want := out.String(), "p1/app [REDACTED] [REDACTED]\n"; got != want {
		t.Errorf("out = %q, want %q (stderr %q)", got, want, errOut.String())
	}
}
