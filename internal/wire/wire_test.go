package wire

import (
	"bytes"
	"testing"
)

func TestFramingLeavesBinaryIntact(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, Message{Type: "start", Offset: 7}); err != nil {
		t.Fatal(err)
	}
	b.WriteString("filebytes")
	msg, err := Read(&b)
	if err != nil || msg.Offset != 7 {
		t.Fatalf("%+v %v", msg, err)
	}
	if b.String() != "filebytes" {
		t.Errorf("consumed binary: %q", b.String())
	}
}
