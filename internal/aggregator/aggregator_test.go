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

package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

var key = types.NamespacedName{Namespace: "pay", Name: "errors"}

func TestWindow(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	b := func(ago time.Duration, n int64, sample string) Bucket {
		return Bucket{Namespace: "pay", Rule: "errors", Start: now.Add(-ago), Count: n, Last: now.Add(-ago), Sample: sample}
	}
	s := NewStore()
	s.Add(Report{Node: "n1", Buckets: []Bucket{b(0, 2, "a"), b(30*time.Second, 3, "b"), b(5*time.Minute, 100, "c")}})
	s.Add(Report{Node: "n2", Buckets: []Bucket{b(0, 4, "d")}})
	// Resent bucket replaces, doesn't add.
	s.Add(Report{Node: "n1", Buckets: []Bucket{b(0, 5, "e")}})

	sum, last, sample, active := s.Window(key, time.Minute, now)
	if sum != 5+3+4 || !active {
		t.Errorf("sum = %d active = %v, want 12 true", sum, active)
	}
	if !last.Equal(now) || sample != "e" {
		t.Errorf("last = %v sample = %q, want now and e", last, sample)
	}
	if sum, _, _, _ := s.Window(key, 10*time.Minute, now); sum != 112 {
		t.Errorf("10m sum = %d, want 112", sum)
	}

	// Buckets older than MaxWindow are dropped; the last match is kept.
	sum, _, sample, active = s.Window(key, time.Hour, now.Add(2*time.Hour))
	if sum != 0 || active || sample != "e" {
		t.Errorf("after 2h: sum = %d active = %v sample = %q, want 0 false e", sum, active, sample)
	}

	s.Delete(key)
	if _, _, _, active := s.Window(key, time.Hour, now); active {
		t.Error("active after Delete")
	}
}

func TestSampleTruncated(t *testing.T) {
	s := NewStore()
	s.Add(Report{Buckets: []Bucket{{Namespace: "pay", Rule: "errors", Count: 1, Last: time.Now(), Sample: strings.Repeat("é", 200)}}})
	_, _, sample, _ := s.Window(key, time.Hour, time.Now())
	if len(sample) > MaxSample || !strings.HasPrefix(strings.Repeat("é", 200), sample) || len(sample) < MaxSample-1 {
		t.Errorf("sample len %d, want valid UTF-8 prefix of at most %d bytes", len(sample), MaxSample)
	}
}

func TestHandler(t *testing.T) {
	review := func(_ context.Context, token string) (string, error) {
		switch token {
		case "agent":
			return "system:serviceaccount:riffle-agent:riffle-agent", nil
		case "other":
			return "system:serviceaccount:default:default", nil
		}
		return "", errors.New("invalid")
	}
	s := NewStore()
	h := s.Handler(review, "system:serviceaccount:riffle-agent:riffle-agent")
	body, _ := json.Marshal(Report{Node: "n1", Buckets: []Bucket{{Namespace: "pay", Rule: "errors", Start: time.Now(), Count: 7}}})

	for _, tc := range []struct {
		name, method, auth, body string
		want                     int
	}{
		{"get", http.MethodGet, "Bearer agent", string(body), http.StatusMethodNotAllowed},
		{"no token", http.MethodPost, "", string(body), http.StatusUnauthorized},
		{"bad token", http.MethodPost, "Bearer nope", string(body), http.StatusUnauthorized},
		{"other sa", http.MethodPost, "Bearer other", string(body), http.StatusForbidden},
		{"bad json", http.MethodPost, "Bearer agent", "{", http.StatusBadRequest},
		{"ok", http.MethodPost, "Bearer agent", string(body), http.StatusNoContent},
	} {
		req := httptest.NewRequest(tc.method, Path, bytes.NewBufferString(tc.body))
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
	if sum, _, _, _ := s.Window(key, time.Minute, time.Now()); sum != 7 {
		t.Errorf("sum = %d, want 7 (only the accepted report)", sum)
	}
	if got := (<-s.Updated).Object; got != key {
		t.Errorf("Updated = %v, want %v", got, key)
	}
}
