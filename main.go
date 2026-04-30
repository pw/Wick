// wick — a tiny lisp in a single file.
//
// Supports: numbers, strings, booleans, symbols, lists, closures with lexical
// scope, recursion with tail-call optimization, quote / quasiquote-less quote,
// and a small stdlib written in wick itself.
//
// Special forms: quote ' if cond def set! fn let begin and or try
// Primitives: arithmetic, comparison, cons car cdr list null? pair? eq? not
//             apply mod string-length string-append number->string string->number
//             string-contains? string-split string-replace substring
//             string-upcase string-downcase string-trim
//             re-match? re-find re-find-all re-replace re-split
//             print display newline
//             dict dict-get dict-set dict-del dict-has? dict-keys dict-values
//             dict-size dict?
//             json-parse json-stringify
//             read-file write-file append-file file-exists?
//             http-get http-post
//             error? error-message raise
// Stdlib (written in wick): map filter fold reverse range length sum product
//             take nth drop last append inc dec zero? positive? negative? even? odd?
//             abs min max member? sort
//
// Run: `wick` for REPL, `wick file.wick` to execute a file.

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- Values ----------

type Value interface{ String() string }

type Num float64
type Str string
type Sym string
type Nil struct{}
type Bool bool
type List []Value

func (n Num) String() string {
	f := float64(n)
	if f == float64(int64(f)) && f > -1e18 && f < 1e18 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
func (s Str) String() string { return strconv.Quote(string(s)) }
func (s Sym) String() string { return string(s) }
func (Nil) String() string   { return "nil" }
func (b Bool) String() string {
	if b {
		return "#t"
	}
	return "#f"
}
func (l List) String() string {
	parts := make([]string, len(l))
	for i, v := range l {
		parts[i] = v.String()
	}
	return "(" + strings.Join(parts, " ") + ")"
}

type Dict struct {
	m map[string]Value
}

func (d *Dict) String() string {
	keys := make([]string, 0, len(d.m))
	for k := range d.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		parts = append(parts, strconv.Quote(k), d.m[k].String())
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func dictKey(v Value) (string, error) {
	switch x := v.(type) {
	case Str:
		return string(x), nil
	case Sym:
		return string(x), nil
	}
	return "", fmt.Errorf("dict key must be string or symbol, got %s", v)
}

type Fn struct {
	params []Sym
	body   List
	env    *Env
}

func (f *Fn) String() string { return "#<fn>" }

type Builtin struct {
	name string
	f    func(args []Value) (Value, error)
}

func (b *Builtin) String() string { return "#<builtin " + b.name + ">" }

type Err struct{ msg string }

func (e *Err) String() string { return "(error " + strconv.Quote(e.msg) + ")" }

// ---------- Environment ----------

type Env struct {
	parent *Env
	vars   map[Sym]Value
}

func NewEnv(parent *Env) *Env { return &Env{parent: parent, vars: map[Sym]Value{}} }

func (e *Env) Lookup(s Sym) (Value, error) {
	for env := e; env != nil; env = env.parent {
		if v, ok := env.vars[s]; ok {
			return v, nil
		}
	}
	return nil, fmt.Errorf("unbound: %s", s)
}

func (e *Env) Set(s Sym, v Value) { e.vars[s] = v }

func (e *Env) SetExisting(s Sym, v Value) error {
	for env := e; env != nil; env = env.parent {
		if _, ok := env.vars[s]; ok {
			env.vars[s] = v
			return nil
		}
	}
	return fmt.Errorf("set!: unbound: %s", s)
}

// ---------- Tokenizer + Parser ----------

func tokenize(src string) []string {
	var toks []string
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == ';':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '(' || c == ')' || c == '[' || c == ']' || c == '{' || c == '}' || c == '\'':
			toks = append(toks, string(c))
			i++
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' && j+1 < len(src) {
					j += 2
					continue
				}
				j++
			}
			if j >= len(src) {
				toks = append(toks, src[i:])
				return toks
			}
			toks = append(toks, src[i:j+1])
			i = j + 1
		default:
			j := i
			for j < len(src) && !strings.ContainsRune(" \t\n\r()[]{}';\"", rune(src[j])) {
				j++
			}
			toks = append(toks, src[i:j])
			i = j
		}
	}
	return toks
}

type reader struct {
	toks []string
	pos  int
}

func (r *reader) peek() (string, bool) {
	if r.pos >= len(r.toks) {
		return "", false
	}
	return r.toks[r.pos], true
}
func (r *reader) next() (string, bool) {
	t, ok := r.peek()
	if ok {
		r.pos++
	}
	return t, ok
}

