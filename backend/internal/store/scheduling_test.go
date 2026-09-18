package store

import "testing"

func TestEqualOptionalInt64(t *testing.T) {
	one, anotherOne, two := int64(1), int64(1), int64(2)
	for _, test := range []struct {
		name string
		a, b *int64
		want bool
	}{
		{"both nil", nil, nil, true},
		{"one nil", &one, nil, false},
		{"equal", &one, &anotherOne, true},
		{"different", &one, &two, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := equalOptionalInt64(test.a, test.b); got != test.want {
				t.Fatalf("equalOptionalInt64() = %v, want %v", got, test.want)
			}
		})
	}
}
