package qjs

import (
	"errors"
	"fmt"
	"math/big"
	"time"
	"unsafe"
)

// Function and AsyncFunction are the Go callback types exposed to JavaScript, and JSAtom is
// the interned identifier type for property names.
type (
	// Function is the signature for Go functions exposed to JavaScript. Return the Value to be
	// seen in JS and an optional error to propagate as a JS exception.
	Function func(ctx *This) (*Value, error)

	// AsyncFunction is the signature for asynchronous Go functions exposed to JavaScript. Use
	// the provided context (ex: its Promise) to resolve or reject and then return.
	AsyncFunction func(ctx *This)

	// JSAtom is a QuickJS atom identifier for interned property names.
	JSAtom uint32
)

// Value is a handle to a JavaScript value associated with a Context. Use Value.Free when finished
// to release the underlying QuickJS resource.
type Value struct {
	// Underlying QuickJS value handle. Call Free when done to avoid leaking the JS value.
	handle *Handle

	// Owning Context used to perform all operations on this value, including conversion and property
	// access.
	context *Context
}

// This provides the JavaScript call context passed to Go functions exposed to JS. It wraps
// the JS "this" value, the call arguments, and (for async bindings) a Promise to resolve or
// reject.
type This struct {
	// Underlying "this" value for the current call. Borrowed; do not Free. Clone if you need to
	// retain it.
	*Value

	// Execution context associated with the call. Never nil.
	context *Context

	// Arguments in call order. Borrowed; do not Free. Clone values if you need to retain them
	// after the call.
	args []*Value

	// Promise available when the call is async (ex: via an AsyncFunction). Use Resolve/Reject.
	// Nil for sync calls; do not Free.
	promise *Value

	// Reports whether the Go function was invoked as async.
	isAsync bool
}

// JSPropertyEnum mirrors the engine's property enumeration entry in WASM memory.
type JSPropertyEnum struct {
	isEnumerable bool   // Whether the property is enumerable.
	atom         JSAtom // Property name as a QuickJS atom identifier.
}

// jsPropertyEnumSize is the size in bytes of JSPropertyEnum in WASM memory. It is used to
// interpret property enumeration results returned from the engine.
const jsPropertyEnumSize = uint32(unsafe.Sizeof(JSPropertyEnum{}))

// Atom represents a JavaScript atom: Object property names and some strings are stored as
// Atoms (unique strings) to save memory and allow fast comparison.
type Atom struct {
	*Value           // Underlying QuickJS atom value handle.
	context *Context // Owning Context used for atom operations and memory management.
}

// OwnProperty describes an own property of an object as returned by GetOwnProperties.
type OwnProperty struct {
	isEnumerable bool // Whether the property is enumerable.
	atom         Atom // Property name as a QuickJS Atom. Call atom.Free when finished.
}

// String returns the property's name as a Go string.
func (p OwnProperty) String() string {
	return p.atom.String()
}

// Context returns the QuickJS execution context for this call.
func (t *This) Context() *Context {
	return t.context
}

// Args returns the call arguments as JS values in the order they were passed. The returned
// values are borrowed for the duration of the call; do not Free them. Clone any value you
// need to retain beyond the call.
func (t *This) Args() []*Value {
	return t.args
}

// Promise returns the engine-managed Promise when the call is async, or nil otherwise. Use
// Resolve/Reject on the returned value within async Go callbacks. Do not Free it.
func (t *This) Promise() *Value {
	return t.promise
}

// IsAsync reports whether the current call was made through an async binding.
func (t *This) IsAsync() bool {
	return t.isAsync
}

// Free releases the underlying QuickJS atom. After Free, the Atom must not be used.
func (a Atom) Free() {
	a.context.Call("JS_FreeAtom", a.context.Raw(), a.Raw())
}

// String returns the Atom's string value.
func (a Atom) String() string {
	result := a.context.Call("QJS_AtomToCString", a.context.Raw(), a.Raw())
	defer result.handle.Free()

	return result.handle.String()
}

// ToValue converts the Atom into a JavaScript value. The caller must Free the returned Value.
func (a Atom) ToValue() *Value {
	return a.context.Call("JS_AtomToValue", a.context.Raw(), a.Raw())
}

// Handle returns the underlying QuickJS handle for v. Ownership is not transferred; do not
// free the handle directly. Use v.Free to release the value.
func (v *Value) Handle() *Handle {
	return v.handle
}

