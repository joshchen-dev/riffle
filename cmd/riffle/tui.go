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
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/joshchen-dev/riffle/internal/tail"
	"github.com/joshchen-dev/riffle/pkg/filter"
)

// maxLines is how many lines the TUI keeps, so a new filter also applies to recent history.
const maxLines = 10000

type lineMsg tail.Line

type errMsg string

// sendWriter turns stream errors into messages, so they show in the status line instead of breaking the screen.
type sendWriter func(tea.Msg)

func (w sendWriter) Write(p []byte) (int, error) {
	w(errMsg(strings.TrimSpace(string(p))))
	return len(p), nil
}

// runTUI tails into m and prints the saved LogAlert YAML, if any, after the screen closes.
// Lines are filtered in the TUI; o only redacts.
func runTUI(ctx context.Context, cs kubernetes.Interface, o tail.Options, m *tui, redact []string,
	out, errOut io.Writer) int {
	var err error
	if o.Filter, err = filter.Compile("", "", redact); err != nil {
		fmt.Fprintf(errOut, "riffle tail: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := tea.NewProgram(m, tea.WithContext(ctx))
	o.Sink = func(l tail.Line) { p.Send(lineMsg(l)) }
	go func() {
		if err := tail.Run(ctx, cs, o, io.Discard, sendWriter(p.Send)); err != nil {
			p.Send(errMsg(err.Error()))
		}
	}()
	if _, err := p.Run(); err != nil && ctx.Err() == nil {
		fmt.Fprintf(errOut, "riffle tail: %v\n", err)
		return 1
	}
	if m.saved != "" {
		fmt.Fprint(out, m.saved)
		fmt.Fprintln(errOut, "riffle: set the name and threshold, then kubectl apply -f")
	}
	return 0
}

// tui shows tailed lines that pass an editable match/where filter.
// ctrl+s quits and leaves the filter as LogAlert YAML in saved.
type tui struct {
	ns, selector, container string

	lines   []tail.Line
	inputs  [2]textinput.Model // match, where
	valid   [2]string          // last value of each input that compiled
	focus   int
	filter  *filter.Filter
	err     error
	width   int
	height  int
	saved   string
	saveErr error
	tailErr string
}

func newTUI(ns, selector, container, match, where string) *tui {
	m := &tui{ns: ns, selector: selector, container: container, filter: &filter.Filter{}, height: 24, width: 80}
	for i, label := range []string{"match: ", "where: "} {
		m.inputs[i] = textinput.New()
		m.inputs[i].Prompt = label
	}
	m.inputs[0].Placeholder = "RE2 regex"
	m.inputs[1].Placeholder = "level=error AND latency_ms>500"
	m.inputs[0].SetValue(match)
	m.inputs[1].SetValue(where)
	m.inputs[1].Focus()
	m.focus = 1
	m.compile()
	return m
}

func (m *tui) Init() tea.Cmd { return nil }

// compile keeps the last valid value of each field, so an incomplete where doesn't freeze match.
func (m *tui) compile() {
	m.err = nil
	if _, err := filter.Compile(m.inputs[0].Value(), "", nil); err != nil {
		m.err = err
	} else {
		m.valid[0] = m.inputs[0].Value()
	}
	if _, err := filter.Compile("", m.inputs[1].Value(), nil); err != nil {
		m.err = err
	} else {
		m.valid[1] = m.inputs[1].Value()
	}
	m.filter, _ = filter.Compile(m.valid[0], m.valid[1], nil) // both parts compiled above
}

func (m *tui) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case lineMsg:
		m.lines = append(m.lines, tail.Line(msg))
		if len(m.lines) > maxLines {
			m.lines = m.lines[len(m.lines)-maxLines:]
		}
		return m, nil
	case errMsg:
		m.tailErr = string(msg)
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "tab", "shift+tab":
			m.inputs[m.focus].Blur()
			m.focus = 1 - m.focus
			return m, m.inputs[m.focus].Focus()
		case "ctrl+s":
			m.save()
			if m.saveErr == nil {
				return m, tea.Quit
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	m.compile()
	m.saveErr = nil
	return m, cmd
}

func (m *tui) save() {
	la, err := newAlert("new-rule", m.ns, m.selector, m.container, m.inputs[0].Value(), m.inputs[1].Value(),
		1, 5*time.Minute, nil)
	if err != nil {
		m.saveErr = err
		return
	}
	b, err := yaml.Marshal(la)
	m.saved, m.saveErr = string(b), err
}

func (m *tui) View() tea.View {
	const footer = 4 // two inputs, status, help
	rows := max(m.height-footer, 1)

	// Newest matching lines, scanning back from the end.
	var shown []string
	for i := len(m.lines) - 1; i >= 0 && len(shown) < rows; i-- {
		l := m.lines[i]
		if m.filter.Match(l.Raw) {
			shown = append(shown, truncateCells(fmt.Sprintf("%s/%s %s", l.Pod, l.Container, l.Msg), m.width))
		}
	}
	var b strings.Builder
	for range rows - len(shown) {
		b.WriteString(" \n") // not empty, so the renderer keeps the footer at the bottom
	}
	for _, s := range slices.Backward(shown) {
		b.WriteString(s + "\n")
	}
	b.WriteString(m.inputs[0].View() + "\n")
	b.WriteString(m.inputs[1].View() + "\n")
	switch {
	case m.saveErr != nil:
		fmt.Fprintf(&b, "can't save: %v\n", m.saveErr)
	case m.err != nil:
		fmt.Fprintf(&b, "%v (showing last valid filter)\n", m.err)
	case m.tailErr != "":
		fmt.Fprintln(&b, m.tailErr)
	default:
		fmt.Fprintf(&b, "%d lines buffered\n", len(m.lines))
	}
	b.WriteString("tab switch field · ctrl+s save as LogAlert · esc quit")
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// truncateCells cuts s to n runes.
// ponytail: counts runes, not terminal cells; wide characters can still wrap.
func truncateCells(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n])
}
