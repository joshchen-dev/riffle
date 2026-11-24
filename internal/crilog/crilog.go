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

// Package crilog follows container log files written in the CRI format
// (/var/log/pods/<ns>_<pod>_<uid>/<container>/<n>.log), across rotation and truncation.
package crilog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// MaxLine caps a message joined from partial lines. The rest is dropped.
const MaxLine = 1 << 20

// Entry is one full log message.
type Entry struct {
	Time   time.Time
	Stream string // stdout or stderr
	Msg    string
}

// parse splits "<RFC3339Nano> <stream> <P|F> <msg>".
func parse(line string) (t time.Time, stream string, partial bool, msg string, err error) {
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 3 {
		return t, "", false, "", fmt.Errorf("bad CRI line %q", line)
	}
	if t, err = time.Parse(time.RFC3339Nano, parts[0]); err != nil {
		return t, "", false, "", err
	}
	if len(parts) == 4 {
		msg = parts[3]
	}
	return t, parts[1], parts[2] == "P", msg, nil
}

// Follow reads path and calls emit for every full message until ctx is done.
// It starts at the end of the file unless fromStart. A missing file is waited for.
func Follow(ctx context.Context, path string, fromStart bool, poll time.Duration, emit func(Entry)) error {
	fl := &follower{path: path, emit: emit}
	defer fl.close()
	wait := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(poll):
			return true
		}
	}

	for {
		err := fl.open(!fromStart)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fromStart = true // created after we started: read all of it
		if !wait() {
			return nil
		}
	}

	for {
		if err := fl.drain(); err != nil {
			return err
		}
		cur, err := fl.f.Stat()
		if err != nil {
			return err
		}
		onDisk, err := os.Stat(fl.path)
		switch {
		case err == nil && !os.SameFile(cur, onDisk):
			// Rotated. The runtime writes to the old file until it creates the new one, so lines may
			// have landed between the drain above and the Stat. The new file exists now, so this drain
			// gets the last of them. (Not covered by tests: the window is too narrow to hit reliably.)
			if err := fl.drain(); err != nil {
				return err
			}
			if err := fl.open(false); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		case err == nil && onDisk.Size() < fl.pos:
			// Truncated in place.
			if err := fl.open(false); err != nil {
				return err
			}
			continue
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return err
		}
		// Unchanged, or renamed away and the new file isn't there yet.
		if !wait() {
			return nil
		}
	}
}

type follower struct {
	path    string
	emit    func(Entry)
	f       *os.File
	r       *bufio.Reader
	pos     int64
	pending []byte          // bytes after the last newline
	joined  strings.Builder // P chunks waiting for their F line
}

// open (re)opens path, dropping any half-read state from the previous file.
func (fl *follower) open(seekEnd bool) error {
	f, err := os.Open(fl.path)
	if err != nil {
		return err
	}
	fl.close()
	fl.f, fl.pos, fl.pending = f, 0, nil
	fl.joined.Reset()
	if seekEnd {
		if fl.pos, err = f.Seek(0, io.SeekEnd); err != nil {
			return err
		}
	}
	fl.r = bufio.NewReader(f)
	return nil
}

func (fl *follower) close() {
	if fl.f != nil {
		_ = fl.f.Close()
	}
}

// drain reads complete lines to EOF.
func (fl *follower) drain() error {
	for {
		b, err := fl.r.ReadBytes('\n')
		fl.pos += int64(len(b))
		if err == nil {
			fl.handle(string(append(fl.pending, b[:len(b)-1]...)))
			fl.pending = nil
			continue
		}
		fl.pending = append(fl.pending, b...)
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func (fl *follower) handle(line string) {
	t, stream, partial, msg, err := parse(line)
	if err != nil {
		return // ponytail: malformed lines are skipped silently; count them once the agent has metrics
	}
	if fl.joined.Len()+len(msg) <= MaxLine {
		fl.joined.WriteString(msg)
	}
	if partial {
		return
	}
	fl.emit(Entry{Time: t, Stream: stream, Msg: fl.joined.String()})
	fl.joined.Reset()
}
