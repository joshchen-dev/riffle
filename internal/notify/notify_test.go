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

package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var key = types.NamespacedName{Namespace: "riffle-system", Name: "riffle-webhook"}

func secret(url string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Data:       map[string][]byte{"url": []byte(url)},
	}
}

func TestSend(t *testing.T) {
	var got []Alert
	fails := 2
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fails > 0 {
			fails--
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		var a Alert
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			t.Error(err)
		}
		got = append(got, a)
	}))
	defer srv.Close()

	w := &Webhook{Reader: fake.NewClientBuilder().WithObjects(secret(srv.URL)).Build(), Secret: key}
	if err := w.Send(t.Context(), Alert{Text: "hi", Status: Firing, Rule: "errors"}); err != nil {
		t.Fatalf("Send after 2 failures: %v", err)
	}
	if len(got) != 1 || got[0].Text != "hi" || got[0].Status != Firing {
		t.Errorf("got %+v, want one firing alert", got)
	}

	fails = 3
	if err := w.Send(t.Context(), Alert{}); err == nil {
		t.Error("Send after 3 failures: want error")
	}
}

func TestSendWithoutSecret(t *testing.T) {
	w := &Webhook{Reader: fake.NewClientBuilder().Build(), Secret: key}
	if err := w.Send(t.Context(), Alert{}); err != nil {
		t.Errorf("no Secret: %v, want nil", err)
	}
	w.Reader = fake.NewClientBuilder().WithObjects(secret("")).Build()
	if err := w.Send(t.Context(), Alert{}); err == nil {
		t.Error("Secret without url: want error")
	}
}
