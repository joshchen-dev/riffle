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

// Package aggregator sums per-node match counts that agents push, so the operator can
// evaluate cluster-wide thresholds.
package aggregator

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

const (
	// BucketSize is the time span of one count.
	BucketSize = 10 * time.Second
	// MaxWindow is the longest threshold window; older buckets are dropped.
	MaxWindow = time.Hour
	// MaxSample is the longest sample kept, in bytes.
	MaxSample = 256
	// Audience is the token audience agents use and the operator checks.
	Audience = "riffle-operator"
	// Path is where agents POST a Report.
	Path = "/v1/counts"
	// MaxLines is how many matched lines an agent sends per rule and bucket, for the archive.
	MaxLines = 100
)

// Report is what an agent POSTs.
type Report struct {
	Node    string   `json:"node"`
	Buckets []Bucket `json:"buckets"`
}

// Bucket is the match count for one rule in one BucketSize span on one node.
// Agents may resend a bucket; the latest value replaces the earlier one.
type Bucket struct {
	Namespace string    `json:"namespace"`
	Rule      string    `json:"rule"`
	Start     time.Time `json:"start"`
	Count     int64     `json:"count"`
	Last      time.Time `json:"last"`            // time of the last match
	Sample    string    `json:"sample"`          // last matched line, redacted
	Lines     []Line    `json:"lines,omitempty"` // first MaxLines matched lines, redacted; only grows
}

// Line is one matched line.
type Line struct {
	Time time.Time `json:"t"`
	Text string    `json:"line"`
}

// Record is a matched line on its way to the archive.
type Record struct {
	Namespace string    `json:"namespace"`
	Rule      string    `json:"rule"`
	Node      string    `json:"node"`
	Time      time.Time `json:"t"`
	Line      string    `json:"line"`
}

// Store keeps buckets in memory.
// ponytail: a restart loses the window; persist buckets if windows grow past 1h.
type Store struct {
	mu    sync.Mutex
	rules map[types.NamespacedName]*rule
	// Updated receives the key of every rule a Report touched.
	Updated chan event.TypedGenericEvent[types.NamespacedName]
	// Archive, if set, receives lines not seen before.
	Archive func([]Record)
}

type rule struct {
	counts map[nodeBucket]*slot
	last   time.Time
	sample string
}

type slot struct {
	count    int64
	archived int // lines already passed to Archive; a resent bucket only adds lines after these
}

type nodeBucket struct {
	node  string
	start time.Time
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{rules: map[types.NamespacedName]*rule{}, Updated: make(chan event.TypedGenericEvent[types.NamespacedName], 1024)}
}

// Add records a report and returns the rules it touched and the lines not seen before.
// ponytail: an operator restart forgets which lines were archived, so the current bucket's lines can repeat.
func (s *Store) Add(r Report) ([]types.NamespacedName, []Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	touched := make([]types.NamespacedName, 0, len(r.Buckets))
	var fresh []Record
	for _, b := range r.Buckets {
		key := types.NamespacedName{Namespace: b.Namespace, Name: b.Rule}
		ru := s.rules[key]
		if ru == nil {
			ru = &rule{counts: map[nodeBucket]*slot{}}
			s.rules[key] = ru
		}
		nb := nodeBucket{r.Node, b.Start.Truncate(BucketSize)}
		sl := ru.counts[nb]
		if sl == nil {
			sl = &slot{}
			ru.counts[nb] = sl
		}
		sl.count = b.Count
		for _, l := range b.Lines[min(sl.archived, len(b.Lines)):min(len(b.Lines), MaxLines)] {
			fresh = append(fresh, Record{Namespace: b.Namespace, Rule: b.Rule, Node: r.Node, Time: l.Time, Line: l.Text})
		}
		sl.archived = max(sl.archived, min(len(b.Lines), MaxLines))
		if b.Count > 0 && !b.Last.Before(ru.last) {
			ru.last, ru.sample = b.Last, truncate(b.Sample, MaxSample)
		}
		touched = append(touched, key)
	}
	return touched, fresh
}

// Window returns the sum of buckets that start within window of now, plus the last match.
// active is false once the rule has no buckets left.
func (s *Store) Window(key types.NamespacedName, window time.Duration, now time.Time) (sum int64, last time.Time, sample string, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ru := s.rules[key]
	if ru == nil {
		return 0, time.Time{}, "", false
	}
	// A bucket counts while any part of it is inside the window.
	from := now.Add(-window - BucketSize)
	for nb, sl := range ru.counts {
		switch {
		case !nb.start.After(now.Add(-MaxWindow - BucketSize)):
			delete(ru.counts, nb)
		case nb.start.After(from):
			sum += sl.count
		}
	}
	return sum, ru.last, ru.sample, len(ru.counts) > 0
}

// Delete forgets a rule.
func (s *Store) Delete(key types.NamespacedName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rules, key)
}

// Handler accepts Reports from callers whose bearer token review returns user.
func (s *Store) Handler(review func(ctx context.Context, token string) (string, error), user string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
		if !ok {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		// ponytail: one TokenReview per push; cache reviews if agents × push rate gets large.
		got, err := review(req.Context(), token)
		if err != nil {
			http.Error(w, "token review failed", http.StatusUnauthorized)
			return
		}
		if got != user {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var r Report
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		touched, fresh := s.Add(r)
		if s.Archive != nil && len(fresh) > 0 {
			s.Archive(fresh)
		}
		for _, key := range touched {
			select {
			case s.Updated <- event.TypedGenericEvent[types.NamespacedName]{Object: key}:
			case <-req.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
