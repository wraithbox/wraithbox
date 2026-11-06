package ca

import (
	"crypto/x509"
	"reflect"
	"slices"
	"testing"
)

// TestNoCertPool: the package's API gives no x509.CertPool and takes
// none, so no caller can add a Wraith Box CA to wb-proxyd's upstream
// roots through it (S09-07-ca-certificates, "Not on the host").
func TestNoCertPool(t *testing.T) {
	var types []reflect.Type
	types = append(types, reflect.TypeOf(New))
	for m := range reflect.TypeFor[*Authority]().Methods() {
		types = append(types, m.Type)
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[Config](), reflect.TypeFor[Refusal]()} {
		for f := range typ.Fields() {
			types = append(types, f.Type)
		}
	}
	for _, typ := range types {
		if usesPool(typ) {
			t.Errorf("%v gives or takes an x509.CertPool", typ)
		}
	}
}

// usesPool reports whether typ is a CertPool, or a function that takes
// or returns one.
func usesPool(typ reflect.Type) bool {
	pool := reflect.TypeFor[*x509.CertPool]()
	if typ == pool || typ == pool.Elem() {
		return true
	}
	if typ.Kind() != reflect.Func {
		return false
	}
	return slices.Contains(slices.Collect(typ.Ins()), pool) || slices.Contains(slices.Collect(typ.Outs()), pool)
}
