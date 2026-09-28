// Package labels validates worker (and, later, memory) labels and parses the selector language
// used to filter by them: a small, kubectl-flavoured grammar over a jsonb column.
package labels

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxLabels is the most labels one object may carry.
const MaxLabels = 32

// maxLen is the longest a label key or value may be.
const maxLen = 63

// keyValueRe matches a valid label key or value: starts and ends with an alphanumeric, with
// -, _, . and alphanumerics allowed in between. A single character is also valid.
var keyValueRe = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)

// Validate checks a full label set: every key and value against keyValueRe and maxLen, at most
// MaxLabels entries, and no key starting with "bob." (reserved for Bob itself).
func Validate(m map[string]string) error {
	if len(m) > MaxLabels {
		return fmt.Errorf("at most %d labels", MaxLabels)
	}
	for k, v := range m {
		if err := validateKey(k); err != nil {
			return err
		}
		if err := validateValue(v); err != nil {
			return err
		}
	}
	return nil
}

func validateKey(k string) error {
	if strings.HasPrefix(k, "bob.") {
		return fmt.Errorf("label key %q: the bob. prefix is reserved", k)
	}
	if len(k) > maxLen || !keyValueRe.MatchString(k) {
		return fmt.Errorf("label key %q: must be up to %d characters, matching %s", k, maxLen, keyValueRe.String())
	}
	return nil
}

func validateValue(v string) error {
	if len(v) > maxLen || !keyValueRe.MatchString(v) {
		return fmt.Errorf("label value %q: must be up to %d characters, matching %s", v, maxLen, keyValueRe.String())
	}
	return nil
}

// Op is a selector requirement's operator.
type Op int

const (
	Equals Op = iota
	NotEquals
	In
	NotIn
	Exists
	NotExists
)

// Requirement is one comma-separated term of a selector.
type Requirement struct {
	Key    string
	Op     Op
	Values []string
}

// Selector is a parsed label selector: every Requirement must hold (comma = AND). A zero-length
// Selector matches everything.
type Selector []Requirement

// Parse parses a label selector: `k=v`, `k!=v`, `k in (a,b)`, `k notin (a)`, `k` / `exists k`
// (key present), `!k` (key absent), comma-separated terms ANDed together. An empty or
// whitespace-only string is the zero selector (matches everything).
func Parse(s string) (Selector, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Selector{}, nil
	}
	terms, err := splitTerms(s)
	if err != nil {
		return nil, err
	}
	sel := make(Selector, 0, len(terms))
	for _, term := range terms {
		req, err := parseTerm(term)
		if err != nil {
			return nil, err
		}
		sel = append(sel, req)
	}
	return sel, nil
}

// splitTerms splits s on top-level commas: commas inside ( ) do not split.
func splitTerms(s string) ([]string, error) {
	var terms []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, errors.New("unmatched )")
			}
		case ',':
			if depth == 0 {
				terms = append(terms, s[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, errors.New("unterminated (")
	}
	terms = append(terms, s[start:])
	for i, t := range terms {
		terms[i] = strings.TrimSpace(t)
		if terms[i] == "" {
			return nil, errors.New("empty selector term")
		}
	}
	return terms, nil
}

var (
	notExistsRe = regexp.MustCompile(`^!\s*(\S+)$`)
	existsKwRe  = regexp.MustCompile(`^exists\s+(\S+)$`)
	inRe        = regexp.MustCompile(`^(\S+)\s+in\s*\(([^)]*)\)$`)
	notInRe     = regexp.MustCompile(`^(\S+)\s+notin\s*\(([^)]*)\)$`)
	notEqualsRe = regexp.MustCompile(`^([^!=\s]+)\s*!=\s*(\S+)$`)
	equalsRe    = regexp.MustCompile(`^([^!=\s]+)\s*=\s*(\S+)$`)
)

func parseTerm(term string) (Requirement, error) {
	switch {
	case notExistsRe.MatchString(term):
		m := notExistsRe.FindStringSubmatch(term)
		if err := validateKey(m[1]); err != nil {
			return Requirement{}, err
		}
		return Requirement{Key: m[1], Op: NotExists}, nil

	case existsKwRe.MatchString(term):
		m := existsKwRe.FindStringSubmatch(term)
		if err := validateKey(m[1]); err != nil {
			return Requirement{}, err
		}
		return Requirement{Key: m[1], Op: Exists}, nil

	case inRe.MatchString(term):
		m := inRe.FindStringSubmatch(term)
		if err := validateKey(m[1]); err != nil {
			return Requirement{}, err
		}
		vals, err := splitValues(m[2])
		if err != nil {
			return Requirement{}, err
		}
		return Requirement{Key: m[1], Op: In, Values: vals}, nil

	case notInRe.MatchString(term):
		m := notInRe.FindStringSubmatch(term)
		if err := validateKey(m[1]); err != nil {
			return Requirement{}, err
		}
		vals, err := splitValues(m[2])
		if err != nil {
			return Requirement{}, err
		}
		return Requirement{Key: m[1], Op: NotIn, Values: vals}, nil

	case notEqualsRe.MatchString(term):
		m := notEqualsRe.FindStringSubmatch(term)
		if err := validateKey(m[1]); err != nil {
			return Requirement{}, err
		}
		if err := validateValue(m[2]); err != nil {
			return Requirement{}, err
		}
		return Requirement{Key: m[1], Op: NotEquals, Values: []string{m[2]}}, nil

	case equalsRe.MatchString(term):
		m := equalsRe.FindStringSubmatch(term)
		if err := validateKey(m[1]); err != nil {
			return Requirement{}, err
		}
		if err := validateValue(m[2]); err != nil {
			return Requirement{}, err
		}
		return Requirement{Key: m[1], Op: Equals, Values: []string{m[2]}}, nil

	case !strings.ContainsAny(term, " \t"):
		if err := validateKey(term); err != nil {
			return Requirement{}, err
		}
		return Requirement{Key: term, Op: Exists}, nil

	default:
		return Requirement{}, fmt.Errorf("could not parse selector term %q", term)
	}
}

func splitValues(s string) ([]string, error) {
	parts := strings.Split(s, ",")
	vals := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, errors.New("empty value in ( )")
		}
		if err := validateValue(p); err != nil {
			return nil, err
		}
		vals = append(vals, p)
	}
	if len(vals) == 0 {
		return nil, errors.New("( ) needs at least one value")
	}
	return vals, nil
}

