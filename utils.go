package qjs

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func Min(a, b int) int {
	if a < b {
		return a
	}

	return b
}

func IsImplementError(rtype reflect.Type) bool {
	return rtype.Implements(reflect.TypeOf((*error)(nil)).Elem())
}

// IsImplementsJSONUnmarshaler checks if a type implements json.Unmarshaler.
func IsImplementsJSONUnmarshaler(rtype reflect.Type) bool {
	unmarshalerType := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

	return rtype.Implements(unmarshalerType) || reflect.PointerTo(rtype).Implements(unmarshalerType)
}

// GetGoTypeName creates a descriptive string for complex types.
func GetGoTypeName(input any) string {
	var targetType reflect.Type
	switch v := input.(type) {
	case reflect.Type:
		targetType = v
	default:
		targetType = reflect.TypeOf(v)
	}

	switch targetType.Kind() {
	case reflect.Ptr:
		return "*" + GetGoTypeName(targetType.Elem())
	case reflect.Slice:
		return "[]" + GetGoTypeName(targetType.Elem())
	case reflect.Array:
		return fmt.Sprintf("[%d]%s", targetType.Len(), GetGoTypeName(targetType.Elem()))
	case reflect.Map:
		return fmt.Sprintf("map[%s]%s", GetGoTypeName(targetType.Key()), GetGoTypeName(targetType.Elem()))
	case reflect.Chan:
		return "chan " + GetGoTypeName(targetType.Elem())
	case reflect.Func:
		return CreateGoFuncSignature(targetType)
	default:
		return targetType.String()
	}
}

// CreateGoFuncSignature creates a readable string for function types.
func CreateGoFuncSignature(fnType reflect.Type) string {
	parts := []string{"func("}
	params := []string{}

	for i := range fnType.NumIn() {
		paramType := fnType.In(i)
		if fnType.IsVariadic() && i == fnType.NumIn()-1 {
			params = append(params, "..."+GetGoTypeName(paramType.Elem()))
		} else {
			params = append(params, GetGoTypeName(paramType))
		}
	}

	parts = append(parts, strings.Join(params, ", "))
	parts = append(parts, ")")

	if fnType.NumOut() > 0 {
		parts = append(parts, " ")
		if fnType.NumOut() > 1 {
			parts = append(parts, "(")
		}

		returns := []string{}
		for i := range fnType.NumOut() {
			returns = append(returns, GetGoTypeName(fnType.Out(i)))
		}

		parts = append(parts, strings.Join(returns, ", "))
		if fnType.NumOut() > 1 {
			parts = append(parts, ")")
		}
	}

	return strings.Join(parts, "")
}

// IsConvertibleToJs checks if a Go type can be converted to a JavaScript type.
func IsConvertibleToJs(rtype reflect.Type, visited map[reflect.Type]bool, detail string) (err error) {
	// Prevent infinite recursion for recursive types
	if visited[rtype] {
		return nil
	}

	visited[rtype] = true
	if rtype.Kind() == reflect.Ptr {
		return IsConvertibleToJs(rtype.Elem(), visited, detail)
	}

	switch rtype.Kind() {
	case reflect.Chan:
		return nil
	case reflect.UnsafePointer:
		return newGoToJsErr("unsafe.Pointer", nil, detail)
	case reflect.Func:
		return nil
	case reflect.Slice:
		err := IsConvertibleToJs(rtype.Elem(), visited, detail)
		if err != nil {
			return newGoToJsErr("slice: "+GetGoTypeName(rtype.Elem()), nil, detail)
		}

		return nil
	case reflect.Array:
		err := IsConvertibleToJs(rtype.Elem(), visited, detail)
		if err != nil {
			return newGoToJsErr("array: "+GetGoTypeName(rtype.Elem()), nil, detail)
		}

		return nil
	case reflect.Map:
		keyType := rtype.Key()

		err := IsConvertibleToJs(keyType, visited, detail)
		if err != nil {
			return newGoToJsErr("map key: "+GetGoTypeName(keyType), nil, detail)
		}

		valueType := rtype.Elem()
		if err := IsConvertibleToJs(valueType, visited, detail); err != nil {
			return newGoToJsErr("map value: "+GetGoTypeName(valueType), nil, detail)
		}

		return nil
	case reflect.Struct:
		for i := range rtype.NumField() {
			field := rtype.Field(i)
			jsonTagName, _, _ := strings.Cut(field.Tag.Get("json"), ",")

			if !field.IsExported() || jsonTagName == "-" {
				continue
			}

			if err := IsConvertibleToJs(field.Type, visited, detail); err != nil {
				return newGoToJsErr(GetGoTypeName(rtype)+"."+field.Name, err)
			}
		}

		return nil
	case reflect.Interface:
		return nil
	}

	return nil
}

// IsNumericType checks if a reflect.Type represents a numeric type.
func IsNumericType(rtype reflect.Type) bool {
	if rtype.Kind() == reflect.Ptr {
		rtype = rtype.Elem()
	}

	switch rtype.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128:
		return true
	default:
		return false
	}
}
