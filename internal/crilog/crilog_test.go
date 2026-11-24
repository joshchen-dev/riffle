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

package crilog

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const ts = "2026-11-24T01:02:03.000000001Z"

type collector struct {
	mu   sync.Mutex
	msgs []string
}

func (c *collector) emit(e Entry) { c.mu.Lock(); defer c.mu.Unlock(); c.msgs = append(c.msgs, e.Msg) }

// waitFor polls until the collected messages equal want.
func (c *collector) waitFor(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		got := slices.Clone(c.msgs)
		c.mu.Unlock()
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %q, want %q", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func appendTo(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(strings.Join(lines, "")); err != nil {
		t.Fatal(err)
	}
}

func cri(tag, msg string) string { return ts + " stdout " + tag + " " + msg + "\n" }

func follow(t *testing.T, path string, fromStart bool) *collector {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	c := &collector{}
	go func() { done <- Follow(ctx, path, fromStart, 5*time.Millisecond, c.emit) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Follow: %v", err)
		}
	})
	return c
}

func TestParse(t *testing.T) {
	tm, stream, partial, msg, err := parse(ts + " stderr P hello world")
	if err != nil || stream != "stderr" || !partial || msg != "hello world" || tm.Nanosecond() != 1 {
		t.Errorf("got %v %q %v %q %v", tm, stream, partial, msg, err)
	}
	if _, _, _, msg, err := parse(ts + " stdout F"); err != nil || msg != "" {
		t.Errorf("empty message: %q %v", msg, err)
	}
	for _, bad := range []string{"", "nope", "notatime stdout F x"} {
		if _, _, _, _, err := parse(bad); err == nil {
			t.Errorf("parse(%q): want error", bad)
		}
	}
}

func TestStartsAtEndAndJoinsPartials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "0.log")
	appendTo(t, path, cri("F", "old"))
	c := follow(t, path, false)
	time.Sleep(20 * time.Millisecond)

	appendTo(t, path, cri("P", "a "), cri("P", "b "), cri("F", "c"), cri("F", "d"))
	c.waitFor(t, "a b c", "d")
}

func TestHalfWrittenLineWaitsForNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "0.log")
	appendTo(t, path, "")
	c := follow(t, path, true)

	line := cri("F", "whole")
	appendTo(t, path, line[:20])
	time.Sleep(30 * time.Millisecond)
	c.waitFor(t)
	appendTo(t, path, line[20:])
	c.waitFor(t, "whole")
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "0.log")
	appendTo(t, path, "")
	c := follow(t, path, true)
	appendTo(t, path, cri("F", "1"))
	c.waitFor(t, "1")

	// Kubelet renames the file, the runtime writes a last line to it, then reopens a new 0.log.
	if err := os.Rename(path, path+".20261124-010203"); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path+".20261124-010203", cri("F", "2"))
	appendTo(t, path, cri("F", "3"))
	c.waitFor(t, "1", "2", "3")
}

func TestTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "0.log")
	appendTo(t, path, "")
	c := follow(t, path, true)
	appendTo(t, path, cri("F", "before-truncate"))
	c.waitFor(t, "before-truncate")

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	appendTo(t, path, cri("F", "x"))
	c.waitFor(t, "before-truncate", "x")
}

func TestWaitsForMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "0.log")
	c := follow(t, path, false)
	time.Sleep(20 * time.Millisecond)
	appendTo(t, path, cri("F", "first"))
	c.waitFor(t, "first")
}

func TestCapsJoinedMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "0.log")
	appendTo(t, path, "")
	c := follow(t, path, true)
	chunk := strings.Repeat("x", MaxLine/2+1)
	appendTo(t, path, cri("P", chunk), cri("P", chunk), cri("F", "end"))
	c.waitFor(t, chunk+"end")
}
