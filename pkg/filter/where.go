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

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// where grammar:
//
//	or   = and { "OR" and }
//	and  = not { "AND" not }
//	not  = "NOT" not | "(" or ")" | cmp
//	cmp  = field op value
//	op   = "=" | "!=" | ">" | ">=" | "<" | "<=" | "~"
//
// field is a dotted JSON path. value is a bare word or a "quoted string".
// AND, OR and NOT are case-insensitive.

type expr interface{ eval(obj map[string]any) bool }

type andExpr struct{ l, r expr }
type orExpr struct{ l, r expr }
type notExpr struct{ e expr }

type cmpExpr struct {
	path []string
	op   string
	val  string
	num  float64
	isN  bool           // val parses as a number
	re   *regexp.Regexp // op "~"
}

func (e andExpr) eval(o map[string]any) bool { return e.l.eval(o) && e.r.eval(o) }
func (e orExpr) eval(o map[string]any) bool  { return e.l.eval(o) || e.r.eval(o) }
func (e notExpr) eval(o map[string]any) bool { return !e.e.eval(o) }

// eval is false when the field is missing, an object or an array.
func (e cmpExpr) eval(o map[string]any) bool {
	var v any = o
	for _, k := range e.path {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if v, ok = m[k]; !ok {
			return false
		}
	}
	var s string
	switch x := v.(type) {
	case float64:
		if e.isN {
			return cmpNum(x, e.op, e.num)
		}
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		s = x
	case bool:
		s = strconv.FormatBool(x)
	case nil:
		s = "null"
	default:
		return false
	}
	switch e.op {
	case "=":
		return s == e.val
	case "!=":
		return s != e.val
	case "~":
		return e.re.MatchString(s)
	}
	// Ordered ops on a string field: compare numerically if it parses.
	f, err := strconv.ParseFloat(s, 64)
	return err == nil && e.isN && cmpNum(f, e.op, e.num)
}

func cmpNum(a float64, op string, b float64) bool {
	switch op {
	case "=":
		return a == b
	case "!=":
		return a != b
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	case "<=":
		return a <= b
	}
	return false
}

type tok struct {
	s      string
	quoted bool
}

func tokenize(in string) ([]tok, error) {
	var out []tok
	for i := 0; i < len(in); {
		c := in[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '(' || c == ')':
			out = append(out, tok{s: string(c)})
			i++
		case c == '"':
			s, n, err := unquote(in[i:])
			if err != nil {
				return nil, err
			}
			out = append(out, tok{s: s, quoted: true})
			i += n
		case strings.ContainsRune("=!<>~", rune(c)):
			n := 1
			if i+1 < len(in) && in[i+1] == '=' && c != '=' && c != '~' {
				n = 2
			}
			op := in[i : i+n]
			if op == "!" {
				return nil, fmt.Errorf("unexpected %q at %d", op, i)
			}
			out = append(out, tok{s: op})
			i += n
		default:
			j := i
			for j < len(in) && !strings.ContainsRune(" \t\n()\"=!<>~", rune(in[j])) {
				j++
			}
			out = append(out, tok{s: in[i:j]})
			i = j
		}
	}
	return out, nil
}

// unquote reads a "..." string with \" and \\ escapes and returns it and the bytes consumed.
func unquote(in string) (string, int, error) {
	var b strings.Builder
	for i := 1; i < len(in); i++ {
		switch in[i] {
		case '\\':
			if i+1 == len(in) {
				return "", 0, errors.New("unterminated string")
			}
			i++
			b.WriteByte(in[i])
		case '"':
			return b.String(), i + 1, nil
		default:
			b.WriteByte(in[i])
		}
	}
	return "", 0, errors.New("unterminated string")
}

type parser struct {
	toks []tok
	pos  int
}

func parseWhere(in string) (expr, error) {
	toks, err := tokenize(in)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("unexpected %q", p.toks[p.pos].s)
	}
	return e, nil
}

func (p *parser) peekKeyword(kw string) bool {
	return p.pos < len(p.toks) && !p.toks[p.pos].quoted && strings.EqualFold(p.toks[p.pos].s, kw)
}

func (p *parser) or() (expr, error) {
	l, err := p.and()
	for err == nil && p.peekKeyword("OR") {
		p.pos++
		var r expr
		if r, err = p.and(); err == nil {
			l = orExpr{l, r}
		}
	}
	return l, err
}

func (p *parser) and() (expr, error) {
	l, err := p.not()
	for err == nil && p.peekKeyword("AND") {
		p.pos++
		var r expr
		if r, err = p.not(); err == nil {
			l = andExpr{l, r}
		}
	}
	return l, err
}

func (p *parser) not() (expr, error) {
	if p.peekKeyword("NOT") {
		p.pos++
		e, err := p.not()
		return notExpr{e}, err
	}
	if p.pos < len(p.toks) && !p.toks[p.pos].quoted && p.toks[p.pos].s == "(" {
		p.pos++
		e, err := p.or()
		if err != nil {
			return nil, err
		}
		if p.pos >= len(p.toks) || p.toks[p.pos].s != ")" {
			return nil, errors.New("missing )")
		}
		p.pos++
		return e, nil
	}
	return p.cmp()
}

var fieldRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z0-9_-]+)*$`)

func (p *parser) cmp() (expr, error) {
	if p.pos+3 > len(p.toks) {
		return nil, errors.New("expected field op value")
	}
	f, op, v := p.toks[p.pos], p.toks[p.pos+1], p.toks[p.pos+2]
	if f.quoted || !fieldRe.MatchString(f.s) {
		return nil, fmt.Errorf("bad field %q", f.s)
	}
	switch op.s {
	case "=", "!=", ">", ">=", "<", "<=", "~":
	default:
		return nil, fmt.Errorf("bad operator %q after %q", op.s, f.s)
	}
	if op.quoted {
		return nil, fmt.Errorf("bad operator %q after %q", op.s, f.s)
	}
	if !v.quoted && (v.s == "(" || v.s == ")") {
		return nil, fmt.Errorf("missing value after %s%s", f.s, op.s)
	}
	p.pos += 3
	c := cmpExpr{path: strings.Split(f.s, "."), op: op.s, val: v.s}
	if n, err := strconv.ParseFloat(v.s, 64); err == nil && !v.quoted {
		c.num, c.isN = n, true
	}
	switch op.s {
	case "~":
		re, err := regexp.Compile(v.s)
		if err != nil {
			return nil, fmt.Errorf("%s~: %w", f.s, err)
		}
		c.re = re
	case ">", ">=", "<", "<=":
		if !c.isN {
			return nil, fmt.Errorf("%s%s needs a number, got %q", f.s, op.s, v.s)
		}
	}
	return c, nil
}
