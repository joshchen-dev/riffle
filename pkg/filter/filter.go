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

// Package filter compiles LogAlert match, where and redact rules.
// The operator, agent and CLI share it so a rule means the same thing everywhere.
package filter

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Redacted replaces every redact match.
const Redacted = "[REDACTED]"

// Filter is a compiled rule. The zero value matches every line.
type Filter struct {
	match  *regexp.Regexp
	where  expr
	redact []*regexp.Regexp
}

// Compile builds a Filter. Empty match or where are ignored.
func Compile(match, where string, redact []string) (*Filter, error) {
	f := &Filter{}
	var err error
	if match != "" {
		if f.match, err = regexp.Compile(match); err != nil {
			return nil, fmt.Errorf("match: %w", err)
		}
	}
	if where != "" {
		if f.where, err = parseWhere(where); err != nil {
			return nil, fmt.Errorf("where: %w", err)
		}
	}
	for i, r := range redact {
		re, err := regexp.Compile(r)
		if err != nil {
			return nil, fmt.Errorf("redact[%d]: %w", i, err)
		}
		f.redact = append(f.redact, re)
	}
	return f, nil
}

// Match reports whether line passes match and where.
// A where never matches a line that isn't a JSON object.
func (f *Filter) Match(line string) bool {
	if f.match != nil && !f.match.MatchString(line) {
		return false
	}
	if f.where == nil {
		return true
	}
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) != nil {
		return false
	}
	return f.where.eval(obj)
}

// Redact replaces every redact match in line.
func (f *Filter) Redact(line string) string {
	for _, re := range f.redact {
		line = re.ReplaceAllLiteralString(line, Redacted)
	}
	return line
}
