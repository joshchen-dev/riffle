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

// Command agent runs on every node and counts LogAlert matches in pod log files.
package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
	"github.com/joshchen-dev/riffle/internal/agent"
)

func main() {
	logDir := flag.String("log-dir", "/var/log/pods", "pod log directory")
	addr := flag.String("metrics-bind-address", ":8080", "metrics and healthz address")
	resync := flag.Duration("resync", 2*time.Second, "how often to reconcile followers")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("agent")

	node := os.Getenv("NODE_NAME")
	if node == "" {
		log.Error(nil, "NODE_NAME is not set")
		os.Exit(1)
	}
	if err := run(*logDir, *addr, *resync, node); err != nil {
		log.Error(err, "Agent failed")
		os.Exit(1)
	}
}

func run(logDir, addr string, resync time.Duration, node string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := ctrl.Log.WithName("agent")

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := rifflev1alpha1.AddToScheme(scheme); err != nil {
		return err
	}
	c, err := cache.New(ctrl.GetConfigOrDie(), cache.Options{
		Scheme: scheme,
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Pod{}: {Field: fields.OneTermEqualSelector("spec.nodeName", node)},
		},
	})
	if err != nil {
		return err
	}
	go func() {
		if err := c.Start(ctx); err != nil {
			log.Error(err, "Cache stopped")
			stop()
		}
	}()

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	a := agent.New(ctx, logDir, reg)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(err, "Metrics server stopped")
			stop()
		}
	}()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	log.Info("Starting", "node", node, "logDir", logDir)
	tick := time.NewTicker(resync)
	defer tick.Stop()
	for {
		var pods corev1.PodList
		var alerts rifflev1alpha1.LogAlertList
		if err := c.List(ctx, &pods); err != nil {
			log.Error(err, "Failed to list pods")
		} else if err := c.List(ctx, &alerts); err != nil {
			log.Error(err, "Failed to list LogAlerts")
		} else {
			a.Sync(pods.Items, alerts.Items)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
