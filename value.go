package qjs

import (
	"errors"
	"fmt"
	"math/big"
	"time"
	"unsafe"
)

type (
	// Function defines a Go function that can be exported to JavaScript.
	Function func(ctx *This) (*Value, error)
	// AsyncFunction defines an asynchronous Go function exposed to JavaScript.
	AsyncFunction func(ctx *This)
	// JSAtom represents a QuickJS atom identifier.
	JSAtom uint32
)

// Value wraps a QuickJS JSValue handle and its owning context.
type Value struct {
	// handle references the underlying JSValue handle.
	handle *Handle
	// context points to the QuickJS context that created the value.
	context *Context
}

// This describes the JavaScript "this" binding provided to Go callbacks.
type This struct {
	*Value

	// context is the QuickJS execution context for the function call.
	context *Context
	// args contains the arguments passed from JavaScript.
	args []*Value
	// promise references the promise used for async functions.
	promise *Value
	// isAsync reports whether the invocation originated from an async function.
	isAsync bool
}

// JSPropertyEnum captures property enumeration metadata emitted by QuickJS.
type JSPropertyEnum struct {
	// isEnumerable reports whether the property is enumerable.
	isEnumerable bool
	// atom is the identifier for the property name.
	atom JSAtom
}

const jsPropertyEnumSize = uint32(unsafe.Sizeof(JSPropertyEnum{}))

// Atom represents a JavaScript atom:
// Object property names and some strings are stored as Atoms (unique strings) to save memory and allow fast comparison.
type Atom struct {
	*Value

	context *Context
}

// OwnProperty describes an object's property entry during enumeration.
type OwnProperty struct {
	// isEnumerable reports whether the property is enumerable.
	isEnumerable bool
	// atom contains the property's atom identifier.
	atom Atom
}

// String returns the property's name as a string.
func (p OwnProperty) String() string {
	return p.atom.String()
}

// Context returns the QuickJS context for the function invocation.
func (t *This) Context() *Context {
	return t.context
}

// Args provides the arguments passed from JavaScript.
func (t *This) Args() []*Value {
	return t.args
}

// Promise returns the promise used to resolve async invocations.
func (t *This) Promise() *Value {
	return t.promise
}

// IsAsync reports whether the invocation is asynchronous.
func (t *This) IsAsync() bool {
	return t.isAsync
}

// Free releases the underlying QuickJS atom handle.
func (a Atom) Free() {
	a.context.Call("JS_FreeAtom", a.context.Raw(), a.Raw())
}

// String converts the atom into its string representation.
func (a Atom) String() string {
	result := a.context.Call("QJS_AtomToCString", a.context.Raw(), a.Raw())
	defer result.handle.Free()

	return result.handle.String()
}

// ToValue converts the atom into a JavaScript value.
func (a Atom) ToValue() *Value {
	return a.context.Call("JS_AtomToValue", a.context.Raw(), a.Raw())
}

// Handle returns the underlying Handle backing this value.
func (v *Value) Handle() *Handle {
	return v.handle
}

// Raw returns the raw QuickJS value identifier.
func (v *Value) Raw() uint64 {
	if v == nil || v.handle == nil {
		return 0
	}

	return v.handle.raw
}

// Free releases the QuickJS value, preventing further use.
func (v *Value) Free() {
	if v != nil && v.Raw() != 0 {
		v.context.FreeJsValue(v.handle.raw)
		v.handle.raw = 0
	}
}

// Context returns the QuickJS context associated with the value.
func (v *Value) Context() *Context {
	return v.context
}

// Ctx returns the raw context handle for the value.
func (v *Value) Ctx() uint64 {
	return v.context.Raw()
}

// Call invokes a QuickJS runtime function and returns the resulting value.
func (v *Value) Call(name string, args ...uint64) *Value {
	return v.context.Call(name, args...)
}

// Clone duplicates the value within the same context.
func (v *Value) Clone() *Value {
	return v.Call("QJS_CloneValue", v.Ctx(), v.Raw())
}

