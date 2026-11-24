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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// LogAlertSpec defines a log rule.
// +kubebuilder:validation:XValidation:rule="has(self.match) || has(self.where)",message="match or where is required"
type LogAlertSpec struct {
	// selector picks pods in the LogAlert's namespace.
	// +required
	Selector metav1.LabelSelector `json:"selector"`

	// container limits matching to one container. Empty means all.
	// +optional
	Container string `json:"container,omitempty"`

	// match is an RE2 regex tested against the raw line.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Match string `json:"match,omitempty"`

	// where filters JSON lines, e.g. `level=error AND latency_ms>500`.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Where string `json:"where,omitempty"`

	// threshold fires the alert when matches in window reach count.
	// +required
	Threshold Threshold `json:"threshold"`

	// redact lists RE2 regexes replaced with [REDACTED] in anything Riffle emits.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=1024
	Redact []string `json:"redact,omitempty"`

	// suspend stops matching without deleting the rule.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// Threshold is a match count over a sliding window.
type Threshold struct {
	// +required
	// +kubebuilder:validation:Minimum=1
	Count int32 `json:"count"`

	// window is between 1m and 1h.
	// +required
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('1m') && duration(self) <= duration('1h')",message="window must be between 1m and 1h"
	Window metav1.Duration `json:"window"`
}

// Condition types and reasons.
const (
	ConditionReady  = "Ready"
	ConditionFiring = "Firing"

	ReasonValid       = "Valid"
	ReasonInvalidSpec = "InvalidSpec"
	ReasonSuspended   = "Suspended"

	ReasonThresholdExceeded = "ThresholdExceeded"
	ReasonBelowThreshold    = "BelowThreshold"
	ReasonNotEvaluated      = "NotEvaluated"
)

// LogAlertStatus is the observed state of a LogAlert.
type LogAlertStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// matchedPods is the number of Running pods the selector picks.
	// +optional
	MatchedPods int32 `json:"matchedPods,omitempty"`

	// +optional
	LastMatchTime *metav1.Time `json:"lastMatchTime,omitempty"`

	// +optional
	LastFiredTime *metav1.Time `json:"lastFiredTime,omitempty"`

	// conditions: Ready, Firing.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Firing",type=string,JSONPath=`.status.conditions[?(@.type=="Firing")].status`
// +kubebuilder:printcolumn:name="Pods",type=integer,JSONPath=`.status.matchedPods`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LogAlert is a log rule for pods in its namespace.
type LogAlert struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LogAlert
	// +required
	Spec LogAlertSpec `json:"spec"`

	// status defines the observed state of LogAlert
	// +optional
	Status LogAlertStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// LogAlertList contains a list of LogAlert
type LogAlertList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LogAlert `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &LogAlert{}, &LogAlertList{})
		return nil
	})
}
