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

// Command riffle tails and filters pod logs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
	"github.com/joshchen-dev/riffle/internal/tail"
	"github.com/joshchen-dev/riffle/pkg/filter"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: riffle <command>

commands:
  tail        follow logs of selected pods
  alerts      list LogAlerts with Ready and Firing
  alert new   print or create a LogAlert
  version     print version
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "tail":
		return runTail(args[1:], os.Stdout, os.Stderr)
	case "alerts":
		return runAlerts(args[1:], os.Stdout, os.Stderr)
	case "alert":
		if len(args) > 1 && args[1] == "new" {
			return runAlertNew(args[2:], os.Stdout, os.Stderr)
		}
		fmt.Fprint(os.Stderr, "usage: riffle alert new <name> ...\n")
		return 2
	case "version":
		fmt.Println(version)
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "riffle: unknown command %q\n%s", args[0], usage)
		return 2
	}
}

func runTail(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		fmt.Fprintln(errOut, "usage: riffle tail [-n ns] [-l selector] [-c container] [--match re] [--where expr]")
		fmt.Fprintln(errOut, "       riffle tail [-n ns] --rule name")
		fmt.Fprintln(errOut, "       riffle tail -i ...   interactive")
		fs.PrintDefaults()
	}
	var (
		k         = addKubeFlags(fs)
		ns        = fs.String("n", "", "namespace (default: from kubeconfig)")
		selector  = fs.String("l", "", "label selector")
		container = fs.String("c", "", "container (default: all)")
		match     = fs.String("match", "", "RE2 regex on the raw line")
		where     = fs.String("where", "", "JSON field filter, e.g. 'level=error AND latency_ms>500'")
		since     = fs.Duration("since", time.Minute, "start this far back")
		rule      = fs.String("rule", "", "use the selector, container, match, where and redact of this LogAlert")
		tuiMode   = fs.Bool("i", false, "interactive: edit the filter live, ctrl+s saves it as LogAlert YAML")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errOut, "riffle tail: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *rule != "" && (*selector != "" || *container != "" || *match != "" || *where != "") {
		fmt.Fprintln(errOut, "riffle tail: --rule can't be combined with -l, -c, --match or --where")
		return 2
	}
	f, err := filter.Compile(*match, *where, nil)
	if err != nil {
		fmt.Fprintf(errOut, "riffle tail: %v\n", err)
		return 2
	}

	cfg, err := k.load(ns)
	if err != nil {
		fmt.Fprintf(errOut, "riffle tail: %v\n", err)
		return 1
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		fmt.Fprintf(errOut, "riffle tail: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	o := tail.Options{
		Namespace: *ns,
		Selector:  *selector,
		Container: *container,
		Since:     *since,
		Filter:    f,
		Delay:     250 * time.Millisecond,
	}
	alerts, err := loadAlerts(ctx, cfg, *ns)
	switch {
	case err != nil && *rule != "":
		fmt.Fprintf(errOut, "riffle tail: %v\n", err)
		return 1
	case err != nil:
		fmt.Fprintf(errOut, "riffle tail: LogAlert redaction off: %v\n", err)
	}
	var ruleRedact []string
	if *rule != "" {
		spec, err := applyRule(&o, alerts, *rule)
		if err != nil {
			fmt.Fprintf(errOut, "riffle tail: %v\n", err)
			return 1
		}
		*selector, *container, *match, *where, ruleRedact = o.Selector, o.Container, spec.Match, spec.Where, spec.Redact
	}
	o.RedactFor = redactFor(alerts)
	if *tuiMode {
		return runTUI(ctx, cs, o, newTUI(*ns, *selector, *container, *match, *where), ruleRedact, out, errOut)
	}
	if err := tail.Run(ctx, cs, o, out, errOut); err != nil {
		fmt.Fprintf(errOut, "riffle tail: %v\n", err)
		return 1
	}
	return 0
}

type kubeFlags struct{ kubeconfig, context *string }

func addKubeFlags(fs *flag.FlagSet) kubeFlags {
	return kubeFlags{
		kubeconfig: fs.String("kubeconfig", "", "kubeconfig path"),
		context:    fs.String("context", "", "kubeconfig context"),
	}
}

// load returns the REST config and fills an empty *ns from kubeconfig.
func (k kubeFlags) load(ns *string) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = *k.kubeconfig
	overrides := &clientcmd.ConfigOverrides{CurrentContext: *k.context}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	if *ns == "" {
		var err error
		if *ns, _, err = cc.Namespace(); err != nil {
			return nil, err
		}
	}
	return cc.ClientConfig()
}

// riffleClient is a client that knows the LogAlert types.
func riffleClient(cfg *rest.Config) (client.Client, error) {
	scheme := runtime.NewScheme()
	if err := rifflev1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	return client.New(cfg, client.Options{Scheme: scheme})
}

// loadAlerts lists LogAlerts in ns, or nil if Riffle isn't installed.
func loadAlerts(ctx context.Context, cfg *rest.Config, ns string) ([]rifflev1alpha1.LogAlert, error) {
	c, err := riffleClient(cfg)
	if err != nil {
		return nil, err
	}
	return listAlerts(ctx, c, ns)
}
