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

// Package tail follows logs of every container a selector picks and merges them into one timeline.
package tail

import (
	"bufio"
	"container/heap"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/joshchen-dev/riffle/pkg/filter"
)

// Options configures Run.
type Options struct {
	Namespace string
	Selector  string // label selector; empty means all pods
	Container string // empty means all containers
	Since     time.Duration
	Filter    *filter.Filter
	Delay     time.Duration // reorder window; lines are held this long before printing
	// RedactFor returns extra redaction for a pod, by its labels. May be nil, and may return nil.
	RedactFor func(podLabels map[string]string) *filter.Filter
	// Sink receives lines in order instead of out. Optional.
	Sink func(Line)
}

// Line is one log line.
type Line struct {
	Time      time.Time
	Pod       string
	Container string
	Msg       string // redacted
	Raw       string // as read; for matching, never printed
	arrived   time.Time
	redact    *filter.Filter
}

// Run tails until ctx is done. Stream errors go to errOut and don't stop the run.
func Run(ctx context.Context, cs kubernetes.Interface, o Options, out, errOut io.Writer) error {
	lines := make(chan Line, 1024)
	watchErr := make(chan error, 1)
	go func() { watchErr <- watchPods(ctx, cs, o, lines, errOut) }()

	m := &merger{delay: o.Delay}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	print := func(ls []Line) {
		for _, l := range ls {
			if o.Sink != nil {
				o.Sink(l)
				continue
			}
			fmt.Fprintf(out, "%s/%s %s\n", l.Pod, l.Container, l.Msg)
		}
	}
	for {
		select {
		case l := <-lines:
			if o.Filter.Match(l.Msg) {
				l.Raw = l.Msg
				l.Msg = o.Filter.Redact(l.Msg)
				if l.redact != nil {
					l.Msg = l.redact.Redact(l.Msg)
				}
				l.arrived = time.Now()
				m.push(l)
			}
		case now := <-tick.C:
			print(m.pop(now))
		case err := <-watchErr:
			print(m.pop(time.Now().Add(m.delay)))
			return err
		case <-ctx.Done():
			print(m.pop(time.Now().Add(m.delay)))
			return nil
		}
	}
}

// watchPods starts one stream per running container instance and re-watches when the server closes the watch.
func watchPods(ctx context.Context, cs kubernetes.Interface, o Options, lines chan<- Line, errOut io.Writer) error {
	started := map[string]bool{} // container ID → stream started
	for {
		w, err := cs.CoreV1().Pods(o.Namespace).Watch(ctx, metav1.ListOptions{LabelSelector: o.Selector})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("watch pods: %w", err)
		}
		for ev := range w.ResultChan() {
			pod, ok := ev.Object.(*corev1.Pod)
			if !ok || ev.Type == watch.Deleted {
				continue
			}
			for _, cst := range pod.Status.ContainerStatuses {
				if cst.State.Running == nil || cst.ContainerID == "" || started[cst.ContainerID] {
					continue
				}
				if o.Container != "" && cst.Name != o.Container {
					continue
				}
				started[cst.ContainerID] = true
				var redact *filter.Filter
				if o.RedactFor != nil {
					redact = o.RedactFor(pod.Labels)
				}
				go stream(ctx, cs, o, pod.Name, cst.Name, redact, lines, errOut)
			}
		}
		w.Stop()
		if ctx.Err() != nil {
			return nil
		}
	}
}

// stream follows one container. A restart gets a new container ID and a new stream.
// ponytail: a stream cut by a network error is not resumed; resume with SinceTime if it matters.
func stream(ctx context.Context, cs kubernetes.Interface, o Options, pod, container string, redact *filter.Filter,
	lines chan<- Line, errOut io.Writer) {
	opts := &corev1.PodLogOptions{Container: container, Follow: true, Timestamps: true}
	if o.Since > 0 {
		s := int64(o.Since.Seconds())
		opts.SinceSeconds = &s
	}
	rc, err := cs.CoreV1().Pods(o.Namespace).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(errOut, "riffle: %s/%s: %v\n", pod, container, err)
		}
		return
	}
	defer func() { _ = rc.Close() }()
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		t, msg := parseLine(sc.Text())
		select {
		case lines <- Line{Time: t, Pod: pod, Container: container, Msg: msg, redact: redact}:
		case <-ctx.Done():
			return
		}
	}
}

// parseLine splits "<RFC3339Nano> <msg>" as returned with timestamps=true.
// Lines without a timestamp get the current time.
func parseLine(s string) (time.Time, string) {
	ts, msg, ok := strings.Cut(s, " ")
	if ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return t, msg
		}
	}
	return time.Now(), s
}

// merger holds lines for delay after arrival and releases them in timestamp order.
// ponytail: ordering is only exact within the delay window; backlog from --since that arrives
// further apart than delay can interleave out of order.
type merger struct {
	delay time.Duration
	h     lineHeap
}

func (m *merger) push(l Line) { heap.Push(&m.h, l) }

// pop returns lines whose earliest-timestamp head arrived at least delay before now.
func (m *merger) pop(now time.Time) []Line {
	var out []Line
	for m.h.Len() > 0 && !m.h[0].arrived.After(now.Add(-m.delay)) {
		out = append(out, heap.Pop(&m.h).(Line))
	}
	return out
}

type lineHeap []Line

func (h lineHeap) Len() int           { return len(h) }
func (h lineHeap) Less(i, j int) bool { return h[i].Time.Before(h[j].Time) }
func (h lineHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *lineHeap) Push(x any)        { *h = append(*h, x.(Line)) }
func (h *lineHeap) Pop() any {
	old := *h
	l := old[len(old)-1]
	*h = old[:len(old)-1]
	return l
}
