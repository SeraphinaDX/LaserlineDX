package invite

import (
	"net"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, a := range []string{"127.0.0.1:7777", "[2001:db8::1]:42", "myhost.example:80"} {
		c := Code{Address: a}
		c.Secret[0] = 1
		c.Fingerprint[0] = 2
		copy(c.Secret[1:], []byte("hello"))
		got, err := Parse(c.String())
		if err != nil {
			t.Fatal(err)
		}
		if got != c {
			t.Fatalf("round trip mismatch: %v", got)
		}
		if _, _, err := net.SplitHostPort(got.Address); err != nil {
			t.Fatal(err)
		}
	}
}
func TestBadCode(t *testing.T) {
	for _, s := range []string{"", "LD1-x", "LD1-invalid!", "LD2-abc", "W1-old-invitation"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
