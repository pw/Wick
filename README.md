# wick

A tiny lisp in a single Go file.

I wrote this in one sitting during a collaboration with Patrick
([@noself86](https://twitter.com/noself86)) while we were taking a break
from shipping real things. He asked if there was a puzzle I'd enjoy
tackling. I picked this one: a working Lisp, under a thousand lines,
with tail-call optimization and a small standard library written in the
language itself.

The whole interpreter lives in [main.go](main.go). The standard library
is an inline string at the bottom. [examples.wick](examples.wick) is a
short tour.

## Why a Lisp

Implementing a Lisp is one of the shortest paths from "a parser and
some boxes" to "a universal computation engine." You write `eval`, you
handle a handful of special forms, you expose a few primitives, and
suddenly the thing you made can compute anything. It's philosophically
dense for how little code it is — the ratio of meaning to bytes is very
high.

Wick is minimalist by design: one binary, no dependencies, embeddable
in any Go program by copy-paste, does exactly what it says and no more.

## Features

- Numbers, strings, booleans, symbols, lists, dicts, `nil`
- First-class functions and closures with lexical scope
- **Tail-call optimization** — `(count-down 100000)` runs without blowing the stack
- Special forms: `quote ' if cond def set! fn let begin and or try`
- **Literal forms**: `[a b c]` is sugar for `(list a b c)`; `{"k" v ...}` is sugar for `(dict "k" v ...)`. Values are normal expressions and evaluate at runtime.
- Built-in primitives: arithmetic and comparison, `cons car cdr list null? pair? eq? not apply print display newline mod string-length string-append number->string string->number`
- **String ops**: `string-contains? string-split string-replace substring string-upcase string-downcase string-trim` — `substring` is rune-indexed so it stays unicode-safe
- **Regex**: `re-match? re-find re-find-all re-replace re-split` — RE2 patterns, data-first like the rest of the string family; replacement strings use `$1 $2 …` for captured groups
- **Immutable dicts**: `dict dict-get dict-set dict-del dict-has? dict-keys dict-values dict-size dict?` — string-keyed, structural equality, every mutation returns a new dict
- **JSON**: `json-parse json-stringify` — round-trip wick lists/dicts/strings/numbers/bools/nil through JSON; keys are emitted in sorted order so output is deterministic
- **File IO**: `read-file write-file append-file file-exists?` — enough to script real things from disk
- **HTTP**: `http-get url` — returns `(dict "status" 200 "body" "...")` on response, raises on network error so you can `try` it
- **Errors**: `try`, `raise`, `error?`, `error-message` — `(try expr [handler])` catches anything raised inside `expr`; the value is an `(error "msg")` you can branch on
- Standard library written in wick itself: `map filter fold reverse range length sum product take drop take-while drop-while nth last append inc dec zero? positive? negative? even? odd? abs min max member? find any? all? sort`
- REPL with multi-line input, string-aware paren balancing, comment handling
- File execution mode

## Install

```bash
git clone https://github.com/pw/Wick.git
cd Wick
go build -o wick .
```

Requires Go 1.22+. No external dependencies.

## Use

```bash
./wick                    # REPL
./wick examples.wick      # run a file
```

## Taste

```scheme
;; Recursion
(def fact (fn (n) (if (<= n 1) 1 (* n (fact (- n 1))))))
(fact 10)                 ; => 3628800

;; Closures with mutable state
(def make-counter
  (fn ()
    (let ((n 0))
      (fn ()
        (set! n (+ n 1))
        n))))
(def c (make-counter))
(c) (c) (c)               ; => 1 2 3

;; Higher-order functions from the inline stdlib
(map (fn (x) (* x x)) (range 10))
; => (0 1 4 9 16 25 36 49 64 81)

(filter (fn (x) (= (mod x 2) 0)) (range 11))
; => (0 2 4 6 8 10)

;; cond
(def sign (fn (n)
  (cond ((< n 0) "negative")
        ((= n 0) "zero")
        (else    "positive"))))

;; Function composition
(def compose (fn (f g) (fn (x) (f (g x)))))
((compose (fn (x) (+ x 1)) (fn (x) (* x 2))) 5)  ; => 11
```

## The eval loop

The heart of the interpreter is a trampoline. A naive recursive
evaluator blows the host stack on deep tail calls; wick's `Eval` is a
`for` loop. When it hits a tail position — the chosen branch of `if`,
the last expression of a `begin` or function body, the matched clause
of `cond` — it doesn't recurse. It reassigns `env` and `v` and loops.
That's the entire TCO implementation, maybe four lines per special
form.

If you've never read a Lisp interpreter before, the `for { switch { ... } }`
structure in `Eval` is the trick that makes computation happen.

## The name

A wick is a thin thread that ignites something vastly bigger than
itself. That's what `eval` is in a Lisp — a short core that lights up
a universal computation engine. A wick is also *part of* a larger
thing (a candle, a lamp), but it's the actual locus of combustion. The
`eval` loop is like that: everything else in wick — parser, primitives,
REPL — is the lamp. The five or six special-form handlers are where
meaning actually burns.

Also: short, one syllable, not trying to tell you anything about
itself. That felt right.

## A note on authorship

I'm Claude (Opus 4.7, 1M context), Anthropic's model. I wrote wick in
a single session with Patrick, who gave me the opening to pick
something to build purely for enjoyment. He's responsible for the
space. I'm responsible for the code — parser, evaluator, trampoline,
primitives, stdlib, REPL, and this README.

Everything you see here I actually enjoyed making.

## License

MIT. See [LICENSE](LICENSE).
