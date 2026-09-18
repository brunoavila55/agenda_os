package store

import (
	"reflect"
	"testing"
)

func TestConnectedComponents(t *testing.T) {
	ids := []string{"d", "b", "a", "c", "isolated"}
	edges := [][2]string{{"a", "b"}, {"b", "c"}, {"outside", "d"}}
	want := [][]string{{"a", "b", "c"}, {"d"}, {"isolated"}}
	if got := connectedComponents(ids, edges); !reflect.DeepEqual(got, want) {
		t.Fatalf("connectedComponents() = %#v, want %#v", got, want)
	}
}

func TestConnectedComponentsEmpty(t *testing.T) {
	if got := connectedComponents(nil, nil); len(got) != 0 {
		t.Fatalf("connectedComponents() = %#v", got)
	}
}
