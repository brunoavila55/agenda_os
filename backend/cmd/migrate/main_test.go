package main

import "testing"

func TestTransactionBody(t *testing.T) {
	want := "CREATE TABLE example (id integer);"
	for _, input := range []string{
		"BEGIN;\n" + want + "\nCOMMIT;\n",
		"  begin;\n" + want + "\ncommit;  ",
		want,
	} {
		if got := transactionBody(input); got != want {
			t.Errorf("transactionBody() = %q, want %q", got, want)
		}
	}
}
