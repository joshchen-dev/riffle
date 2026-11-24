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

// Package agent runs on every node, follows the log files of pods that LogAlerts select
// and counts matches as Prometheus metrics.
package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
	"github.com/joshchen-dev/riffle/internal/crilog"
	"github.com/joshchen-dev/riffle/pkg/filter"
)

// Metrics are registered by New.
type Metrics struct {
	Matches *prometheus.CounterVec
	Lines   prometheus.Counter
	Bytes   prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Matches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "riffle_matches_total", Help: "Log lines matched by a LogAlert.",
		}, []string{"namespace", "rule", "container"}),
		Lines: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "riffle_lines_total", Help: "Log lines read from followed containers.",
		}),
		Bytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "riffle_bytes_total", Help: "Log bytes read from followed containers.",
		}),
	}
	reg.MustRegister(m.Matches, m.Lines, m.Bytes)
	return m
}

// Agent follows one file per selected container instance.
type Agent struct {
	LogDir  string
	Poll    time.Duration
	Started time.Time // containers started after this are read from the start of their file
	Metrics *Metrics

	ctx       context.Context
	followers map[string]*follower // by file path
	compiled  map[types.UID]compiledRule
}

type rule struct {
	namespace, name, container string
	sel                        labels.Selector
	filter                     *filter.Filter
}

type compiledRule struct {
	generation int64
	rule       *rule // nil if the spec doesn't compile
}

type follower struct {
	cancel    context.CancelFunc
	container string
	rules     atomic.Pointer[[]*rule]
}

// New returns an Agent whose followers stop when ctx is done.
func New(ctx context.Context, logDir string, reg prometheus.Registerer) *Agent {
	return &Agent{
		LogDir:    logDir,
		Poll:      250 * time.Millisecond,
		Started:   time.Now(),
		Metrics:   newMetrics(reg),
		ctx:       ctx,
		followers: map[string]*follower{},
		compiled:  map[types.UID]compiledRule{},
	}
}

// Sync starts, updates and stops followers to match pods (on this node) and alerts.
func (a *Agent) Sync(pods []corev1.Pod, alerts []rifflev1alpha1.LogAlert) {
	byNS := a.rulesByNamespace(alerts)
	want := map[string]bool{}
	for i := range pods {
		p := &pods[i]
		for _, cst := range p.Status.ContainerStatuses {
			if cst.State.Running == nil {
				continue
			}
			rules := matching(byNS[p.Namespace], p.Labels, cst.Name)
			if len(rules) == 0 {
				continue
			}
			path := filepath.Join(a.LogDir, fmt.Sprintf("%s_%s_%s", p.Namespace, p.Name, p.UID),
				cst.Name, fmt.Sprintf("%d.log", cst.RestartCount))
			want[path] = true
			fl, ok := a.followers[path]
			if !ok {
				fl = a.start(path, cst.Name, cst.State.Running.StartedAt.After(a.Started))
			}
			fl.rules.Store(&rules)
		}
	}
	for path, fl := range a.followers {
		if !want[path] {
			// ponytail: lines written in the last poll interval before a container stops may be missed.
			fl.cancel()
			delete(a.followers, path)
		}
	}
}

func (a *Agent) start(path, container string, fromStart bool) *follower {
	ctx, cancel := context.WithCancel(a.ctx)
	fl := &follower{cancel: cancel, container: container}
	a.followers[path] = fl
	go func() {
		_ = crilog.Follow(ctx, path, fromStart, a.Poll, func(e crilog.Entry) {
			a.Metrics.Lines.Inc()
			a.Metrics.Bytes.Add(float64(len(e.Msg)))
			for _, r := range *fl.rules.Load() {
				if r.filter.Match(e.Msg) {
					a.Metrics.Matches.WithLabelValues(r.namespace, r.name, container).Inc()
				}
			}
		})
	}()
	return fl
}

// rulesByNamespace compiles alerts, reusing the previous result while the generation is unchanged.
func (a *Agent) rulesByNamespace(alerts []rifflev1alpha1.LogAlert) map[string][]*rule {
	seen := map[types.UID]bool{}
	out := map[string][]*rule{}
	for i := range alerts {
		la := &alerts[i]
		seen[la.UID] = true
		c, ok := a.compiled[la.UID]
		if !ok || c.generation != la.Generation {
			c = compiledRule{generation: la.Generation, rule: compile(la)}
			a.compiled[la.UID] = c
		}
		if c.rule != nil && !la.Spec.Suspend {
			out[la.Namespace] = append(out[la.Namespace], c.rule)
		}
	}
	for uid := range a.compiled {
		if !seen[uid] {
			delete(a.compiled, uid)
		}
	}
	return out
}

func compile(la *rifflev1alpha1.LogAlert) *rule {
	sel, err := metav1.LabelSelectorAsSelector(&la.Spec.Selector)
	if err != nil {
		return nil
	}
	f, err := filter.Compile(la.Spec.Match, la.Spec.Where, la.Spec.Redact)
	if err != nil {
		return nil // the operator reports InvalidSpec
	}
	return &rule{namespace: la.Namespace, name: la.Name, container: la.Spec.Container, filter: f, sel: sel}
}

func matching(rules []*rule, podLabels map[string]string, container string) []*rule {
	var out []*rule
	for _, r := range rules {
		if r.sel.Matches(labels.Set(podLabels)) && (r.container == "" || r.container == container) {
			out = append(out, r)
		}
	}
	return out
}
