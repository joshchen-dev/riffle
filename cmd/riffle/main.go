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

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/joshchen-dev/riffle/internal/tail"
	"github.com/joshchen-dev/riffle/pkg/filter"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: riffle <command>

commands:
  tail      follow logs of selected pods
  version   print version
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
		fs.PrintDefaults()
	}
	var (
		kubeconfig = fs.String("kubeconfig", "", "kubeconfig path")
		kubectx    = fs.String("context", "", "kubeconfig context")
		ns         = fs.String("n", "", "namespace (default: from kubeconfig)")
		selector   = fs.String("l", "", "label selector")
		container  = fs.String("c", "", "container (default: all)")
		match      = fs.String("match", "", "RE2 regex on the raw line")
		where      = fs.String("where", "", "JSON field filter, e.g. 'level=error AND latency_ms>500'")
		since      = fs.Duration("since", time.Minute, "start this far back")
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
	f, err := filter.Compile(*match, *where, nil)
	if err != nil {
		fmt.Fprintf(errOut, "riffle tail: %v\n", err)
		return 2
	}

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = *kubeconfig
	overrides := &clientcmd.ConfigOverrides{CurrentContext: *kubectx}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	if *ns == "" {
		if *ns, _, err = cc.Namespace(); err != nil {
			fmt.Fprintf(errOut, "riffle tail: %v\n", err)
			return 1
		}
	}
	cfg, err := cc.ClientConfig()
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
	if err := tail.Run(ctx, cs, o, out, errOut); err != nil {
		fmt.Fprintf(errOut, "riffle tail: %v\n", err)
		return 1
	}
	return 0
}