// Raw returns the underlying QuickJS handle for v. It returns 0 if v or its handle is nil.
// Raw is intended for low-level engine calls and does not transfer ownership of the handle.
func (v *Value) Raw() uint64 {
	if v == nil || v.handle == nil {
		return 0
	}

	return v.handle.raw
}

// Free releases the underlying QuickJS value owned by v. It is safe to call on a nil receiver
// or a value that has already been freed; in both cases it is a no-op. After Free returns,
// v.Raw() is 0 and v must not be used. Callers must Free every Value they own.
func (v *Value) Free() {
	if v != nil && v.Raw() != 0 {
		v.context.FreeJsValue(v.handle.raw)
		v.handle.raw = 0
	}
}

// Context returns the QuickJS execution context that owns v. The returned context is managed
// by the runtime; callers must not free it, and it becomes invalid once the owning Runtime
// is freed.
func (v *Value) Context() *Context {
	return v.context
}

// Ctx returns the raw JSContext handle associated with v for use in low-level calls. v must
// be non-nil.
func (v *Value) Ctx() uint64 {
	return v.context.Raw()
}

// Call is a convenience wrapper around v.context.Call. It invokes the exported QuickJS/WASM
// function name with the given raw arguments and returns the result as a Value. It panics
// on missing function or call failure. The caller must Free the returned Value. v must be
// non-nil.
func (v *Value) Call(name string, args ...uint64) *Value {
	return v.context.Call(name, args...)
}

// Clone returns a new Value referencing the same underlying QuickJS value. The caller must
// call Free on the returned Value.
func (v *Value) Clone() *Value {
	return v.Call("QJS_CloneValue", v.Ctx(), v.Raw())
}

// Type returns a human-readable type name for v. It recognizes special cases such as Symbol,
// QJSProxyValue, NaN, Infinity, BigInt, Number, Date, Boolean, Null, Undefined, Uninitialized,
// String, Error, Array, Map, Set, Promise, Constructor (optionally with a name), Function,
// and ArrayBuffer. It returns "unknown" if the type cannot be determined.
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

// NewUndefined creates a new JavaScript undefined value in v's context. The caller owns the
// result and must call Free when finished.
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

// GetOwnProperties returns the object's own properties. Each entry contains the property's
// enumerability and name as an Atom. Call Atom.Free on each entry when finished.
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

// GetProperty returns the value of the property named by name. The caller must Free the returned
// Value.
func (v *Value) GetProperty(name *Value) *Value {
	atom := v.Call("JS_ValueToAtom", v.Ctx(), name.Raw())

	return v.Call("JS_GetProperty", v.Ctx(), v.Raw(), atom.Raw())
}

// SetProperty sets the property identified by name on v to val. The name is converted to a
// property atom internally. val must be non-nil. This is intended for object values; otherwise
// behavior is engine-defined. Errors are not returned; if the engine raises an exception,
// it remains pending in the context.
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

// Exception converts v into a Go error, using v.String() as the message and appending the
// "stack" property if available. It is typically called on JS Error values.
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

// IsNumber reports whether v is a JavaScript Number (not a BigInt).
func (v *Value) IsNumber() bool {
	return v.Call("QJS_IsNumber", v.Raw()).handle.Bool()
}

// IsNaN reports whether v is JavaScript NaN.
func (v *Value) IsNaN() bool {
	return v.String() == "NaN"
}

// IsInfinity reports whether v is JavaScript Infinity.
func (v *Value) IsInfinity() bool {
	return v.String() == "Infinity"
}

// IsBigInt reports whether v is a JavaScript BigInt.
func (v *Value) IsBigInt() bool {
	return v.Call("QJS_IsBigInt", v.Raw()).handle.Bool()
}

// IsDate reports whether v is a JavaScript Date object.
func (v *Value) IsDate() bool {
	return v.Call("JS_IsDate", v.Raw()).handle.Bool()
}

// IsBool reports whether v is a JavaScript Boolean.
func (v *Value) IsBool() bool {
	return v.Call("QJS_IsBool", v.Raw()).handle.Bool()
}

// IsNull reports whether v is JavaScript null.
func (v *Value) IsNull() bool {
	return v.Call("QJS_IsNull", v.Raw()).handle.Bool()
}

// IsUndefined reports whether v is JavaScript undefined.
func (v *Value) IsUndefined() bool {
	return v.Call("QJS_IsUndefined", v.Raw()).handle.Bool()
}

