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

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
	"github.com/joshchen-dev/riffle/internal/aggregator"
	"github.com/joshchen-dev/riffle/pkg/filter"
)

// LogAlertReconciler validates LogAlerts and reports their status.
type LogAlertReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Counts   *aggregator.Store
	Recorder events.EventRecorder
	Now      func() time.Time
}

// +kubebuilder:rbac:groups=riffle.jhchen.dev,resources=logalerts,verbs=get;list;watch
// +kubebuilder:rbac:groups=riffle.jhchen.dev,resources=logalerts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile sets Ready, matchedPods and Firing.
func (r *LogAlertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var la rifflev1alpha1.LogAlert
	if err := r.Get(ctx, req.NamespacedName, &la); err != nil {
		if apierrors.IsNotFound(err) {
			r.Counts.Delete(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	status := la.Status.DeepCopy()
	status.ObservedGeneration = la.Generation
	ready := metav1.Condition{
		Type:               rifflev1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             rifflev1alpha1.ReasonValid,
		ObservedGeneration: la.Generation,
	}

	sel, err := metav1.LabelSelectorAsSelector(&la.Spec.Selector)
	if err == nil {
		_, err = filter.Compile(la.Spec.Match, la.Spec.Where, la.Spec.Redact)
	}
	switch {
	case err != nil:
		ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, rifflev1alpha1.ReasonInvalidSpec, err.Error()
		status.MatchedPods = 0
	default:
		var pods corev1.PodList
		if err := r.List(ctx, &pods, client.InNamespace(la.Namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
			return ctrl.Result{}, err
		}
		status.MatchedPods = 0
		for _, p := range pods.Items {
			if p.Status.Phase == corev1.PodRunning {
				status.MatchedPods++
			}
		}
		if la.Spec.Suspend {
			ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, rifflev1alpha1.ReasonSuspended, "spec.suspend is true"
		}
	}
	meta.SetStatusCondition(&status.Conditions, ready)

	firing := metav1.Condition{
		Type:               rifflev1alpha1.ConditionFiring,
		Status:             metav1.ConditionFalse,
		Reason:             rifflev1alpha1.ReasonNotEvaluated,
		Message:            "rule is not Ready",
		ObservedGeneration: la.Generation,
	}
	now := r.Now()
	var result ctrl.Result
	var sum int64
	var sample string
	if ready.Status == metav1.ConditionTrue {
		th := la.Spec.Threshold
		var last time.Time
		var active bool
		sum, last, sample, active = r.Counts.Window(req.NamespacedName, th.Window.Duration, now)
		if !last.IsZero() && (status.LastMatchTime == nil || last.After(status.LastMatchTime.Time)) {
			status.LastMatchTime = &metav1.Time{Time: last}
		}
		firing.Reason, firing.Message = rifflev1alpha1.ReasonBelowThreshold, ""
		if sum >= int64(th.Count) {
			firing.Status, firing.Reason = metav1.ConditionTrue, rifflev1alpha1.ReasonThresholdExceeded
			firing.Message = fmt.Sprintf("%d or more matches in %s", th.Count, th.Window.Duration)
		}
		if active {
			// Re-evaluate as the window slides, even when no new counts arrive.
			result.RequeueAfter = aggregator.BucketSize
		}
	}
	fired := firing.Status == metav1.ConditionTrue &&
		!meta.IsStatusConditionTrue(la.Status.Conditions, rifflev1alpha1.ConditionFiring)
	if fired {
		status.LastFiredTime = &metav1.Time{Time: now}
	}
	meta.SetStatusCondition(&status.Conditions, firing)

	if equality.Semantic.DeepEqual(&la.Status, status) {
		return result, nil
	}
	la.Status = *status
	if err := r.Status().Update(ctx, &la); err != nil {
		return ctrl.Result{}, err
	}
	if fired {
		r.Recorder.Eventf(&la, nil, corev1.EventTypeWarning, rifflev1alpha1.ReasonThresholdExceeded, "Evaluate",
			"%d matches in %s, threshold %d. Last: %s", sum, la.Spec.Threshold.Window.Duration, la.Spec.Threshold.Count, sample)
	}
	logf.FromContext(ctx).V(1).Info("Updated status", "ready", ready.Reason, "firing", firing.Reason, "matchedPods", status.MatchedPods)
	return result, nil
}

// alertsForPod maps a pod to the LogAlerts in its namespace that select it.
func (r *LogAlertReconciler) alertsForPod(ctx context.Context, obj client.Object) []reconcile.Request {
	var list rifflev1alpha1.LogAlertList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list LogAlerts", "namespace", obj.GetNamespace())
		return nil
	}
	var reqs []reconcile.Request
	for _, la := range list.Items {
		sel, err := metav1.LabelSelectorAsSelector(&la.Spec.Selector)
		if err != nil {
			continue
		}
		if sel.Matches(labels.Set(obj.GetLabels())) {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: la.Namespace, Name: la.Name}})
		}
	}
	return reqs
}

// SetupWithManager sets up the controller with the Manager.
func (r *LogAlertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rifflev1alpha1.LogAlert{}).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.alertsForPod)).
		WatchesRawSource(source.Channel(r.Counts.Updated, handler.TypedEnqueueRequestsFromMapFunc(
			func(_ context.Context, key types.NamespacedName) []reconcile.Request {
				return []reconcile.Request{{NamespacedName: key}}
			}))).
		Named("logalert").
		Complete(r)
}
