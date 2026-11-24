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
	"bytes"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
)

func TestPrintAlerts(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	cond := func(typ string, s metav1.ConditionStatus, reason string) metav1.Condition {
		return metav1.Condition{Type: typ, Status: s, Reason: reason}
	}
	hot := la("hot", "api")
	hot.Spec.Threshold = rifflev1alpha1.Threshold{Count: 20, Window: metav1.Duration{Duration: 5 * time.Minute}}
	hot.Status = rifflev1alpha1.LogAlertStatus{
		MatchedPods:   3,
		LastMatchTime: &metav1.Time{Time: now.Add(-90 * time.Second)},
		Conditions: []metav1.Condition{
			cond(rifflev1alpha1.ConditionReady, metav1.ConditionTrue, rifflev1alpha1.ReasonValid),
			cond(rifflev1alpha1.ConditionFiring, metav1.ConditionTrue, rifflev1alpha1.ReasonThresholdExceeded),
		},
	}
	off := la("off", "api")
	off.Namespace = "web"
	off.Spec.Threshold = rifflev1alpha1.Threshold{Count: 1, Window: metav1.Duration{Duration: time.Hour}}
	off.Status.Conditions = []metav1.Condition{
		cond(rifflev1alpha1.ConditionReady, metav1.ConditionFalse, rifflev1alpha1.ReasonSuspended),
	}

	var b bytes.Buffer
	printAlerts(&b, []rifflev1alpha1.LogAlert{hot, off}, true, now)
	want := `NAMESPACE   NAME   READY       FIRING   PODS   THRESHOLD   LAST MATCH
pay         hot    True        True     3      20/5m0s     90s ago
web         off    Suspended   -        0      1/1h0m0s    -
`
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestNewAlert(t *testing.T) {
	for _, tc := range []struct {
		name              string
		sel, match, where string
		count             int32
		window            time.Duration
		redact            []string
		wantErr           string
	}{
		{"no selector", "", "x", "", 1, time.Minute, nil, "-l is required"},
		{"no filter", "app=api", "", "", 1, time.Minute, nil, "--match or --where"},
		{"zero count", "app=api", "x", "", 0, time.Minute, nil, "--count"},
		{"short window", "app=api", "x", "", 1, time.Second, nil, "--window"},
		{"long window", "app=api", "x", "", 1, 2 * time.Hour, nil, "--window"},
		{"bad where", "app=api", "", "a>fast", 1, time.Minute, nil, "where:"},
		{"bad redact", "app=api", "x", "", 1, time.Minute, []string{"["}, "redact[0]"},
		{"bad selector", "app in (", "x", "", 1, time.Minute, nil, "parse"},
		{"ok", "app=api,tier notin (db)", "x", "level=error", 5, time.Hour, []string{`card=\d+`}, ""},
	} {
		got, err := newAlert("a", "pay", tc.sel, "", tc.match, tc.where, tc.count, tc.window, tc.redact)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err %v, want %q", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		sel := got.Spec.Selector
		if got.Kind != "LogAlert" || sel.MatchLabels["app"] != "api" || len(sel.MatchExpressions) != 1 {
			t.Errorf("%s: got %+v", tc.name, got)
		}
	}
}

func TestRunAlertNewPrintsYAML(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runAlertNew([]string{"errors", "-l", "app=api", "--where", "level=error", "--redact", "a", "--redact", "b"},
		&out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var got rifflev1alpha1.LogAlert
	if err := yaml.UnmarshalStrict(out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got.Name != "errors" || got.Namespace != "" || got.Spec.Where != "level=error" || len(got.Spec.Redact) != 2 ||
		got.Spec.Threshold.Count != 1 || got.Spec.Threshold.Window.Duration != 5*time.Minute {
		t.Errorf("got %+v", got)
	}

	if code := runAlertNew([]string{"-l", "app=api"}, &out, &errOut); code != 2 {
		t.Errorf("no name: exit %d, want 2", code)
	}
}
