// Package invite encodes the address, TLS certificate fingerprint and a bearer
// capability into one pasteable invitation. No external rendezvous is required.
package invite

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
)

const Prefix = "LD1-"

type Code struct {
	Address     string
	Fingerprint [32]byte
	Secret      [16]byte
}

func (c Code) String() string {
	addr := []byte(c.Address)
	if len(addr) > 255 {
		return ""
	}
	data := make([]byte, 0, 1+len(addr)+32+16)
	data = append(data, byte(len(addr)))
	data = append(data, addr...)
	data = append(data, c.Fingerprint[:]...)
	data = append(data, c.Secret[:]...)
	return Prefix + base64.RawURLEncoding.EncodeToString(data)
}

func Parse(input string) (Code, error) {
	var c Code
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, Prefix) {
		return c, errors.New("invalid invitation prefix (expected LD1-)")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(input, Prefix))
	if err != nil || len(data) < 1 {
		return c, errors.New("invalid invitation encoding")
	}
	n := int(data[0])
	if n == 0 || len(data) != 1+n+32+16 {
		return c, errors.New("invalid invitation length")
	}
	c.Address = string(data[1 : 1+n])
	if _, _, err := net.SplitHostPort(c.Address); err != nil {
		return Code{}, fmt.Errorf("invalid invitation address: %w", err)
	}
	copy(c.Fingerprint[:], data[1+n:1+n+32])
	copy(c.Secret[:], data[1+n+32:])
	var empty [16]byte
	if subtle.ConstantTimeCompare(c.Secret[:], empty[:]) == 1 {
		return Code{}, errors.New("empty access secret")
	}
	return c, nil
}
