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
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
	"github.com/joshchen-dev/riffle/internal/tail"
)

func la(name, app string, redact ...string) rifflev1alpha1.LogAlert {
	return rifflev1alpha1.LogAlert{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "pay"},
		Spec: rifflev1alpha1.LogAlertSpec{
			Selector:  metav1.LabelSelector{MatchLabels: map[string]string{"app": app}},
			Container: "c",
			Where:     "level=error",
			Redact:    redact,
		},
	}
}

func TestListAlerts(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := rifflev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	a, b := la("a", "api"), la("b", "api")
	b.Namespace = "other"
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&a, &b).Build()
	got, err := listAlerts(t.Context(), c, "pay")
	if err != nil || len(got) != 1 || got[0].Name != "a" {
		t.Errorf("got %v, %v; want [a]", got, err)
	}

	listErr := func(err error) client.Client {
		return fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error { return err },
		}).Build()
	}
	if got, err := listAlerts(t.Context(), listErr(&meta.NoKindMatchError{}), "pay"); got != nil || err != nil {
		t.Errorf("CRD missing: got %v, %v; want nil, nil", got, err)
	}
	if _, err := listAlerts(t.Context(), listErr(errors.New("forbidden")), "pay"); err == nil {
		t.Error("forbidden: want error")
	}
}

func TestApplyRule(t *testing.T) {
	alerts := []rifflev1alpha1.LogAlert{la("other", "web"), la("errors", "api", `card=\d+`)}
	o := tail.Options{Namespace: "pay"}
	if _, err := applyRule(&o, alerts, "errors"); err != nil {
		t.Fatal(err)
	}
	if o.Selector != "app=api" || o.Container != "c" {
		t.Errorf("selector %q container %q, want app=api c", o.Selector, o.Container)
	}
	if !o.Filter.Match(`{"level":"error"}`) || o.Filter.Match(`{"level":"info"}`) {
		t.Error("filter doesn't apply the rule's where")
	}
	if got := o.Filter.Redact("card=42"); got != "[REDACTED]" {
		t.Errorf("Redact = %q, want [REDACTED]", got)
	}

	if _, err := applyRule(&o, alerts, "missing"); err == nil {
		t.Error("missing rule: want error")
	}
	bad := la("bad", "api")
	bad.Spec.Where = "x>fast"
	if _, err := applyRule(&o, []rifflev1alpha1.LogAlert{bad}, "bad"); err == nil {
		t.Error("invalid rule: want error")
	}
}

func TestRedactFor(t *testing.T) {
	invalid := la("invalid", "api", "[")
	redact := redactFor([]rifflev1alpha1.LogAlert{
		la("cards", "api", `card=\d+`),
		la("tokens", "api", `token=\w+`),
		la("web", "web", `ip=\S+`),
		la("none", "api"),
		invalid,
	})
	f := redact(map[string]string{"app": "api"})
	if got := f.Redact("card=1 token=abc ip=1.2.3.4"); got != "[REDACTED] [REDACTED] ip=1.2.3.4" {
		t.Errorf("api pod: %q", got)
	}
	if f := redact(map[string]string{"app": "db"}); f != nil {
		t.Error("unselected pod: want nil")
	}
}
