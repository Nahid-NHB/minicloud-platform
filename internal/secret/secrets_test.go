package secret

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	key, _ := GenerateKey()
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := c.Encrypt([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := c.Decrypt(ct, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, []byte("hello")) {
		t.Fatalf("got %s", pt)
	}
}

func TestWrongKeyFails(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()
	c1, _ := NewCipher(key1)
	c2, _ := NewCipher(key2)
	ct, nonce, _ := c1.Encrypt([]byte("x"))
	if _, err := c2.Decrypt(ct, nonce); err == nil {
		t.Fatal("expected decrypt failure")
	}
}
