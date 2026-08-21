package main

import "testing"

func TestValidateControlAddrRequiresDirectTLSControlPort(t *testing.T) {
	for _, value := range []string{":18443", "0.0.0.0:18443", "[::1]:18443"} {
		if err := validateControlAddr(value); err != nil {
			t.Fatalf("expected valid Direct control address %q: %v", value, err)
		}
	}
	for _, value := range []string{":18442", "127.0.0.1:443", "missing-port"} {
		if err := validateControlAddr(value); err == nil {
			t.Fatalf("expected invalid Direct control address %q", value)
		}
	}
}
