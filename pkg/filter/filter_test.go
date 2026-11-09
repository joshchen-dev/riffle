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

package filter

import "testing"

func TestMatch(t *testing.T) {
	const (
		slow  = `{"level":"error","latency_ms":900,"msg":"upstream timeout","http":{"status":504}}`
		fast  = `{"level":"error","latency_ms":12,"msg":"bad input","http":{"status":400}}`
		info  = `{"level":"info","latency_ms":"700","user":null,"ok":true}`
		plain = `level=error upstream timeout`
	)
	tests := []struct {
		match, where string
		line         string
		want         bool
	}{
		{"", "", plain, true},
		{"timeout", "", plain, true},
		{"timeout", "", fast, false},
		{"", "level=error", plain, false}, // not JSON
		{"", "level=error AND latency_ms>500", slow, true},
		{"", "level=error AND latency_ms>500", fast, false},
		{"", "latency_ms>=900", slow, true},
		{"", "latency_ms<12", fast, false},
		{"", "latency_ms<=12", fast, true},
		{"", "latency_ms>500", info, true}, // numeric string
		{"", "latency_ms=700", info, true},
		{"", "http.status=504", slow, true},
		{"", "http.status>=500 OR level=info", info, true},
		{"", "NOT level=info", slow, true},
		{"", "not (level=error and latency_ms>500)", fast, true},
		{"", "level!=error", slow, false},
		{"", "missing!=x", slow, false}, // missing field never matches
		{"", "http=x", slow, false},     // object field never matches
		{"", `msg~"time(out)?"`, slow, true},
		{"", `msg~^bad`, fast, true},
		{"", `msg="upstream timeout"`, slow, true},
		{"", `user=null AND ok=true`, info, true},
		{"", `level="error"`, slow, true},
		{"timeout", "latency_ms>500", slow, true},
		{"nope", "latency_ms>500", slow, false},
	}
	for _, tt := range tests {
		f, err := Compile(tt.match, tt.where, nil)
		if err != nil {
			t.Fatalf("Compile(%q, %q): %v", tt.match, tt.where, err)
		}
		if got := f.Match(tt.line); got != tt.want {
			t.Errorf("match=%q where=%q on %s: got %v, want %v", tt.match, tt.where, tt.line, got, tt.want)
		}
	}
}

func TestPrecedence(t *testing.T) {
	// AND binds tighter than OR: a OR (b AND c).
	f, err := Compile("", "a=1 OR b=1 AND c=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Match(`{"a":1,"b":0,"c":0}`) {
		t.Error("a=1 alone should match")
	}
	if f.Match(`{"a":0,"b":1,"c":0}`) {
		t.Error("b=1 without c=1 should not match")
	}
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		match, where string
		redact       []string
	}{
		{match: "("},
		{where: "level"},
		{where: "level="},
		{where: "level=error AND"},
		{where: "(level=error"},
		{where: "level=error)"},
		{where: "latency_ms>fast"},
		{where: "msg~(("},
		{where: `msg="open`},
		{where: "a!b"},
		{where: "=x"},
		{where: `"a"=x`},
		{where: "a..b=x"},
		{redact: []string{`ok`, `[`}},
	}
	for _, tt := range tests {
		if _, err := Compile(tt.match, tt.where, tt.redact); err == nil {
			t.Errorf("Compile(%q, %q, %q): want error", tt.match, tt.where, tt.redact)
		}
	}
}

func TestRedact(t *testing.T) {
	f, err := Compile("", "", []string{`card=\d+`, `token=\S+`})
	if err != nil {
		t.Fatal(err)
	}
	got := f.Redact("pay card=4111111111111111 token=abc$1 ok")
	want := "pay [REDACTED] [REDACTED] ok"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
