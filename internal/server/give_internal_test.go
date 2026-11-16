package server

import (
	"reflect"
	"testing"

	"github.com/Ivlad003/edgehop/internal/keys"
)

// Keys typed into a window of the server that is shown on a client travel
// there and back; the swap the server applies on the way out is undone.
func TestInverseKeys(t *testing.T) {
	if got := inverseKeys(nil); got != nil {
		t.Errorf("no remapping: %v", got)
	}
	swap := map[uint16]uint16{keys.LeftAlt: keys.LeftMeta, keys.LeftMeta: keys.LeftAlt}
	if got := inverseKeys(swap); !reflect.DeepEqual(got, swap) {
		t.Errorf("a swap is its own inverse, got %v", got)
	}
	oneWay := map[uint16]uint16{keys.LeftMeta: keys.LeftCtrl}
	if got, want := inverseKeys(oneWay), (map[uint16]uint16{keys.LeftCtrl: keys.LeftMeta}); !reflect.DeepEqual(got, want) {
		t.Errorf("one key sent as another: got %v, want %v", got, want)
	}
	// Two keys sent as the same one: there is no telling which it was.
	both := map[uint16]uint16{keys.LeftMeta: keys.LeftCtrl, keys.RightMeta: keys.LeftCtrl, keys.A: keys.B}
	if got, want := inverseKeys(both), (map[uint16]uint16{keys.B: keys.A}); !reflect.DeepEqual(got, want) {
		t.Errorf("two keys sent as one: got %v, want %v", got, want)
	}
}
