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

// Package archive writes redacted matched lines to S3 as gzipped JSON Lines,
// partitioned by namespace, rule, day and hour.
package archive

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/prometheus/client_golang/prometheus"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/joshchen-dev/riffle/internal/aggregator"
)

// Putter is the part of the S3 client the archive uses.
type Putter interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// Archiver buffers records and uploads them every Interval.
type Archiver struct {
	S3         Putter
	Bucket     string
	Prefix     string
	Interval   time.Duration
	MaxPending int // records buffered while S3 is unreachable; the oldest are dropped beyond this

	mu       sync.Mutex
	pending  []aggregator.Record
	lines    prometheus.Counter
	dropped  prometheus.Counter
	failures prometheus.Counter
}

// New returns an Archiver with its metrics registered in reg.
func New(s3c Putter, bucket, prefix string, reg prometheus.Registerer) *Archiver {
	a := &Archiver{
		S3: s3c, Bucket: bucket, Prefix: prefix, Interval: time.Minute, MaxPending: 100_000,
		lines:    prometheus.NewCounter(prometheus.CounterOpts{Name: "riffle_archived_lines_total", Help: "Lines written to S3."}),
		dropped:  prometheus.NewCounter(prometheus.CounterOpts{Name: "riffle_archive_dropped_total", Help: "Lines dropped because S3 was unreachable for too long."}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{Name: "riffle_archive_upload_failures_total", Help: "Failed S3 uploads."}),
	}
	reg.MustRegister(a.lines, a.dropped, a.failures)
	return a
}

// Add buffers records until the next flush.
func (a *Archiver) Add(rs []aggregator.Record) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = append(a.pending, rs...)
	if over := len(a.pending) - a.MaxPending; over > 0 {
		a.pending = a.pending[over:]
		a.dropped.Add(float64(over))
	}
}

// Start flushes every Interval until ctx is done, then flushes once more.
func (a *Archiver) Start(ctx context.Context) error {
	tick := time.NewTicker(a.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			a.Flush(fctx)
			return nil
		case <-tick.C:
			a.Flush(ctx)
		}
	}
}

type group struct{ ns, rule, hour string }

// Flush uploads one object per namespace, rule and hour. Records of failed uploads go back to the buffer.
func (a *Archiver) Flush(ctx context.Context) {
	a.mu.Lock()
	recs := a.pending
	a.pending = nil
	a.mu.Unlock()

	groups := map[group][]aggregator.Record{}
	for _, r := range recs {
		g := group{r.Namespace, r.Rule, r.Time.UTC().Format("2006-01-02/15")}
		groups[g] = append(groups[g], r)
	}
	now := time.Now().UTC().Format("20060102T150405.000000000Z")
	for g, rs := range groups {
		day, hour := g.hour[:10], g.hour[11:]
		key := path.Join(a.Prefix, g.ns, g.rule, "dt="+day, "hour="+hour, now+".jsonl.gz")
		if err := a.put(ctx, key, rs); err != nil {
			logf.FromContext(ctx).Error(err, "Failed to archive", "key", key)
			a.failures.Inc()
			a.Add(rs)
			continue
		}
		a.lines.Add(float64(len(rs)))
	}
}

func (a *Archiver) put(ctx context.Context, key string, rs []aggregator.Record) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	enc := json.NewEncoder(zw)
	for _, r := range rs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	_, err := a.S3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(a.Bucket),
		Key:             aws.String(key),
		Body:            bytes.NewReader(buf.Bytes()),
		ContentType:     aws.String("application/x-ndjson"),
		ContentEncoding: aws.String("gzip"),
	})
	if err != nil {
		return fmt.Errorf("put s3://%s/%s: %w", a.Bucket, key, err)
	}
	return nil
}