// Type returns a descriptive type string for the JavaScript value.
func (v *Value) Type() string {
	// Check Symbol first, as it is a special case
	if v.IsSymbol() {
		return "Symbol"
	}

	if v.IsQJSProxyValue() {
		return "QJSProxyValue"
	}

	if v.IsNaN() {
		return "NaN"
	}

	if v.IsInfinity() {
		return "Infinity"
	}

	if v.IsBigInt() {
		return "BigInt"
	}

	if v.IsNumber() {
		return "Number"
	}

	if v.IsDate() {
		return "Date"
	}

	if v.IsBool() {
		return "Boolean"
	}

	if v.IsNull() {
		return "Null"
	}

	if v.IsUndefined() {
		return "Undefined"
	}

	if v.IsUninitialized() {
		return "Uninitialized"
	}

	if v.IsString() {
		return "String"
	}

	if v.IsError() {
		return "Error"
	}

	if v.IsArray() {
		return "Array"
	}

	if v.IsMap() {
		return "Map"
	}

	if v.IsSet() {
		return "Set"
	}

	// Check Promise before Function and Constructor.
	if v.IsPromise() {
		return "Promise"
	}

	// Check Constructor before Function.
	if v.IsConstructor() {
		name := v.GetPropertyStr("name")
		defer name.Free()

		constructorName := ""
		if name.IsString() && name.String() != "" {
			constructorName = " " + name.String()
		}

		return "Constructor" + constructorName
	}

	if v.IsFunction() {
		return "Function"
	}

	if v.IsByteArray() {
		return "ArrayBuffer"
	}

	return "unknown"
}

// NewUndefined creates a new undefined value using the current context.
func (v *Value) NewUndefined() *Value {
	return v.context.NewUndefined()
}

// GetOwnPropertyNames returns the names of the properties of the value.
func (v *Value) GetOwnPropertyNames() (_ []string, err error) {
	pList := v.GetOwnProperties()

	names := make([]string, len(pList))

	for i := range names {
		names[i] = pList[i].String()
	}

	return names, nil
}

// GetOwnProperties retrieves metadata for enumerable properties on the value.
func (v *Value) GetOwnProperties() []OwnProperty {
	ptr, entriesCount := v.context.CallUnPack(
		"QJS_GetOwnPropertyNames",
		v.Ctx(),
		v.Raw(),
	)
	if entriesCount == 0 {
		return []OwnProperty{}
	}

	// Block size: number of entries * sizeof(JSPropertyEnum)
	blockSize := entriesCount * jsPropertyEnumSize
	bytes := v.context.MemRead(ptr, uint64(blockSize))

	// SAFETY: This converts C memory layout to Go structs.
	// The memory comes from QJS C code and matches JSPropertyEnum layout.
	// This is safe because:
	// 1. Memory size is validated (size * jsPropertyEnumSize)
	// 2. JSPropertyEnum layout matches C struct layout
	// 3. Memory lifetime is managed by context.FreeHandle()
	entries := unsafe.Slice((*JSPropertyEnum)(unsafe.Pointer(&bytes[0])), entriesCount)

	property := make([]OwnProperty, len(entries))

	for i, entry := range entries {
		property[i].isEnumerable = entry.isEnumerable
		property[i].atom = Atom{
			context: v.context,
			Value: v.context.NewValue(NewHandle(
				v.context.runtime,
				uint64(entry.atom),
			)),
		}
	}

	v.context.FreeHandle(uint64(ptr))

	return property
}

// GetProperty retrieves a property's value using a JavaScript value as the key.
func (v *Value) GetProperty(name *Value) *Value {
	atom := v.Call("JS_ValueToAtom", v.Ctx(), name.Raw())

	return v.Call("JS_GetProperty", v.Ctx(), v.Raw(), atom.Raw())
}

// SetProperty assigns a property identified by a JavaScript value.
func (v *Value) SetProperty(name, val *Value) {
	atom := v.Call("JS_ValueToAtom", v.Ctx(), name.Raw())
	v.Call("JS_SetProperty", v.Ctx(), v.Raw(), atom.Raw(), val.Raw())
}

// GetPropertyStr returns the value of the property with the given name.
func (v *Value) GetPropertyStr(name string) *Value {
	nameVal := v.context.NewStringHandle(name)
	defer v.context.FreeHandle(nameVal.Raw())

	return v.Call("JS_GetPropertyStr", v.Ctx(), v.Raw(), nameVal.Raw())
}

// SetPropertyStr sets the value of the property with the given name.
func (v *Value) SetPropertyStr(name string, val *Value) {
	if val != nil {
		nameVal := v.context.NewStringHandle(name)
		v.Call("JS_SetPropertyStr", v.Ctx(), v.Raw(), nameVal.Raw(), val.Raw())
	}
}

// HasPropertyIndex returns true if the value has the property with the given index.
func (v *Value) HasPropertyIndex(index int64) bool {
	prop := v.context.NewAtomIndex(index)
	defer prop.Free()

	return v.Call("JS_HasProperty", v.Ctx(), v.Raw(), prop.Raw()).Bool()
}