// func (v *Value) IsException() bool {
// 	return v.Call("QJS_IsException", v.Raw()).handle.Bool()
// }

// IsUninitialized reports whether v is the QuickJS "uninitialized" sentinel value.
func (v *Value) IsUninitialized() bool {
	return v.Call("QJS_IsUninitialized", v.Raw()).handle.Bool()
}

// IsString reports whether v is a JavaScript String.
func (v *Value) IsString() bool {
	return v.Call("QJS_IsString", v.Raw()).handle.Bool()
}

// IsSymbol reports whether v is a JavaScript Symbol.
func (v *Value) IsSymbol() bool {
	return v.Call("QJS_IsSymbol", v.Raw()).handle.Bool()
}

// IsQJSProxyValue reports whether v is an internal proxy object created by this package to
// expose a Go value to JavaScript.
func (v *Value) IsQJSProxyValue() bool {
	return v.IsObject() && v.IsGlobalInstanceOf("QJS_PROXY_VALUE")
}

// IsObject reports whether v is a JavaScript object.
func (v *Value) IsObject() bool {
	return v.Call("QJS_IsObject", v.Raw()).handle.Bool()
}

// IsArray reports whether v is a JavaScript Array.
func (v *Value) IsArray() bool {
	return v.Call("QJS_IsArray", v.Raw()).handle.Bool()
}

// IsError reports whether v is a JavaScript Error object.
func (v *Value) IsError() bool {
	return v.Call("QJS_IsError", v.Ctx(), v.Raw()).handle.Bool()
}

// IsFunction reports whether v is a JavaScript function.
func (v *Value) IsFunction() bool {
	return v.Call("QJS_IsFunction", v.Ctx(), v.Raw()).handle.Bool()
}

// IsConstructor reports whether v is a JavaScript constructor (callable with new).
func (v *Value) IsConstructor() bool {
	return v.Call("QJS_IsConstructor", v.Ctx(), v.Raw()).handle.Bool()
}

// IsPromise reports whether v is a JavaScript Promise.
func (v *Value) IsPromise() bool {
	return v.Call("QJS_IsPromise", v.Ctx(), v.Raw()).handle.Bool()
}

// Resolve resolves a promise with the given arguments. This method is intended for use with
// Go function bindings (this.Promise() in async Go functions). It will NOT work with native
// JavaScript promises created via "new Promise()". For native JS promises, use direct function
// calls or Promise.withResolvers instead.
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

// Reject rejects a promise with the given arguments. This method is intended for use with
// Go function bindings (this.Promise() in async Go functions). It will NOT work with native
// JavaScript promises created via "new Promise()". For native JS promises, use direct function
// calls or Promise.withResolvers instead.
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

// Await waits for the Promise v to settle and returns its result. If v is not a Promise, Await
// returns an error.
func (v *Value) Await() (*Value, error) {
	if !v.IsPromise() {
		return nil, newInvalidJsInputErr("Promise", v)
	}

	result := v.Call("js_std_await", v.Ctx(), v.Raw())

	return normalizeJsValue(v.context, result)
}

// IsMap reports whether v is a JavaScript Map.
func (v *Value) IsMap() bool {
	return v.IsObject() && v.IsGlobalInstanceOf("Map") ||
		v.String() == "[object Map]"
}

// IsSet reports whether v is a JavaScript Set.
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

// IsByteArray return true if the value is array buffer.
func (v *Value) IsByteArray() bool {
	return v.IsObject() && v.IsGlobalInstanceOf("ArrayBuffer") ||
		v.String() == "[object ArrayBuffer]"
}

// Object returns the object value of the value.
func (v *Value) Object() *Value {
	return v.Call("JS_ToObject", v.Ctx(), v.Raw())
}

// ForEach iterates over the properties of the object and calls the given function for each
// property.
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

// Bytes returns the contents of v's handle as a byte slice. The returned bytes are a copy
// and safe to modify. It returns an empty slice if v or its handle is nil or freed.
func (v *Value) Bytes() []byte {
	return v.handle.Bytes()
}

// String returns the string representation of v as produced by QuickJS.
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

// Int32 returns the int32 value of the value. in c int is 32 bit, but in go it is depends
// on the architecture.
func (v *Value) Int32() int32 {
	return v.Call("QJS_ToInt32", v.Ctx(), v.Raw()).handle.Int32()
}

// Int64 returns v converted to int64. It panics if the value cannot be represented as an int64.
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
