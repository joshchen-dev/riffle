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

package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"sigs.k8s.io/yaml"

	rifflev1alpha1 "github.com/joshchen-dev/riffle/api/v1alpha1"
	"github.com/joshchen-dev/riffle/internal/tail"
)

func typeText(m *tui, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func screen(m *tui) string { return m.View().Content }

func TestTUIFilters(t *testing.T) {
	m := newTUI("pay", "app=api", "", "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	for _, msg := range []string{`{"level":"error","n":1}`, `{"level":"info","n":2}`, `plain timeout`} {
		m.Update(lineMsg(tail.Line{Pod: "p1", Container: "c", Raw: msg, Msg: msg}))
	}
	if s := screen(m); !strings.Contains(s, `"n":1`) || !strings.Contains(s, `"n":2`) || !strings.Contains(s, "plain") {
		t.Fatalf("empty filter should show everything:\n%s", s)
	}

	typeText(m, "level=error")
	s := screen(m)
	if !strings.Contains(s, `p1/c {"level":"error","n":1}`) ||
		strings.Contains(s, `"n":2`) || strings.Contains(s, "plain") {
		t.Errorf("where level=error:\n%s", s)
	}

	// Incomplete expression: error shown, last valid filter kept.
	typeText(m, " AND n>")
	if s := screen(m); !strings.Contains(s, "showing last valid filter") || !strings.Contains(s, `"n":1`) {
		t.Errorf("invalid where:\n%s", s)
	}

	// tab moves to match; a match that excludes everything empties the view.
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	typeText(m, "zzz")
	if m.inputs[0].Value() != "zzz" || strings.Contains(screen(m), `"n":1`) {
		t.Errorf("match not applied: match=%q\n%s", m.inputs[0].Value(), screen(m))
	}
}

// Lines are matched on the raw text and shown redacted, like plain tail.
// Redaction can break JSON, so matching the redacted text would hide every line.
func TestTUIMatchesRawShowsRedacted(t *testing.T) {
	m := newTUI("pay", "app=api", "", "", "level=error")
	m.Update(lineMsg(tail.Line{Pod: "p1", Container: "c",
		Raw: `{"level":"error","card":"4111"}`, Msg: `{"level":"error",[REDACTED]}`}))
	s := screen(m)
	if !strings.Contains(s, `p1/c {"level":"error",[REDACTED]}`) || strings.Contains(s, "4111") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestTUIKeepsMaxLines(t *testing.T) {
	m := newTUI("pay", "app=api", "", "", "")
	for range maxLines + 5 {
		m.Update(lineMsg(tail.Line{Msg: "x"}))
	}
	if len(m.lines) != maxLines {
		t.Errorf("lines = %d, want %d", len(m.lines), maxLines)
	}
}

func TestTUISave(t *testing.T) {
	m := newTUI("pay", "app=api", "api", "timeout", "level=error")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+s should quit")
	}
	var la rifflev1alpha1.LogAlert
	if err := yaml.UnmarshalStrict([]byte(m.saved), &la); err != nil {
		t.Fatalf("%v\n%s", err, m.saved)
	}
	if la.Namespace != "pay" || la.Spec.Container != "api" ||
		la.Spec.Match != "timeout" || la.Spec.Where != "level=error" {
		t.Errorf("saved %+v", la)
	}

	// No selector: stay open and say why.
	m = newTUI("pay", "", "", "", "level=error")
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}); cmd != nil {
		t.Error("ctrl+s without -l should not quit")
	}
	if !strings.Contains(screen(m), "can't save: -l is required") {
		t.Errorf("screen:\n%s", screen(m))
	}
}

func TestTUIShowsTailErrors(t *testing.T) {
	m := newTUI("pay", "app=api", "", "", "")
	_, _ = sendWriter(func(msg tea.Msg) { m.Update(msg) }).Write([]byte("riffle: p1/c: forbidden\n"))
	if !strings.Contains(screen(m), "riffle: p1/c: forbidden") {
		t.Errorf("screen:\n%s", screen(m))
	}
}
