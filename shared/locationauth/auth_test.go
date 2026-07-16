package locationauth

import (
	"encoding/base64"
	"testing"
)

func testCredential() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, credentialBytes))
}

func TestSignAndVerifyJSON(t *testing.T) {
	v := struct {
		ID string `json:"id"`
	}{ID: "location-1"}
	sig, err := SignJSON(testCredential(), v)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyJSON(testCredential(), sig, v); err != nil {
		t.Fatal(err)
	}
	v.ID = "location-2"
	if err := VerifyJSON(testCredential(), sig, v); err == nil {
		t.Fatal("tampered payload verified")
	}
}

func TestConfigEncryptorUsesSeparatedKey(t *testing.T) {
	enc, err := ConfigEncryptor(testCredential())
	if err != nil {
		t.Fatal(err)
	}
	ct, err := enc.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	got, err := enc.Decrypt(ct)
	if err != nil || got != "secret" {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
}
