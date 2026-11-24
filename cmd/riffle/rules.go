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

package main

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
	"github.com/joshchen-dev/riffle/internal/tail"
	"github.com/joshchen-dev/riffle/pkg/filter"
)

// listAlerts returns the LogAlerts in ns, or nil if Riffle isn't installed.
func listAlerts(ctx context.Context, c client.Reader, ns string) ([]rifflev1alpha1.LogAlert, error) {
	var l rifflev1alpha1.LogAlertList
	if err := c.List(ctx, &l, client.InNamespace(ns)); err != nil {
		if meta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, err
	}
	return l.Items, nil
}

// applyRule sets o's selector, container and filter from the LogAlert called name.
func applyRule(o *tail.Options, alerts []rifflev1alpha1.LogAlert, name string) error {
	for i := range alerts {
		la := &alerts[i]
		if la.Name != name {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(&la.Spec.Selector)
		if err != nil {
			return fmt.Errorf("rule %s: %w", name, err)
		}
		f, err := filter.Compile(la.Spec.Match, la.Spec.Where, la.Spec.Redact)
		if err != nil {
			return fmt.Errorf("rule %s: %w", name, err)
		}
		o.Selector, o.Container, o.Filter = sel.String(), la.Spec.Container, f
		return nil
	}
	return fmt.Errorf("no LogAlert %q in namespace %s", name, o.Namespace)
}

// redactFor returns the redact patterns of every alert that selects a pod.
// ponytail: alerts are read once at start; rules added while tailing don't apply.
func redactFor(alerts []rifflev1alpha1.LogAlert) func(map[string]string) *filter.Filter {
	type rule struct {
		sel    labels.Selector
		redact []string
	}
	var rules []rule
	for _, la := range alerts {
		sel, err := metav1.LabelSelectorAsSelector(&la.Spec.Selector)
		if err != nil || len(la.Spec.Redact) == 0 {
			continue
		}
		if _, err := filter.Compile("", "", la.Spec.Redact); err != nil {
			continue // InvalidSpec; the operator reports it
		}
		rules = append(rules, rule{sel, la.Spec.Redact})
	}
	return func(podLabels map[string]string) *filter.Filter {
		var redact []string
		for _, r := range rules {
			if r.sel.Matches(labels.Set(podLabels)) {
				redact = append(redact, r.redact...)
			}
		}
		if len(redact) == 0 {
			return nil
		}
		f, _ := filter.Compile("", "", redact) // each pattern compiled above
		return f
	}
}
