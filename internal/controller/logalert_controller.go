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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
	"github.com/joshchen-dev/riffle/pkg/filter"
)

// LogAlertReconciler validates LogAlerts and reports their status.
type LogAlertReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=riffle.jhchen.dev,resources=logalerts,verbs=get;list;watch
// +kubebuilder:rbac:groups=riffle.jhchen.dev,resources=logalerts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile sets Ready and matchedPods. Firing is set by the aggregator (M4).
func (r *LogAlertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var la rifflev1alpha1.LogAlert
	if err := r.Get(ctx, req.NamespacedName, &la); err != nil {
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

	if equality.Semantic.DeepEqual(&la.Status, status) {
		return ctrl.Result{}, nil
	}
	la.Status = *status
	if err := r.Status().Update(ctx, &la); err != nil {
		return ctrl.Result{}, err
	}
	logf.FromContext(ctx).V(1).Info("Updated status", "ready", ready.Reason, "matchedPods", status.MatchedPods)
	return ctrl.Result{}, nil
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
		Named("logalert").
		Complete(r)
}
