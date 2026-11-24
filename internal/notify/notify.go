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

// Package notify posts alerts to a webhook whose URL is kept in a Secret.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Alert statuses.
const (
	Firing   = "firing"
	Resolved = "resolved"
)

// Alert is the webhook body. text makes it a valid Slack incoming-webhook message.
type Alert struct {
	Text      string    `json:"text"`
	Status    string    `json:"status"`
	Namespace string    `json:"namespace"`
	Rule      string    `json:"rule"`
	Count     int64     `json:"count"`
	Threshold int32     `json:"threshold"`
	Window    string    `json:"window"`
	Sample    string    `json:"sample,omitempty"` // redacted
	Time      time.Time `json:"time"`
}

// Webhook reads the URL from Secret key "url" on every send, so changes need no restart.
type Webhook struct {
	Reader  client.Reader // uncached, so the operator needs no list/watch on Secrets
	Secret  types.NamespacedName
	Backoff time.Duration // between attempts, doubled each time
}

// Notify sends a in the background.
func (w *Webhook) Notify(a Alert) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := w.Send(ctx, a); err != nil {
			logf.Log.WithName("notify").Error(err, "Failed to send webhook", "namespace", a.Namespace, "rule", a.Rule, "status", a.Status)
		}
	}()
}

// Send posts a, trying 3 times. No Secret means no webhook is configured.
// ponytail: alerts are lost if all attempts fail; queue them if receivers are often down.
func (w *Webhook) Send(ctx context.Context, a Alert) error {
	var s corev1.Secret
	if err := w.Reader.Get(ctx, w.Secret, &s); err != nil {
		return client.IgnoreNotFound(err)
	}
	url := string(s.Data["url"])
	if url == "" {
		return fmt.Errorf("secret %s has no url key", w.Secret)
	}
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	backoff := w.Backoff
	for attempt := 1; ; attempt++ {
		err = post(ctx, url, body)
		if err == nil || attempt == 3 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

func post(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: %s", resp.Status)
	}
	return nil
}