func (r *reader) read() (Value, error) {
	t, ok := r.next()
	if !ok {
		return nil, io.EOF
	}
	switch t {
	case "(":
		var list List
		for {
			tk, ok := r.peek()
			if !ok {
				return nil, errors.New("unclosed '('")
			}
			if tk == ")" {
				r.next()
				return list, nil
			}
			v, err := r.read()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
	case ")":
		return nil, errors.New("unexpected ')'")
	case "[":
		// [a b c] desugars to (list a b c). Empty [] is (list).
		out := List{Sym("list")}
		for {
			tk, ok := r.peek()
			if !ok {
				return nil, errors.New("unclosed '['")
			}
			if tk == "]" {
				r.next()
				return out, nil
			}
			v, err := r.read()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
	case "]":
		return nil, errors.New("unexpected ']'")
	case "{":
		// {"k" v ...} desugars to (dict "k" v ...). Empty {} is (dict).
		out := List{Sym("dict")}
		for {
			tk, ok := r.peek()
			if !ok {
				return nil, errors.New("unclosed '{'")
			}
			if tk == "}" {
				r.next()
				return out, nil
			}
			v, err := r.read()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
	case "}":
		return nil, errors.New("unexpected '}'")
	case "'":
		v, err := r.read()
		if err != nil {
			return nil, err
		}
		return List{Sym("quote"), v}, nil
	default:
		return atom(t), nil
	}
}

func atom(t string) Value {
	switch t {
	case "#t":
		return Bool(true)
	case "#f":
		return Bool(false)
	case "nil":
		return Nil{}
	}
	if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
		if s, err := strconv.Unquote(t); err == nil {
			return Str(s)
		}
		return Str(t[1 : len(t)-1])
	}
	if n, err := strconv.ParseFloat(t, 64); err == nil {
		return Num(n)
	}
	return Sym(t)
}

func ParseAll(src string) ([]Value, error) {
	r := &reader{toks: tokenize(src)}
	var out []Value
	for {
		v, err := r.read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

// ---------- Evaluator (trampoline-style for TCO) ----------

func Eval(v Value, env *Env) (Value, error) {
	for {
		switch x := v.(type) {
		case Num, Str, Bool, Nil, *Fn, *Builtin, *Dict, *Err:
			return x, nil
		case Sym:
			return env.Lookup(x)
		case List:
			if len(x) == 0 {
				return Nil{}, nil
			}
			if sym, ok := x[0].(Sym); ok {
				switch sym {
				case "quote":
					if len(x) != 2 {
						return nil, errors.New("quote: need 1 arg")
					}
					return x[1], nil
				case "if":
					if len(x) < 3 || len(x) > 4 {
						return nil, errors.New("if: need 2 or 3 args")
					}
					cond, err := Eval(x[1], env)
					if err != nil {
						return nil, err
					}
					if truthy(cond) {
						v = x[2]
						continue
					}
					if len(x) == 4 {
						v = x[3]
						continue
					}
					return Nil{}, nil
				case "cond":
					matched := false
					for _, cl := range x[1:] {
						clause, ok := cl.(List)
						if !ok || len(clause) == 0 {
							return nil, errors.New("cond: clause must be non-empty list")
						}
						var c Value = Bool(false)
						if s, isSym := clause[0].(Sym); isSym && s == "else" {
							c = Bool(true)
						} else {
							r, err := Eval(clause[0], env)
							if err != nil {
								return nil, err
							}
							c = r
						}
						if truthy(c) {
							body := clause[1:]
							if len(body) == 0 {
								return c, nil
							}
							for i := 0; i < len(body)-1; i++ {
								if _, err := Eval(body[i], env); err != nil {
									return nil, err
								}
							}
							v = body[len(body)-1]
							matched = true
							break
						}
					}
					if matched {
						continue
					}
					return Nil{}, nil
				case "def":
					if len(x) != 3 {
						return nil, errors.New("def: need name + value")
					}
					name, ok := x[1].(Sym)
					if !ok {
						return nil, errors.New("def: name must be symbol")
					}
					val, err := Eval(x[2], env)
					if err != nil {
						return nil, err
					}
					env.Set(name, val)
					return val, nil
				case "set!":
					if len(x) != 3 {
						return nil, errors.New("set!: need name + value")
					}
					name, ok := x[1].(Sym)
					if !ok {
						return nil, errors.New("set!: name must be symbol")
					}
					val, err := Eval(x[2], env)
					if err != nil {
						return nil, err
					}
					if err := env.SetExisting(name, val); err != nil {
						return nil, err
					}
					return val, nil
				case "fn":
					if len(x) < 3 {
						return nil, errors.New("fn: need params + body")
					}
					plist, ok := x[1].(List)
					if !ok {
						return nil, errors.New("fn: params must be list")
					}
					params := make([]Sym, len(plist))
					for i, p := range plist {
						s, ok := p.(Sym)
						if !ok {
							return nil, errors.New("fn: params must be symbols")
						}
						params[i] = s
					}
					return &Fn{params: params, body: List(x[2:]), env: env}, nil
				case "let":
					if len(x) < 3 {
						return nil, errors.New("let: need bindings + body")
					}
					bindings, ok := x[1].(List)
					if !ok {
						return nil, errors.New("let: bindings must be list")
					}
					sub := NewEnv(env)
					for _, b := range bindings {
						bl, ok := b.(List)
						if !ok || len(bl) != 2 {
							return nil, errors.New("let: each binding must be (name value)")
						}
						name, ok := bl[0].(Sym)
						if !ok {
							return nil, errors.New("let: binding name must be symbol")
						}
						val, err := Eval(bl[1], env)
						if err != nil {
							return nil, err
						}
						sub.Set(name, val)
					}
					body := x[2:]
					for i := 0; i < len(body)-1; i++ {
						if _, err := Eval(body[i], sub); err != nil {
							return nil, err
						}
					}
					env = sub
					v = body[len(body)-1]
					continue
				case "begin":
					body := x[1:]
					if len(body) == 0 {
						return Nil{}, nil
					}
					for i := 0; i < len(body)-1; i++ {
						if _, err := Eval(body[i], env); err != nil {
							return nil, err
						}
					}
					v = body[len(body)-1]
					continue
				case "and":
					var last Value = Bool(true)
					for _, a := range x[1:] {
						r, err := Eval(a, env)
						if err != nil {
							return nil, err
						}
						if !truthy(r) {
							return r, nil
						}
						last = r
					}
					return last, nil
				case "or":
					for _, a := range x[1:] {
						r, err := Eval(a, env)
						if err != nil {
							return nil, err
						}
						if truthy(r) {
							return r, nil
						}
					}
					return Bool(false), nil
				case "try":
					if len(x) < 2 || len(x) > 3 {
						return nil, errors.New("try: need 1 or 2 args (expr [handler])")
					}
					result, evalErr := Eval(x[1], env)
					if evalErr == nil {
						return result, nil
					}
					errVal := &Err{msg: evalErr.Error()}
					if len(x) == 2 {
						return errVal, nil
					}
					handler, herr := Eval(x[2], env)
					if herr != nil {
						return nil, herr
					}
					switch h := handler.(type) {
					case *Builtin:
						return h.f([]Value{errVal})
					case *Fn:
						if len(h.params) != 1 {
							return nil, fmt.Errorf("try: handler must take 1 arg, got %d", len(h.params))
						}
						sub := NewEnv(h.env)
						sub.Set(h.params[0], errVal)
						for i := 0; i < len(h.body)-1; i++ {
							if _, err := Eval(h.body[i], sub); err != nil {
								return nil, err
							}
						}
						v = h.body[len(h.body)-1]
						env = sub
						continue
					default:
						return nil, fmt.Errorf("try: handler not callable: %s", handler)
					}
				}
			}
			// function application
			fn, err := Eval(x[0], env)
			if err != nil {
				return nil, err
			}
			args := make([]Value, len(x)-1)
			for i, a := range x[1:] {
				args[i], err = Eval(a, env)
				if err != nil {
					return nil, err
				}
			}
			switch f := fn.(type) {
			case *Builtin:
				return f.f(args)
			case *Fn:
				if len(args) != len(f.params) {
					return nil, fmt.Errorf("arity: need %d, got %d", len(f.params), len(args))
				}
				sub := NewEnv(f.env)
				for i, p := range f.params {
					sub.Set(p, args[i])
				}
				for i := 0; i < len(f.body)-1; i++ {
					if _, err := Eval(f.body[i], sub); err != nil {
						return nil, err
					}
				}
				env = sub
				v = f.body[len(f.body)-1]
				continue
			default:
				return nil, fmt.Errorf("not callable: %s", fn)
			}
		default:
			return nil, fmt.Errorf("unknown: %T", v)
		}
	}
}

func truthy(v Value) bool {
	switch x := v.(type) {
	case Bool:
		return bool(x)
	case Nil:
		return false
	default:
		return true
	}
}

// ---------- JSON conversion ----------

func jsonToValue(v interface{}) (Value, error) {
	switch x := v.(type) {
	case nil:
		return Nil{}, nil
	case bool:
		return Bool(x), nil
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return nil, fmt.Errorf("json-parse: bad number %s: %v", x, err)
		}
		return Num(f), nil
	case float64:
		return Num(x), nil
	case string:
		return Str(x), nil
	case []interface{}:
		out := make(List, len(x))
		for i, e := range x {
			ev, err := jsonToValue(e)
			if err != nil {
				return nil, err
			}
			out[i] = ev
		}
		return out, nil
	case map[string]interface{}:
		m := make(map[string]Value, len(x))
		for k, e := range x {
			ev, err := jsonToValue(e)
			if err != nil {
				return nil, err
			}
			m[k] = ev
		}
		return &Dict{m: m}, nil
	}
	return nil, fmt.Errorf("json-parse: unexpected JSON type %T", v)
}

func valueToJSON(v Value) (interface{}, error) {
	switch x := v.(type) {
	case Nil:
		return nil, nil
	case Bool:
		return bool(x), nil
	case Num:
		return float64(x), nil
	case Str:
		return string(x), nil
	case List:
		out := make([]interface{}, len(x))
		for i, e := range x {
			r, err := valueToJSON(e)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case *Dict:
		m := make(map[string]interface{}, len(x.m))
		for k, e := range x.m {
			r, err := valueToJSON(e)
			if err != nil {
				return nil, err
			}
			m[k] = r
		}
		return m, nil
	}
	return nil, fmt.Errorf("json-stringify: cannot serialize %s", v)
}

// ---------- Primitives ----------

func defaultEnv() *Env {
	env := NewEnv(nil)
	reduce := func(name string, op func(a, b float64) float64) *Builtin {
		return &Builtin{name: name, f: func(args []Value) (Value, error) {
			if len(args) == 0 {
				return nil, errors.New(name + ": need at least 1 arg")
			}
			acc, ok := args[0].(Num)
			if !ok {
				return nil, fmt.Errorf("%s: need number, got %s", name, args[0])
			}
			if len(args) == 1 && (name == "-" || name == "/") {
				if name == "-" {
					return Num(-float64(acc)), nil
				}
				return Num(1 / float64(acc)), nil
			}
			for _, a := range args[1:] {
				b, ok := a.(Num)
				if !ok {
					return nil, fmt.Errorf("%s: need number, got %s", name, a)
				}
				acc = Num(op(float64(acc), float64(b)))
			}
			return acc, nil
		}}
	}
	cmp := func(name string, op func(a, b float64) bool) *Builtin {
		return &Builtin{name: name, f: func(args []Value) (Value, error) {
			if len(args) < 2 {
				return Bool(true), nil
			}
			for i := 0; i < len(args)-1; i++ {
				a, ok := args[i].(Num)
				if !ok {
					return nil, fmt.Errorf("%s: need number", name)
				}
				b, ok := args[i+1].(Num)
				if !ok {
					return nil, fmt.Errorf("%s: need number", name)
				}
				if !op(float64(a), float64(b)) {
					return Bool(false), nil
				}
			}
			return Bool(true), nil
		}}
	}

	env.Set("+", reduce("+", func(a, b float64) float64 { return a + b }))
	env.Set("-", reduce("-", func(a, b float64) float64 { return a - b }))
	env.Set("*", reduce("*", func(a, b float64) float64 { return a * b }))
	env.Set("/", reduce("/", func(a, b float64) float64 { return a / b }))
	env.Set("<", cmp("<", func(a, b float64) bool { return a < b }))
	env.Set(">", cmp(">", func(a, b float64) bool { return a > b }))
	env.Set("<=", cmp("<=", func(a, b float64) bool { return a <= b }))
	env.Set(">=", cmp(">=", func(a, b float64) bool { return a >= b }))
	env.Set("=", cmp("=", func(a, b float64) bool { return a == b }))
	env.Set("mod", &Builtin{name: "mod", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("mod: need 2 args")
		}
		a, ok := args[0].(Num)
		if !ok {
			return nil, errors.New("mod: need numbers")
		}
		b, ok := args[1].(Num)
		if !ok {
			return nil, errors.New("mod: need numbers")
		}
		ai, bi := int64(a), int64(b)
		if bi == 0 {
			return nil, errors.New("mod: div by zero")
		}
		return Num(float64(ai % bi)), nil
	}})

	env.Set("list", &Builtin{name: "list", f: func(args []Value) (Value, error) {
		return List(append([]Value{}, args...)), nil
	}})
	env.Set("cons", &Builtin{name: "cons", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("cons: need 2 args")
		}
		if tail, ok := args[1].(List); ok {
			out := make(List, 0, len(tail)+1)
			out = append(out, args[0])
			out = append(out, tail...)
			return out, nil
		}
		if _, isNil := args[1].(Nil); isNil {
			return List{args[0]}, nil
		}
		return nil, errors.New("cons: second arg must be list or nil")
	}})
	env.Set("car", &Builtin{name: "car", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("car: need 1 arg")
		}
		l, ok := args[0].(List)
		if !ok || len(l) == 0 {
			return nil, errors.New("car: empty or not list")
		}
		return l[0], nil
	}})
	env.Set("cdr", &Builtin{name: "cdr", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("cdr: need 1 arg")
		}
		l, ok := args[0].(List)
		if !ok || len(l) == 0 {
			return nil, errors.New("cdr: empty or not list")
		}
		return l[1:], nil
	}})
	env.Set("null?", &Builtin{name: "null?", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("null?: need 1 arg")
		}
		if _, ok := args[0].(Nil); ok {
			return Bool(true), nil
		}
		if l, ok := args[0].(List); ok {
			return Bool(len(l) == 0), nil
		}
		return Bool(false), nil
	}})
	env.Set("pair?", &Builtin{name: "pair?", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("pair?: need 1 arg")
		}
		l, ok := args[0].(List)
		return Bool(ok && len(l) > 0), nil
	}})
	env.Set("eq?", &Builtin{name: "eq?", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("eq?: need 2 args")
		}
		return Bool(equals(args[0], args[1])), nil
	}})
	env.Set("not", &Builtin{name: "not", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("not: need 1 arg")
		}
		return Bool(!truthy(args[0])), nil
	}})
	env.Set("print", &Builtin{name: "print", f: func(args []Value) (Value, error) {
		parts := make([]string, len(args))
		for i, a := range args {
			if s, ok := a.(Str); ok {
				parts[i] = string(s)
			} else {
				parts[i] = a.String()
			}
		}
		fmt.Println(strings.Join(parts, " "))
		return Nil{}, nil
	}})
	env.Set("display", &Builtin{name: "display", f: func(args []Value) (Value, error) {
		for _, a := range args {
			if s, ok := a.(Str); ok {
				fmt.Print(string(s))
			} else {
				fmt.Print(a.String())
			}
		}
		return Nil{}, nil
	}})
	env.Set("newline", &Builtin{name: "newline", f: func(args []Value) (Value, error) {
		fmt.Println()
		return Nil{}, nil
	}})
	env.Set("apply", &Builtin{name: "apply", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("apply: need 2 args (fn args-list)")
		}
		fn := args[0]
		var callArgs []Value
		switch a := args[1].(type) {
		case List:
			callArgs = []Value(a)
		case Nil:
			callArgs = nil
		default:
			return nil, errors.New("apply: second arg must be list")
		}
		switch f := fn.(type) {
		case *Builtin:
			return f.f(callArgs)
		case *Fn:
			if len(callArgs) != len(f.params) {
				return nil, fmt.Errorf("arity: need %d, got %d", len(f.params), len(callArgs))
			}
			sub := NewEnv(f.env)
			for i, p := range f.params {
				sub.Set(p, callArgs[i])
			}
			var last Value = Nil{}
			for _, b := range f.body {
				r, err := Eval(b, sub)
				if err != nil {
					return nil, err
				}
				last = r
			}
			return last, nil
		default:
			return nil, fmt.Errorf("apply: not callable: %s", fn)
		}
	}})
	env.Set("string-length", &Builtin{name: "string-length", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("string-length: need 1 arg")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("string-length: need string, got %s", args[0])
		}
		return Num(len([]rune(string(s)))), nil
	}})
	env.Set("string-append", &Builtin{name: "string-append", f: func(args []Value) (Value, error) {
		var b strings.Builder
		for _, a := range args {
			s, ok := a.(Str)
			if !ok {
				return nil, fmt.Errorf("string-append: need string, got %s", a)
			}
			b.WriteString(string(s))
		}
		return Str(b.String()), nil
	}})
	env.Set("number->string", &Builtin{name: "number->string", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("number->string: need 1 arg")
		}
		n, ok := args[0].(Num)
		if !ok {
			return nil, fmt.Errorf("number->string: need number, got %s", args[0])
		}
		return Str(n.String()), nil
	}})
	env.Set("string->number", &Builtin{name: "string->number", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("string->number: need 1 arg")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("string->number: need string, got %s", args[0])
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(string(s)), 64)
		if err != nil {
			return Nil{}, nil
		}
		return Num(f), nil
	}})
	env.Set("string-contains?", &Builtin{name: "string-contains?", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("string-contains?: need 2 args (haystack needle)")
		}
		hay, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("string-contains?: need string, got %s", args[0])
		}
		needle, ok := args[1].(Str)
		if !ok {
			return nil, fmt.Errorf("string-contains?: need string, got %s", args[1])
		}
		return Bool(strings.Contains(string(hay), string(needle))), nil
	}})
	env.Set("string-split", &Builtin{name: "string-split", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("string-split: need 2 args (string delimiter)")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("string-split: need string, got %s", args[0])
		}
		delim, ok := args[1].(Str)
		if !ok {
			return nil, fmt.Errorf("string-split: need string, got %s", args[1])
		}
		parts := strings.Split(string(s), string(delim))
		out := make(List, len(parts))
		for i, p := range parts {
			out[i] = Str(p)
		}
		return out, nil
	}})
	env.Set("string-replace", &Builtin{name: "string-replace", f: func(args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, errors.New("string-replace: need 3 args (string old new)")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("string-replace: need string, got %s", args[0])
		}
		old, ok := args[1].(Str)
		if !ok {
			return nil, fmt.Errorf("string-replace: need string, got %s", args[1])
		}
		new_, ok := args[2].(Str)
		if !ok {
			return nil, fmt.Errorf("string-replace: need string, got %s", args[2])
		}
		return Str(strings.ReplaceAll(string(s), string(old), string(new_))), nil
	}})
	env.Set("substring", &Builtin{name: "substring", f: func(args []Value) (Value, error) {
		if len(args) != 2 && len(args) != 3 {
			return nil, errors.New("substring: need 2 or 3 args (string start [end])")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("substring: need string, got %s", args[0])
		}
		start, ok := args[1].(Num)
		if !ok {
			return nil, fmt.Errorf("substring: need number for start, got %s", args[1])
		}
		runes := []rune(string(s))
		startIdx := int(start)
		if startIdx < 0 {
			startIdx = 0
		}
		if startIdx > len(runes) {
			startIdx = len(runes)
		}
		endIdx := len(runes)
		if len(args) == 3 {
			end, ok := args[2].(Num)
			if !ok {
				return nil, fmt.Errorf("substring: need number for end, got %s", args[2])
			}
			endIdx = int(end)
			if endIdx < startIdx {
				endIdx = startIdx
			}
			if endIdx > len(runes) {
				endIdx = len(runes)
			}
		}
		return Str(string(runes[startIdx:endIdx])), nil
	}})
	env.Set("string-upcase", &Builtin{name: "string-upcase", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("string-upcase: need 1 arg")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("string-upcase: need string, got %s", args[0])
		}
		return Str(strings.ToUpper(string(s))), nil
	}})
	env.Set("string-downcase", &Builtin{name: "string-downcase", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("string-downcase: need 1 arg")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("string-downcase: need string, got %s", args[0])
		}
		return Str(strings.ToLower(string(s))), nil
	}})
	env.Set("string-trim", &Builtin{name: "string-trim", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("string-trim: need 1 arg")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("string-trim: need string, got %s", args[0])
		}
		return Str(strings.TrimSpace(string(s))), nil
	}})

	// ---------- Regex ----------
	// Patterns are Go's RE2 syntax. Data-first (string, pattern) to match the
	// rest of the string family. Replacement strings use $1, $2, ... for
	// captured groups (Go's regexp.ReplaceAllString convention).
	reCompile := func(name string, p Value) (*regexp.Regexp, error) {
		s, ok := p.(Str)
		if !ok {
			return nil, fmt.Errorf("%s: need string for pattern, got %s", name, p)
		}
		re, err := regexp.Compile(string(s))
		if err != nil {
			return nil, fmt.Errorf("%s: bad pattern %q: %v", name, string(s), err)
		}
		return re, nil
	}
	env.Set("re-match?", &Builtin{name: "re-match?", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("re-match?: need 2 args (string pattern)")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("re-match?: need string, got %s", args[0])
		}
		re, err := reCompile("re-match?", args[1])
		if err != nil {
			return nil, err
		}
		return Bool(re.MatchString(string(s))), nil
	}})
	env.Set("re-find", &Builtin{name: "re-find", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("re-find: need 2 args (string pattern)")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("re-find: need string, got %s", args[0])
		}
		re, err := reCompile("re-find", args[1])
		if err != nil {
			return nil, err
		}
		loc := re.FindStringIndex(string(s))
		if loc == nil {
			return Nil{}, nil
		}
		return Str(string(s)[loc[0]:loc[1]]), nil
	}})
	env.Set("re-find-all", &Builtin{name: "re-find-all", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("re-find-all: need 2 args (string pattern)")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("re-find-all: need string, got %s", args[0])
		}
		re, err := reCompile("re-find-all", args[1])
		if err != nil {
			return nil, err
		}
		matches := re.FindAllString(string(s), -1)
		out := make(List, len(matches))
		for i, m := range matches {
			out[i] = Str(m)
		}
		return out, nil
	}})
	env.Set("re-replace", &Builtin{name: "re-replace", f: func(args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, errors.New("re-replace: need 3 args (string pattern replacement)")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("re-replace: need string, got %s", args[0])
		}
		re, err := reCompile("re-replace", args[1])
		if err != nil {
			return nil, err
		}
		repl, ok := args[2].(Str)
		if !ok {
			return nil, fmt.Errorf("re-replace: need string for replacement, got %s", args[2])
		}
		return Str(re.ReplaceAllString(string(s), string(repl))), nil
	}})
	env.Set("re-split", &Builtin{name: "re-split", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("re-split: need 2 args (string pattern)")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("re-split: need string, got %s", args[0])
		}
		re, err := reCompile("re-split", args[1])
		if err != nil {
			return nil, err
		}
		parts := re.Split(string(s), -1)
		out := make(List, len(parts))
		for i, p := range parts {
			out[i] = Str(p)
		}
		return out, nil
	}})

	// ---------- Dicts ----------
	env.Set("dict", &Builtin{name: "dict", f: func(args []Value) (Value, error) {
		if len(args)%2 != 0 {
			return nil, errors.New("dict: need even number of args (key value pairs)")
		}
		m := make(map[string]Value, len(args)/2)
		for i := 0; i < len(args); i += 2 {
			k, err := dictKey(args[i])
			if err != nil {
				return nil, err
			}
			m[k] = args[i+1]
		}
		return &Dict{m: m}, nil
	}})
	env.Set("dict?", &Builtin{name: "dict?", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("dict?: need 1 arg")
		}
		_, ok := args[0].(*Dict)
		return Bool(ok), nil
	}})
	env.Set("dict-get", &Builtin{name: "dict-get", f: func(args []Value) (Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, errors.New("dict-get: need 2 or 3 args (dict key [default])")
		}
		d, ok := args[0].(*Dict)
		if !ok {
			return nil, fmt.Errorf("dict-get: need dict, got %s", args[0])
		}
		k, err := dictKey(args[1])
		if err != nil {
			return nil, err
		}
		if v, ok := d.m[k]; ok {
			return v, nil
		}
		if len(args) == 3 {
			return args[2], nil
		}
		return Nil{}, nil
	}})
	env.Set("dict-set", &Builtin{name: "dict-set", f: func(args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, errors.New("dict-set: need 3 args (dict key value)")
		}
		d, ok := args[0].(*Dict)
		if !ok {
			return nil, fmt.Errorf("dict-set: need dict, got %s", args[0])
		}
		k, err := dictKey(args[1])
		if err != nil {
			return nil, err
		}
		m := make(map[string]Value, len(d.m)+1)
		for kk, vv := range d.m {
			m[kk] = vv
		}
		m[k] = args[2]
		return &Dict{m: m}, nil
	}})
	env.Set("dict-del", &Builtin{name: "dict-del", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("dict-del: need 2 args (dict key)")
		}
		d, ok := args[0].(*Dict)
		if !ok {
			return nil, fmt.Errorf("dict-del: need dict, got %s", args[0])
		}
		k, err := dictKey(args[1])
		if err != nil {
			return nil, err
		}
		m := make(map[string]Value, len(d.m))
		for kk, vv := range d.m {
			if kk != k {
				m[kk] = vv
			}
		}
		return &Dict{m: m}, nil
	}})
	env.Set("dict-has?", &Builtin{name: "dict-has?", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("dict-has?: need 2 args (dict key)")
		}
		d, ok := args[0].(*Dict)
		if !ok {
			return nil, fmt.Errorf("dict-has?: need dict, got %s", args[0])
		}
		k, err := dictKey(args[1])
		if err != nil {
			return nil, err
		}
		_, ok = d.m[k]
		return Bool(ok), nil
	}})
	env.Set("dict-keys", &Builtin{name: "dict-keys", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("dict-keys: need 1 arg")
		}
		d, ok := args[0].(*Dict)
		if !ok {
			return nil, fmt.Errorf("dict-keys: need dict, got %s", args[0])
		}
		keys := make([]string, 0, len(d.m))
		for k := range d.m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(List, len(keys))
		for i, k := range keys {
			out[i] = Str(k)
		}
		return out, nil
	}})
	env.Set("dict-values", &Builtin{name: "dict-values", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("dict-values: need 1 arg")
		}
		d, ok := args[0].(*Dict)
		if !ok {
			return nil, fmt.Errorf("dict-values: need dict, got %s", args[0])
		}
		keys := make([]string, 0, len(d.m))
		for k := range d.m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(List, len(keys))
		for i, k := range keys {
			out[i] = d.m[k]
		}
		return out, nil
	}})
	env.Set("dict-size", &Builtin{name: "dict-size", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("dict-size: need 1 arg")
		}
		d, ok := args[0].(*Dict)
		if !ok {
			return nil, fmt.Errorf("dict-size: need dict, got %s", args[0])
		}
		return Num(len(d.m)), nil
	}})

	// ---------- JSON ----------
	env.Set("json-parse", &Builtin{name: "json-parse", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("json-parse: need 1 arg")
		}
		s, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("json-parse: need string, got %s", args[0])
		}
		dec := json.NewDecoder(strings.NewReader(string(s)))
		dec.UseNumber()
		var raw interface{}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("json-parse: %v", err)
		}
		return jsonToValue(raw)
	}})
	env.Set("json-stringify", &Builtin{name: "json-stringify", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("json-stringify: need 1 arg")
		}
		raw, err := valueToJSON(args[0])
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("json-stringify: %v", err)
		}
		return Str(string(b)), nil
	}})

	// ---------- File IO ----------
	env.Set("read-file", &Builtin{name: "read-file", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("read-file: need 1 arg (path)")
		}
		path, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("read-file: need string path, got %s", args[0])
		}
		data, err := os.ReadFile(string(path))
		if err != nil {
			return nil, fmt.Errorf("read-file: %v", err)
		}
		return Str(string(data)), nil
	}})
	env.Set("write-file", &Builtin{name: "write-file", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("write-file: need 2 args (path content)")
		}
		path, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("write-file: need string path, got %s", args[0])
		}
		content, ok := args[1].(Str)
		if !ok {
			return nil, fmt.Errorf("write-file: need string content, got %s", args[1])
		}
		if err := os.WriteFile(string(path), []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("write-file: %v", err)
		}
		return Bool(true), nil
	}})
	env.Set("append-file", &Builtin{name: "append-file", f: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, errors.New("append-file: need 2 args (path content)")
		}
		path, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("append-file: need string path, got %s", args[0])
		}
		content, ok := args[1].(Str)
		if !ok {
			return nil, fmt.Errorf("append-file: need string content, got %s", args[1])
		}
		f, err := os.OpenFile(string(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("append-file: %v", err)
		}
		defer f.Close()
		if _, err := f.WriteString(string(content)); err != nil {
			return nil, fmt.Errorf("append-file: %v", err)
		}
		return Bool(true), nil
	}})
	env.Set("file-exists?", &Builtin{name: "file-exists?", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("file-exists?: need 1 arg (path)")
		}
		path, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("file-exists?: need string path, got %s", args[0])
		}
		_, err := os.Stat(string(path))
		return Bool(err == nil), nil
	}})
	env.Set("list-dir", &Builtin{name: "list-dir", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("list-dir: need 1 arg (path)")
		}
		path, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("list-dir: need string path, got %s", args[0])
		}
		entries, err := os.ReadDir(string(path))
		if err != nil {
			return nil, fmt.Errorf("list-dir: %v", err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		out := make(List, len(names))
		for i, n := range names {
			out[i] = Str(n)
		}
		return out, nil
	}})

	// ---------- HTTP ----------
	httpClient := &http.Client{Timeout: 10 * time.Second}
	applyHeaders := func(req *http.Request, h Value, name string) error {
		d, ok := h.(*Dict)
		if !ok {
			return fmt.Errorf("%s: headers must be a dict, got %s", name, h)
		}
		for k, v := range d.m {
			s, ok := v.(Str)
			if !ok {
				return fmt.Errorf("%s: header value for %q must be a string, got %s", name, k, v)
			}
			req.Header.Set(k, string(s))
		}
		return nil
	}
	responseDict := func(resp *http.Response) (Value, error) {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		headers := &Dict{m: map[string]Value{}}
		for k, vs := range resp.Header {
			headers.m[k] = Str(strings.Join(vs, ", "))
		}
		return &Dict{m: map[string]Value{
			"status":  Num(resp.StatusCode),
			"body":    Str(string(body)),
			"headers": headers,
		}}, nil
	}
	env.Set("http-get", &Builtin{name: "http-get", f: func(args []Value) (Value, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, errors.New("http-get: need 1 or 2 args (url [headers])")
		}
		url, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("http-get: need string url, got %s", args[0])
		}
		req, err := http.NewRequest("GET", string(url), nil)
		if err != nil {
			return nil, fmt.Errorf("http-get: %v", err)
		}
		req.Header.Set("User-Agent", "wick/0.1")
		if len(args) == 2 {
			if err := applyHeaders(req, args[1], "http-get"); err != nil {
				return nil, err
			}
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("http-get: %v", err)
		}
		defer resp.Body.Close()
		out, err := responseDict(resp)
		if err != nil {
			return nil, fmt.Errorf("http-get: %v", err)
		}
		return out, nil
	}})
	env.Set("http-post", &Builtin{name: "http-post", f: func(args []Value) (Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, errors.New("http-post: need 2 or 3 args (url body [headers])")
		}
		url, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("http-post: need string url, got %s", args[0])
		}
		body, ok := args[1].(Str)
		if !ok {
			return nil, fmt.Errorf("http-post: need string body, got %s", args[1])
		}
		req, err := http.NewRequest("POST", string(url), strings.NewReader(string(body)))
		if err != nil {
			return nil, fmt.Errorf("http-post: %v", err)
		}
		req.Header.Set("User-Agent", "wick/0.1")
		if len(args) == 3 {
			if err := applyHeaders(req, args[2], "http-post"); err != nil {
				return nil, err
			}
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("http-post: %v", err)
		}
		defer resp.Body.Close()
		out, err := responseDict(resp)
		if err != nil {
			return nil, fmt.Errorf("http-post: %v", err)
		}
		return out, nil
	}})

	// ---------- Errors ----------
	env.Set("error?", &Builtin{name: "error?", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("error?: need 1 arg")
		}
		_, ok := args[0].(*Err)
		return Bool(ok), nil
	}})
	env.Set("error-message", &Builtin{name: "error-message", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("error-message: need 1 arg")
		}
		e, ok := args[0].(*Err)
		if !ok {
			return nil, fmt.Errorf("error-message: need error, got %s", args[0])
		}
		return Str(e.msg), nil
	}})
	env.Set("raise", &Builtin{name: "raise", f: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, errors.New("raise: need 1 arg (message)")
		}
		msg, ok := args[0].(Str)
		if !ok {
			return nil, fmt.Errorf("raise: need string, got %s", args[0])
		}
		return nil, errors.New(string(msg))
	}})

	return env
}