// HasProperty returns true if the value has the property with the given name.
func (v *Value) HasProperty(name string) bool {
	prop := v.context.NewAtom(name)
	defer prop.Free()

	return v.Call("JS_HasProperty", v.Ctx(), v.Raw(), prop.Raw()).Bool()
}

// DeleteProperty deletes the property with the given name.
func (v *Value) DeleteProperty(name string) bool {
	prop := v.context.NewAtom(name)
	defer prop.Free()

	return v.Call("JS_DeleteProperty", v.Ctx(), v.Raw(), prop.Raw(), uint64(1)).Bool()
}

// Invoke call the object's method with the given name and arguments.
func (v *Value) Invoke(fname string, args ...any) (_ *Value, err error) {
	jsArgs := make([]*Value, len(args))
	for i, arg := range args {
		if jsArgs[i], err = ToJSValue(v.context, arg); err != nil {
			return nil, err
		}
		defer jsArgs[i].Free()
	}

	return v.InvokeJS(fname, jsArgs...)
}

// InvokeJS call the object's method with the given name and JS arguments.
func (v *Value) InvokeJS(fname string, args ...*Value) (*Value, error) {
	if !v.IsObject() {
		return v.NewUndefined(), ErrCallFuncOnNonObject
	}

	fn := v.GetPropertyStr(fname)
	defer fn.Free()

	if !fn.IsFunction() {
		return v.NewUndefined(), fmt.Errorf("JS property '%s' is not a function", fname)
	}

	argc, argvPtr := createJsCallArgs(v.context, args...)
	defer v.context.FreeHandle(argvPtr)

	jsCallArgs := []uint64{v.Ctx(), fn.Raw(), v.Raw(), argc, argvPtr}
	result := v.Call("QJS_Call", jsCallArgs...)

	return normalizeJsValue(v.context, result)
}

// SetPropertyIndex sets the value of the property with the given index.
func (v *Value) SetPropertyIndex(index int64, val *Value) {
	v.Call("JS_SetPropertyUint32", v.Ctx(), v.Raw(), uint64(index), val.Raw())
}

// GetPropertyIndex returns the value of the property with the given index.
func (v *Value) GetPropertyIndex(index int64) *Value {
	return v.Call("QJS_GetPropertyUint32", v.Ctx(), v.Raw(), uint64(index))
}

// Len returns the length of the array.
func (v *Value) Len() int64 {
	l := v.GetPropertyStr("length")
	defer l.Free()

	return l.Int64()
}

// ByteLen returns the length of the ArrayBuffer.
func (v *Value) ByteLen() int64 {
	return v.GetPropertyStr("byteLength").Int64()
}

// ToByteArray returns the byte array of the ArrayBuffer.
func (v *Value) ToByteArray() []byte {
	v2 := v.Clone()

	result := v2.context.Call("QJS_GetArrayBuffer", v2.context.Raw(), v2.Raw())
	defer result.Free()

	return result.handle.Bytes()
}

// Exception converts a JavaScript Error value into a Go error.
func (v *Value) Exception() error {
	cause := v.String()

	stack := v.GetPropertyStr("stack")
	defer stack.Free()

	if stack.IsUndefined() {
		return errors.New(cause)
	}

	stackStr := stack.String()

	return errors.New(cause + "\n" + stackStr)
}

// IsNumber reports whether the value is a JavaScript number.
func (v *Value) IsNumber() bool {
	return v.Call("QJS_IsNumber", v.Raw()).handle.Bool()
}

// IsNaN reports whether the value represents NaN.
func (v *Value) IsNaN() bool {
	return v.String() == "NaN"
}

// IsInfinity reports whether the value represents positive or negative infinity.
func (v *Value) IsInfinity() bool {
	return v.String() == "Infinity"
}

// IsBigInt reports whether the value is a BigInt.
func (v *Value) IsBigInt() bool {
	return v.Call("QJS_IsBigInt", v.Raw()).handle.Bool()
}

// IsDate reports whether the value is a Date object.
func (v *Value) IsDate() bool {
	return v.Call("JS_IsDate", v.Raw()).handle.Bool()
}

// IsBool reports whether the value is a boolean.
func (v *Value) IsBool() bool {
	return v.Call("QJS_IsBool", v.Raw()).handle.Bool()
}

// IsNull reports whether the value is null.
func (v *Value) IsNull() bool {
	return v.Call("QJS_IsNull", v.Raw()).handle.Bool()
}

// IsUndefined reports whether the value is undefined.
func (v *Value) IsUndefined() bool {
	return v.Call("QJS_IsUndefined", v.Raw()).handle.Bool()
}

// func (v *Value) IsException() bool {
// 	return v.Call("QJS_IsException", v.Raw()).handle.Bool()
// }

