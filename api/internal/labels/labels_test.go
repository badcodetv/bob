package labels

import (
	"reflect"
	"testing"
)

func TestValidateLabels(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		ok     bool
	}{
		{"empty", map[string]string{}, true},
		{"nil", nil, true},
		{"good key and value", map[string]string{"kind": "hypothesis"}, true},
		{"dashes dots underscores", map[string]string{"a.b-c_d": "x.y-z_1"}, true},
		{"single char", map[string]string{"a": "b"}, true},
		{"empty key", map[string]string{"": "v"}, false},
		{"empty value rejected by regex", map[string]string{"k": ""}, false},
		{"key starts with dash", map[string]string{"-k": "v"}, false},
		{"key ends with dash", map[string]string{"k-": "v"}, false},
		{"value with space", map[string]string{"k": "a b"}, false},
		{"key too long", map[string]string{repeat("a", 64): "v"}, false},
		{"key exactly 63", map[string]string{repeat("a", 63): "v"}, true},
		{"value too long", map[string]string{"k": repeat("a", 64)}, false},
		{"reserved bob prefix", map[string]string{"bob.kind": "v"}, false},
		{"reserved bob exact prefix boundary", map[string]string{"bobby": "v"}, true},
		{"too many labels", manyLabels(33), false},
		{"exactly 32 labels", manyLabels(32), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.labels)
			if c.ok && err != nil {
				t.Fatalf("expected valid, got error: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected error, got none")
			}
		})
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func manyLabels(n int) map[string]string {
	m := make(map[string]string, n)
	for i := 0; i < n; i++ {
		m[repeat("k", 1)+itoa(i)] = "v"
	}
	return m
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestParse(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  Selector
		isErr bool
	}{
		{"empty is zero selector", "", Selector{}, false},
		{"whitespace only", "   ", Selector{}, false},
		{"equals", "kind=hypothesis", Selector{{Key: "kind", Op: Equals, Values: []string{"hypothesis"}}}, false},
		{"not equals", "kind!=hypothesis", Selector{{Key: "kind", Op: NotEquals, Values: []string{"hypothesis"}}}, false},
		{"in", "kind in (a,b)", Selector{{Key: "kind", Op: In, Values: []string{"a", "b"}}}, false},
		{"in no spaces", "kind in (a, b, c)", Selector{{Key: "kind", Op: In, Values: []string{"a", "b", "c"}}}, false},
		{"notin", "kind notin (a)", Selector{{Key: "kind", Op: NotIn, Values: []string{"a"}}}, false},
		{"bare key exists", "kind", Selector{{Key: "kind", Op: Exists}}, false},
		{"exists keyword", "exists kind", Selector{{Key: "kind", Op: Exists}}, false},
		{"bang not exists", "!kind", Selector{{Key: "kind", Op: NotExists}}, false},
		{"comma is and", "kind=hypothesis,status=active", Selector{
			{Key: "kind", Op: Equals, Values: []string{"hypothesis"}},
			{Key: "status", Op: Equals, Values: []string{"active"}},
		}, false},
		{"in with internal comma not split wrongly", "kind in (a,b),status=active", Selector{
			{Key: "kind", Op: In, Values: []string{"a", "b"}},
			{Key: "status", Op: Equals, Values: []string{"active"}},
		}, false},
		{"invalid key", "bad key=v", nil, true},
		{"reserved bob key", "bob.kind=v", nil, true},
		{"unterminated paren", "kind in (a,b", nil, true},
		{"empty term", "kind=a,,status=b", nil, true},
		{"bad value", "kind=a b", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.in)
			if c.isErr {
				if err == nil {
					t.Fatalf("expected error, got selector %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestSelectorSQL(t *testing.T) {
	t.Run("zero selector matches all", func(t *testing.T) {
		sel := Selector{}
		sql, args := sel.SQL("labels", 1)
		if sql != "TRUE" {
			t.Fatalf("got sql %q", sql)
		}
		if len(args) != 0 {
			t.Fatalf("got args %+v", args)
		}
	})

	t.Run("equals uses containment", func(t *testing.T) {
		sel, err := Parse("kind=hypothesis")
		if err != nil {
			t.Fatal(err)
		}
		sql, args := sel.SQL("labels", 1)
		if sql != `(labels @> $1::jsonb)` {
			t.Fatalf("got sql %q", sql)
		}
		if len(args) != 1 {
			t.Fatalf("got args %+v", args)
		}
	})

	t.Run("not equals negates containment", func(t *testing.T) {
		sel, err := Parse("kind!=hypothesis")
		if err != nil {
			t.Fatal(err)
		}
		sql, _ := sel.SQL("labels", 1)
		if sql != `(NOT (labels @> $1::jsonb))` {
			t.Fatalf("got sql %q", sql)
		}
	})

	t.Run("exists uses jsonb_exists", func(t *testing.T) {
		sel, err := Parse("kind")
		if err != nil {
			t.Fatal(err)
		}
		sql, args := sel.SQL("labels", 1)
		if sql != `(jsonb_exists(labels, $1))` {
			t.Fatalf("got sql %q", sql)
		}
		if args[0] != "kind" {
			t.Fatalf("got args %+v", args)
		}
	})

	t.Run("not exists negates jsonb_exists", func(t *testing.T) {
		sel, err := Parse("!kind")
		if err != nil {
			t.Fatal(err)
		}
		sql, _ := sel.SQL("labels", 1)
		if sql != `(NOT jsonb_exists(labels, $1))` {
			t.Fatalf("got sql %q", sql)
		}
	})

	t.Run("in ors containment per value", func(t *testing.T) {
		sel, err := Parse("kind in (a,b)")
		if err != nil {
			t.Fatal(err)
		}
		sql, args := sel.SQL("labels", 1)
		if sql != `(labels @> $1::jsonb OR labels @> $2::jsonb)` {
			t.Fatalf("got sql %q", sql)
		}
		if len(args) != 2 {
			t.Fatalf("got args %+v", args)
		}
	})

	t.Run("notin negates the or", func(t *testing.T) {
		sel, err := Parse("kind notin (a)")
		if err != nil {
			t.Fatal(err)
		}
		sql, args := sel.SQL("labels", 1)
		if sql != `(NOT (labels @> $1::jsonb))` {
			t.Fatalf("got sql %q", sql)
		}
		if len(args) != 1 {
			t.Fatalf("got args %+v", args)
		}
	})

	t.Run("multiple requirements are anded, args numbered from argStart", func(t *testing.T) {
		sel, err := Parse("kind=hypothesis,status")
		if err != nil {
			t.Fatal(err)
		}
		sql, args := sel.SQL("labels", 3)
		if sql != `(labels @> $3::jsonb) AND (jsonb_exists(labels, $4))` {
			t.Fatalf("got sql %q", sql)
		}
		if len(args) != 2 {
			t.Fatalf("got args %+v", args)
		}
	})
}
