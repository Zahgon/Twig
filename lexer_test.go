// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import "testing"

func TestTokenizeWhitespaceControl(t *testing.T) {
	stream, err := Tokenize(&Source{Code: "a  {{- x -}}  b\n{% if y %}\nc{% endif %}", Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, tok := range stream.tokens {
		if tok.Type == TextType {
			texts = append(texts, tok.Value.(string))
		}
	}
	want := []string{"a", "b\n", "c"}
	if len(texts) != len(want) {
		t.Fatalf("texts %q, want %q", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("text %d = %q, want %q", i, texts[i], want[i])
		}
	}
}

func TestTokenizeOperators(t *testing.T) {
	stream, err := Tokenize(&Source{Code: "{{ a not in b and c.in starts  with d }}", Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, tok := range stream.tokens {
		if tok.Type == OperatorType {
			ops = append(ops, tok.Value.(string))
		}
	}
	want := []string{"not in", "and", ".", "starts with"}
	if len(ops) != len(want) {
		t.Fatalf("operators %q, want %q", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("operator %d = %q, want %q", i, ops[i], want[i])
		}
	}
}

func TestSyntaxErrorMessage(t *testing.T) {
	_, err := Tokenize(&Source{Code: "{{ a }}\n{{ 'b ", Name: "page.twig"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if got, want := err.Error(), `Unexpected character "'" in "page.twig" at line 2 column 4.`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLevenshteinSuggestions(t *testing.T) {
	e := newSyntaxError(`Unknown "uper" filter.`, 1, &Source{Name: "t"})
	e.addSuggestions("uper", filterNames)
	if got, want := e.Error(), `Unknown "uper" filter. Did you mean "upper" in "t" at line 1?`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
