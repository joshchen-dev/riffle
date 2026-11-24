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
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
	"github.com/joshchen-dev/riffle/pkg/filter"
)

var errNoCRD = errors.New("LogAlert CRD not found; is the Riffle operator installed?")

func runAlerts(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("alerts", flag.ContinueOnError)
	fs.SetOutput(errOut)
	k := addKubeFlags(fs)
	ns := fs.String("n", "", "namespace (default: from kubeconfig)")
	all := fs.Bool("A", false, "all namespaces")
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	cfg, err := k.load(ns)
	if err == nil && *all {
		*ns = ""
	}
	var c client.Client
	if err == nil {
		c, err = riffleClient(cfg)
	}
	var list rifflev1alpha1.LogAlertList
	if err == nil {
		err = c.List(context.Background(), &list, client.InNamespace(*ns))
		if meta.IsNoMatchError(err) {
			err = errNoCRD
		}
	}
	if err != nil {
		fmt.Fprintf(errOut, "riffle alerts: %v\n", err)
		return 1
	}
	printAlerts(out, list.Items, *all, time.Now())
	return 0
}

func printAlerts(out io.Writer, alerts []rifflev1alpha1.LogAlert, allNS bool, now time.Time) {
	tw := tabwriter.NewWriter(out, 0, 4, 3, ' ', 0)
	defer func() { _ = tw.Flush() }()
	if allNS {
		fmt.Fprint(tw, "NAMESPACE\t")
	}
	fmt.Fprintln(tw, "NAME\tREADY\tFIRING\tPODS\tTHRESHOLD\tLAST MATCH")
	for _, la := range alerts {
		if allNS {
			fmt.Fprintf(tw, "%s\t", la.Namespace)
		}
		last := "-"
		if t := la.Status.LastMatchTime; t != nil {
			last = duration.HumanDuration(now.Sub(t.Time)) + " ago"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d/%s\t%s\n", la.Name,
			condition(la, rifflev1alpha1.ConditionReady), condition(la, rifflev1alpha1.ConditionFiring),
			la.Status.MatchedPods, la.Spec.Threshold.Count, la.Spec.Threshold.Window.Duration, last)
	}
}

// condition returns the status of a condition, or its reason when that says more.
func condition(la rifflev1alpha1.LogAlert, typ string) string {
	c := meta.FindStatusCondition(la.Status.Conditions, typ)
	switch {
	case c == nil:
		return "-"
	case c.Status == metav1.ConditionFalse &&
		(c.Reason == rifflev1alpha1.ReasonInvalidSpec || c.Reason == rifflev1alpha1.ReasonSuspended):
		return c.Reason
	}
	return string(c.Status)
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func runAlertNew(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("alert new", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		fmt.Fprintln(errOut, "usage: riffle alert new <name> -l selector [--match re] [--where expr]")
		fmt.Fprintln(errOut, "                        [--count n] [--window d] [--redact re]... [--create]")
		fs.PrintDefaults()
	}
	k := addKubeFlags(fs)
	var (
		ns        = fs.String("n", "", "namespace (default: from kubeconfig with --create, else none)")
		selector  = fs.String("l", "", "label selector (required)")
		container = fs.String("c", "", "container (default: all)")
		match     = fs.String("match", "", "RE2 regex on the raw line")
		where     = fs.String("where", "", "JSON field filter")
		count     = fs.Int("count", 1, "matches that fire the alert")
		window    = fs.Duration("window", 5*time.Minute, "sliding window, 1m to 1h")
		create    = fs.Bool("create", false, "create it in the cluster instead of printing YAML")
		redact    stringList
	)
	fs.Var(&redact, "redact", "RE2 regex to redact; repeatable")
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if name == "" || fs.NArg() > 0 {
		fs.Usage()
		return 2
	}
	la, err := newAlert(name, *ns, *selector, *container, *match, *where, int32(*count), *window, redact)
	if err != nil {
		fmt.Fprintf(errOut, "riffle alert new: %v\n", err)
		return 2
	}

	if !*create {
		b, err := yaml.Marshal(la)
		if err != nil {
			fmt.Fprintf(errOut, "riffle alert new: %v\n", err)
			return 1
		}
		_, _ = out.Write(b)
		return 0
	}
	cfg, err := k.load(ns)
	var c client.Client
	if err == nil {
		c, err = riffleClient(cfg)
	}
	if err == nil {
		la.Namespace = *ns
		err = c.Create(context.Background(), la)
		if meta.IsNoMatchError(err) {
			err = errNoCRD
		}
	}
	if err != nil {
		fmt.Fprintf(errOut, "riffle alert new: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "logalert %s/%s created\n", la.Namespace, la.Name)
	return 0
}

// newAlert builds a LogAlert and checks what the API server would reject, so printed YAML applies cleanly.
func newAlert(name, ns, selector, container, match, where string, count int32, window time.Duration,
	redact []string) (*rifflev1alpha1.LogAlert, error) {
	if selector == "" {
		return nil, errors.New("-l is required")
	}
	if match == "" && where == "" {
		return nil, errors.New("--match or --where is required")
	}
	if count < 1 {
		return nil, errors.New("--count must be at least 1")
	}
	if window < time.Minute || window > time.Hour {
		return nil, errors.New("--window must be between 1m and 1h")
	}
	if _, err := filter.Compile(match, where, redact); err != nil {
		return nil, err
	}
	sel, err := metav1.ParseToLabelSelector(selector)
	if err != nil {
		return nil, err
	}
	return &rifflev1alpha1.LogAlert{
		TypeMeta:   metav1.TypeMeta{APIVersion: rifflev1alpha1.GroupVersion.String(), Kind: "LogAlert"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: rifflev1alpha1.LogAlertSpec{
			Selector:  *sel,
			Container: container,
			Match:     match,
			Where:     where,
			Threshold: rifflev1alpha1.Threshold{Count: count, Window: metav1.Duration{Duration: window}},
			Redact:    redact,
		},
	}, nil
}

func parseExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}
