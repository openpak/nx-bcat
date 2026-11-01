package byml

import (
	"reflect"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	in := Hash{"Version": int32(26), "Name": "x", "On": true, "F": float32(1.5), "U": uint32(7), "N": nil,
		"List": []any{int32(1), "a", Hash{"k": "v"}, []any{}}}
	b, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip\n in %v\nout %v", in, out)
	}
}