// IsUninitialized reports whether the value is in the QuickJS uninitialized state.
func (v *Value) IsUninitialized() bool {
	return v.Call("QJS_IsUninitialized", v.Raw()).handle.Bool()
}

// IsString reports whether the value is a string.
func (v *Value) IsString() bool {
	return v.Call("QJS_IsString", v.Raw()).handle.Bool()
}

// IsSymbol reports whether the value is a Symbol.
func (v *Value) IsSymbol() bool {
	return v.Call("QJS_IsSymbol", v.Raw()).handle.Bool()
}

// IsQJSProxyValue reports whether the value is a QuickJS proxy wrapper.
func (v *Value) IsQJSProxyValue() bool {
	return v.IsObject() && v.IsGlobalInstanceOf("QJS_PROXY_VALUE")
}

// IsObject reports whether the value is considered an object.
func (v *Value) IsObject() bool {
	return v.Call("QJS_IsObject", v.Raw()).handle.Bool()
}

// IsArray reports whether the value is an array.
func (v *Value) IsArray() bool {
	return v.Call("QJS_IsArray", v.Raw()).handle.Bool()
}

// IsError reports whether the value is an error object.
func (v *Value) IsError() bool {
	return v.Call("QJS_IsError", v.Ctx(), v.Raw()).handle.Bool()
}

// IsFunction reports whether the value is a function.
func (v *Value) IsFunction() bool {
	return v.Call("QJS_IsFunction", v.Ctx(), v.Raw()).handle.Bool()
}

// IsConstructor reports whether the value can act as a constructor.
func (v *Value) IsConstructor() bool {
	return v.Call("QJS_IsConstructor", v.Ctx(), v.Raw()).handle.Bool()
}

// IsPromise reports whether the value is a Promise instance.
func (v *Value) IsPromise() bool {
	return v.Call("QJS_IsPromise", v.Ctx(), v.Raw()).handle.Bool()
}

// Resolve resolves a promise with the given arguments.
// This method is intended for use with Go function bindings (this.Promise() in async Go functions).
// It will NOT work with native JavaScript promises created via "new Promise()".
// For native JS promises, use direct function calls or Promise.withResolvers instead.
func (v *Value) Resolve(args ...*Value) error {
	if v.IsPromise() {
		result, err := v.InvokeJS("resolve", args...)
		if err != nil {
			return err
		}

		result.Free()
	}

	return nil
}

// Reject rejects a promise with the given arguments.
// This method is intended for use with Go function bindings (this.Promise() in async Go functions).
// It will NOT work with native JavaScript promises created via "new Promise()".
// For native JS promises, use direct function calls or Promise.withResolvers instead.
func (v *Value) Reject(args ...*Value) error {
	if v.IsPromise() {
		result, err := v.InvokeJS("reject", args...)
		if err != nil {
			return err
		}

		result.Free()
	}

	return nil
}

// Await blocks until the promise settles, returning its resolution or rejection.
func (v *Value) Await() (*Value, error) {
	if !v.IsPromise() {
		return nil, newInvalidJsInputErr("Promise", v)
	}

	result := v.Call("js_std_await", v.Ctx(), v.Raw())

	return normalizeJsValue(v.context, result)
}

// IsMap reports whether the value is a Map-like object.
func (v *Value) IsMap() bool {
	return v.IsObject() && v.IsGlobalInstanceOf("Map") ||
		v.String() == "[object Map]"
}

// IsSet reports whether the value is a Set-like object.
func (v *Value) IsSet() bool {
	return v.IsObject() && v.IsGlobalInstanceOf("Set") ||
		v.String() == "[object Set]"
}

// IsGlobalInstanceOf checks if the value is an instance of the given global constructor.
func (v *Value) IsGlobalInstanceOf(name string) bool {
	ctor := v.context.Global().GetPropertyStr(name)
	defer ctor.Free()

	if ctor.IsUndefined() {
		return false
	}

	instanceOf := v.Call("QJS_IsInstanceOf", v.Ctx(), v.Raw(), ctor.Raw())

	return instanceOf.handle.Bool()
}

// IsByteArray returns true if the value represents an ArrayBuffer.
func (v *Value) IsByteArray() bool {
	return v.IsObject() && v.IsGlobalInstanceOf("ArrayBuffer") ||
		v.String() == "[object ArrayBuffer]"
}

// Object returns the object value of the value.
func (v *Value) Object() *Value {
	return v.Call("JS_ToObject", v.Ctx(), v.Raw())
}