func equals(a, b Value) bool {
	switch x := a.(type) {
	case Num:
		y, ok := b.(Num)
		return ok && x == y
	case Str:
		y, ok := b.(Str)
		return ok && x == y
	case Sym:
		y, ok := b.(Sym)
		return ok && x == y
	case Bool:
		y, ok := b.(Bool)
		return ok && x == y
	case Nil:
		_, ok := b.(Nil)
		return ok
	case List:
		y, ok := b.(List)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equals(x[i], y[i]) {
				return false
			}
		}
		return true
	case *Dict:
		y, ok := b.(*Dict)
		if !ok || len(x.m) != len(y.m) {
			return false
		}
		for k, v := range x.m {
			yv, ok := y.m[k]
			if !ok || !equals(v, yv) {
				return false
			}
		}
		return true
	case *Err:
		y, ok := b.(*Err)
		return ok && x.msg == y.msg
	}
	return false
}

// ---------- Stdlib (written in wick) ----------

const stdlib = `
(def length (fn (xs)
  (if (null? xs) 0 (+ 1 (length (cdr xs))))))

(def map (fn (f xs)
  (if (null? xs) '()
      (cons (f (car xs)) (map f (cdr xs))))))

(def filter (fn (pred xs)
  (if (null? xs) '()
      (if (pred (car xs))
          (cons (car xs) (filter pred (cdr xs)))
          (filter pred (cdr xs))))))

(def fold (fn (f init xs)
  (if (null? xs) init
      (fold f (f init (car xs)) (cdr xs)))))

(def reverse (fn (xs)
  (fold (fn (acc x) (cons x acc)) '() xs)))

(def sum     (fn (xs) (fold + 0 xs)))
(def product (fn (xs) (fold * 1 xs)))

(def range-helper (fn (i acc)
  (if (< i 0) acc (range-helper (- i 1) (cons i acc)))))
(def range (fn (n) (range-helper (- n 1) '())))

(def take (fn (n xs)
  (if (or (= n 0) (null? xs)) '()
      (cons (car xs) (take (- n 1) (cdr xs))))))

(def nth (fn (n xs)
  (if (= n 0) (car xs) (nth (- n 1) (cdr xs)))))

(def drop (fn (n xs)
  (if (or (= n 0) (null? xs)) xs
      (drop (- n 1) (cdr xs)))))

(def take-while (fn (pred xs)
  (if (null? xs) '()
      (if (pred (car xs))
          (cons (car xs) (take-while pred (cdr xs)))
          '()))))

(def drop-while (fn (pred xs)
  (if (null? xs) '()
      (if (pred (car xs))
          (drop-while pred (cdr xs))
          xs))))

(def last (fn (xs)
  (if (null? (cdr xs)) (car xs) (last (cdr xs)))))

(def append (fn (xs ys)
  (if (null? xs) ys
      (cons (car xs) (append (cdr xs) ys)))))

(def inc (fn (n) (+ n 1)))
(def dec (fn (n) (- n 1)))
(def zero?     (fn (n) (= n 0)))
(def positive? (fn (n) (> n 0)))
(def negative? (fn (n) (< n 0)))
(def even? (fn (n) (= (mod n 2) 0)))
(def odd?  (fn (n) (= (mod n 2) 1)))

(def abs (fn (n) (if (< n 0) (- 0 n) n)))

(def min (fn (xs)
  (fold (fn (a b) (if (< a b) a b)) (car xs) (cdr xs))))
(def max (fn (xs)
  (fold (fn (a b) (if (> a b) a b)) (car xs) (cdr xs))))

(def member? (fn (x xs)
  (if (null? xs) #f
      (if (eq? x (car xs)) #t (member? x (cdr xs))))))

(def find (fn (pred xs)
  (if (null? xs) #f
      (if (pred (car xs)) (car xs)
          (find pred (cdr xs))))))

(def any? (fn (pred xs)
  (if (null? xs) #f
      (if (pred (car xs)) #t
          (any? pred (cdr xs))))))

(def all? (fn (pred xs)
  (if (null? xs) #t
      (if (pred (car xs)) (all? pred (cdr xs))
          #f))))

(def sort (fn (cmp xs)
  (if (or (null? xs) (null? (cdr xs))) xs
      (let ((pivot (car xs))
            (rest  (cdr xs)))
        (append
          (sort cmp (filter (fn (y) (cmp y pivot)) rest))
          (cons pivot
            (sort cmp (filter (fn (y) (not (cmp y pivot))) rest))))))))
`

