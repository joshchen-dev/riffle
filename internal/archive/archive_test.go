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

package archive

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/joshchen-dev/riffle/internal/aggregator"
)

type fakeS3 struct {
	objects map[string][]aggregator.Record
	fail    bool
}

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if f.fail {
		return nil, errors.New("unreachable")
	}
	zr, err := gzip.NewReader(in.Body)
	if err != nil {
		return nil, err
	}
	var rs []aggregator.Record
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		var r aggregator.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	f.objects[*in.Key] = rs
	_, _ = io.Copy(io.Discard, zr)
	return &s3.PutObjectOutput{}, nil
}

func TestFlush(t *testing.T) {
	t0 := time.Date(2026, 11, 25, 13, 59, 0, 0, time.UTC)
	rec := func(rule string, at time.Time, line string) aggregator.Record {
		return aggregator.Record{Namespace: "pay", Rule: rule, Node: "n1", Time: at, Line: line}
	}
	f := &fakeS3{objects: map[string][]aggregator.Record{}, fail: true}
	a := New(f, "bucket", "riffle", prometheus.NewRegistry())
	a.Add([]aggregator.Record{
		rec("errors", t0, "a"), rec("errors", t0.Add(30*time.Second), "b"),
		rec("errors", t0.Add(2*time.Minute), "c"), // next hour
		rec("slow", t0, "d"),
	})

	// S3 down: nothing lost.
	a.Flush(t.Context())
	if len(f.objects) != 0 || len(a.pending) != 4 || testutil.ToFloat64(a.failures) != 3 {
		t.Fatalf("failed flush: objects %d pending %d failures %v", len(f.objects), len(a.pending), testutil.ToFloat64(a.failures))
	}

	f.fail = false
	a.Flush(t.Context())
	if len(a.pending) != 0 || testutil.ToFloat64(a.lines) != 4 {
		t.Errorf("pending %d archived %v, want 0 and 4", len(a.pending), testutil.ToFloat64(a.lines))
	}
	got := map[string]int{}
	for key, rs := range f.objects {
		if !strings.HasSuffix(key, ".jsonl.gz") {
			t.Errorf("key %q", key)
		}
		got[key[:strings.LastIndex(key, "/")]] = len(rs)
	}
	want := map[string]int{
		"riffle/pay/errors/dt=2026-11-25/hour=13": 2,
		"riffle/pay/errors/dt=2026-11-25/hour=14": 1,
		"riffle/pay/slow/dt=2026-11-25/hour=13":   1,
	}
	if len(got) != len(want) {
		t.Fatalf("objects %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s: %d records, want %d", k, got[k], n)
		}
	}
}

func TestAddDropsOldestBeyondMaxPending(t *testing.T) {
	a := New(&fakeS3{}, "b", "p", prometheus.NewRegistry())
	a.MaxPending = 3
	for _, l := range []string{"1", "2", "3", "4", "5"} {
		a.Add([]aggregator.Record{{Line: l}})
	}
	if len(a.pending) != 3 || a.pending[0].Line != "3" || testutil.ToFloat64(a.dropped) != 2 {
		t.Errorf("pending %v dropped %v", a.pending, testutil.ToFloat64(a.dropped))
	}
}

func TestStartFlushesOnStop(t *testing.T) {
	f := &fakeS3{objects: map[string][]aggregator.Record{}}
	a := New(f, "b", "p", prometheus.NewRegistry())
	a.Interval = time.Hour
	a.Add([]aggregator.Record{{Namespace: "pay", Rule: "r", Time: time.Now()}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.Start(ctx); err != nil || len(f.objects) != 1 {
		t.Errorf("err %v objects %d, want final flush", err, len(f.objects))
	}
}