// ForEach iterates over the properties of the object and calls the given function for each property.
func (v *Value) ForEach(fn func(key *Value, value *Value)) {
	if !v.IsObject() {
		return
	}

	props := v.GetOwnProperties()
	for _, prop := range props {
		key := prop.atom

		keyValue := key.ToValue()
		if keyValue.String() == "length" {
			keyValue.Free()

			continue // Skip the length property
		}

		value := v.GetProperty(keyValue)
		fn(keyValue, value)
		key.Free()

		if !value.IsFunction() {
			value.Free()
		}
	}
}

// Bytes returns the underlying value as a byte slice copy.
func (v *Value) Bytes() []byte {
	return v.handle.Bytes()
}

// String renders the value as a UTF-8 string.
func (v *Value) String() string {
	result := v.Call("QJS_ToCString", v.Ctx(), v.Raw())
	defer result.handle.Free()

	return result.handle.String()
}

// JSONStringify returns the JSON string representation of the value.
func (v *Value) JSONStringify() (_ string, err error) {
	defer func() {
		r := AnyToError(recover())
		if r != nil {
			err = fmt.Errorf("failed to stringify JS value: %w", r)
		}
	}()

	result := v.Call("QJS_JSONStringify", v.Ctx(), v.Raw())
	defer result.handle.Free()

	return result.handle.String(), nil
}

// DateTime returns the date value of the value.
func (v *Value) DateTime(tzs ...string) *time.Time {
	var loc *time.Location
	if len(tzs) > 0 && tzs[0] != "" {
		loc = ParseTimezone(tzs[0])
	} else {
		loc = time.Local
	}

	if !v.IsDate() {
		return nil
	}

	epochValue := v.Call("QJS_ToEpochTime", v.Ctx(), v.Raw())
	defer epochValue.Free()

	epoch := epochValue.handle.Float64()

	// Check for NaN which indicates an invalid Date
	// Note: NaN != NaN is true, so this checks for NaN
	if epoch != epoch {
		return nil
	}

	epochNs := int64(epoch * 1e6)
	t := time.Unix(0, epochNs).In(loc)

	return &t
}

// Bool returns the boolean value of the value.
func (v *Value) Bool() bool {
	return v.Call("JS_ToBool", v.Ctx(), v.Raw()).handle.Bool()
}

// Int32 returns the int32 value of the value.
// in c int is 32 bit, but in go it is depends on the architecture.
func (v *Value) Int32() int32 {
	return v.Call("QJS_ToInt32", v.Ctx(), v.Raw()).handle.Int32()
}

// Int64 returns the int64 value of the value.
func (v *Value) Int64() int64 {
	return v.Call("QJS_ToInt64", v.Ctx(), v.Raw()).handle.Int64()
}

// Uint32 returns the uint32 value of the value.
func (v *Value) Uint32() uint32 {
	return v.Call("QJS_ToUint32", v.Ctx(), v.Raw()).handle.Uint32()
}

// Float64 returns the float64 value of the value.
func (v *Value) Float64() float64 {
	return v.Call("QJS_ToFloat64", v.Ctx(), v.Raw()).handle.Float64()
}

// BigInt returns the big.Int value of the value.
func (v *Value) BigInt() *big.Int {
	if !v.IsBigInt() {
		return nil
	}

	val, _ := new(big.Int).SetString(v.String(), 10)

	return val
}

// ToArray returns the array value of the value. DO NOT FREE.
func (v *Value) ToArray() (*Array, error) {
	if !v.IsArray() {
		return nil, fmt.Errorf("expected JS array, got %s=%s", v.Type(), v.String())
	}

	return NewArray(v), nil
}

// ToMap returns the map value of the value. DO NOT FREE.
func (v *Value) ToMap() *Map {
	if !v.IsMap() {
		return nil
	}

	return NewMap(v)
}

// ToSet returns the set value of the value. DO NOT FREE.
func (v *Value) ToSet() *Set {
	if !v.IsSet() {
		return nil
	}

	return NewSet(v)
}

// New creates a new instance of the value as a constructor with the given arguments.
func (v *Value) New(args ...*Value) *Value {
	return v.CallConstructor(args...)
}

// CallConstructor calls the constructor with the given arguments.
func (v *Value) CallConstructor(args ...*Value) *Value {
	if !v.IsConstructor() {
		return v.context.NewError(ErrObjectNotAConstructor)
	}

	argc, argvPtr := createJsCallArgs(v.context, args...)
	defer v.context.FreeHandle(argvPtr)

	jsCallArgs := []uint64{v.Ctx(), v.Raw(), argc, argvPtr}

	return v.Call("JS_CallConstructor", jsCallArgs...)
}
