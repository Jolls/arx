//go:build integration

package main

import (
	"reflect"

	"github.com/shopspring/decimal"
)

var decimalType = reflect.TypeOf(decimal.Decimal{})

// deepEqualDec is reflect.DeepEqual that compares decimal.Decimal values by
// numeric value (.Equal) instead of by internal representation. Unexported
// struct fields are ignored.
func deepEqualDec(a, b any) bool {
	return deepEqualDecV(reflect.ValueOf(a), reflect.ValueOf(b))
}

func deepEqualDecV(a, b reflect.Value) bool {
	if a.IsValid() != b.IsValid() {
		return false
	}
	if !a.IsValid() {
		return true
	}
	if a.Type() != b.Type() {
		return false
	}
	if a.Type() == decimalType {
		return a.Interface().(decimal.Decimal).Equal(b.Interface().(decimal.Decimal))
	}
	switch a.Kind() {
	case reflect.Ptr, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return deepEqualDecV(a.Elem(), b.Elem())
	case reflect.Slice:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !deepEqualDecV(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Array:
		for i := 0; i < a.Len(); i++ {
			if !deepEqualDecV(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		for _, k := range a.MapKeys() {
			bv := b.MapIndex(k)
			if !bv.IsValid() || !deepEqualDecV(a.MapIndex(k), bv) {
				return false
			}
		}
		return true
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if !a.Type().Field(i).IsExported() {
				continue
			}
			if !deepEqualDecV(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	}
	if a.CanInterface() && b.CanInterface() {
		return reflect.DeepEqual(a.Interface(), b.Interface())
	}
	return true
}
