package main

import "testing"

func TestResolveAddr(t *testing.T) {
	for _, tc := range []struct {
		name         string
		addrExplicit bool
		addr         string
		port         string
		want         string
	}{
		{name: "implicit addr with PORT takes PORT", addrExplicit: false, addr: defaultAddr, port: "8080", want: ":8080"},
		{name: "implicit addr without PORT keeps default", addrExplicit: false, addr: defaultAddr, port: "", want: defaultAddr},
		{name: "implicit addr with blank PORT keeps default", addrExplicit: false, addr: defaultAddr, port: "  ", want: defaultAddr},
		{name: "explicit addr beats PORT", addrExplicit: true, addr: "127.0.0.1:9000", port: "8080", want: "127.0.0.1:9000"},
		{name: "explicit addr on default value still beats PORT", addrExplicit: true, addr: defaultAddr, port: "8080", want: defaultAddr},
		{name: "PORT is trimmed", addrExplicit: false, addr: defaultAddr, port: " 8080\n", want: ":8080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveAddr(tc.addrExplicit, tc.addr, tc.port); got != tc.want {
				t.Fatalf("resolveAddr(%v, %q, %q) = %q, want %q", tc.addrExplicit, tc.addr, tc.port, got, tc.want)
			}
		})
	}
}