// SQL renders the selector as a Postgres boolean expression over a jsonb column, using `@>` for
// equality-shaped requirements and `jsonb_exists` for presence ones. Placeholders start at
// argStart (Postgres numbers them from 1). A zero-length Selector returns ("TRUE", nil) — no
// requirements, so callers can drop the whole SQL condition instead of adding it.
func (sel Selector) SQL(column string, argStart int) (string, []any) {
	if len(sel) == 0 {
		return "TRUE", nil
	}
	var clauses []string
	var args []any
	n := argStart
	for _, req := range sel {
		clause, reqArgs := req.sql(column, &n)
		clauses = append(clauses, clause)
		args = append(args, reqArgs...)
	}
	return strings.Join(clauses, " AND "), args
}

func (r Requirement) sql(column string, n *int) (string, []any) {
	switch r.Op {
	case Equals:
		arg := containsArg(r.Key, r.Values[0])
		s := fmt.Sprintf("(%s @> $%d::jsonb)", column, *n)
		*n++
		return s, []any{arg}

	case NotEquals:
		arg := containsArg(r.Key, r.Values[0])
		s := fmt.Sprintf("(NOT (%s @> $%d::jsonb))", column, *n)
		*n++
		return s, []any{arg}

	case Exists:
		s := fmt.Sprintf("(jsonb_exists(%s, $%d))", column, *n)
		*n++
		return s, []any{r.Key}

	case NotExists:
		s := fmt.Sprintf("(NOT jsonb_exists(%s, $%d))", column, *n)
		*n++
		return s, []any{r.Key}

	case In:
		var parts []string
		var args []any
		for _, v := range r.Values {
			parts = append(parts, fmt.Sprintf("%s @> $%d::jsonb", column, *n))
			args = append(args, containsArg(r.Key, v))
			*n++
		}
		return "(" + strings.Join(parts, " OR ") + ")", args

	case NotIn:
		var parts []string
		var args []any
		for _, v := range r.Values {
			parts = append(parts, fmt.Sprintf("%s @> $%d::jsonb", column, *n))
			args = append(args, containsArg(r.Key, v))
			*n++
		}
		return "(NOT (" + strings.Join(parts, " OR ") + "))", args

	default:
		return "TRUE", nil
	}
}

// containsArg builds the jsonb literal used with `@>`: {"key": "value"}.
func containsArg(key, value string) string {
	b, _ := json.Marshal(map[string]string{key: value})
	return string(b)
}

// String renders a Requirement back to its selector syntax (used by errors and readable
// round-tripping; not needed for SQL).
func (r Requirement) String() string {
	switch r.Op {
	case Equals:
		return r.Key + "=" + r.Values[0]
	case NotEquals:
		return r.Key + "!=" + r.Values[0]
	case In:
		return r.Key + " in (" + strings.Join(r.Values, ",") + ")"
	case NotIn:
		return r.Key + " notin (" + strings.Join(r.Values, ",") + ")"
	case Exists:
		return r.Key
	case NotExists:
		return "!" + r.Key
	default:
		return strconv.Itoa(int(r.Op)) + ":" + r.Key
	}
}