// ---------- REPL ----------

func runSource(src string, env *Env) (Value, error) {
	vals, err := ParseAll(src)
	if err != nil {
		return nil, err
	}
	var last Value = Nil{}
	for _, v := range vals {
		last, err = Eval(v, env)
		if err != nil {
			return nil, err
		}
	}
	return last, nil
}

func repl(env *Env) {
	fmt.Println("wick — a tiny lisp. try (map (fn (x) (* x x)) (range 10))")
	fmt.Println("       ctrl-D to exit.")
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	var buf strings.Builder
	depth := 0
	inStr := false
	showPrompt := func() {
		if depth == 0 && !inStr {
			fmt.Print("> ")
		} else {
			fmt.Print("  ")
		}
	}
	showPrompt()
	for sc.Scan() {
		line := sc.Text()
		for i := 0; i < len(line); i++ {
			c := line[i]
			if c == '"' && (i == 0 || line[i-1] != '\\') {
				inStr = !inStr
			}
			if inStr {
				continue
			}
			if c == ';' {
				break
			}
			if c == '(' || c == '[' || c == '{' {
				depth++
			}
			if c == ')' || c == ']' || c == '}' {
				depth--
			}
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
		if depth <= 0 && !inStr {
			src := buf.String()
			buf.Reset()
			depth = 0
			val, err := runSource(src, env)
			if err != nil {
				fmt.Println("err:", err)
			} else if _, isNil := val.(Nil); !isNil {
				fmt.Println(val)
			}
		}
		showPrompt()
	}
	fmt.Println()
}

func main() {
	env := defaultEnv()
	if _, err := runSource(stdlib, env); err != nil {
		fmt.Fprintln(os.Stderr, "stdlib failed:", err)
		os.Exit(1)
	}
	if len(os.Args) > 1 {
		data, err := os.ReadFile(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if _, err := runSource(string(data), env); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	repl(env)
}
