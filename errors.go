package qjs

import (
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
)

// Package-wide errors and helper values used by the runtime, memory helpers, and JS/Go conversion.
var (
	// ErrRType is the reflect.Type for the built-in error interface (type error). It is used when
	// synthesizing function types and zero values via reflection.
	ErrRType = reflect.TypeOf((*error)(nil)).Elem()

	// ErrZeroRValue is the zero reflect.Value for the error type, useful as a default return when
	// dynamically creating functions via reflection.
	ErrZeroRValue = reflect.Zero(ErrRType)

	// ErrCallFuncOnNonObject is returned when invoking a method on a JS value that is not an object.
	ErrCallFuncOnNonObject = errors.New("cannot call function on non-object")

	// ErrNotAnObject indicates a JS value is not an object where an object is required.
	ErrNotAnObject = errors.New("value is not an object")

	// ErrObjectNotAConstructor is returned when attempting to call a JS value as a constructor
	// but it is not constructible.
	ErrObjectNotAConstructor = errors.New("object not a constructor")

	// ErrInvalidFileName is returned when load/eval routines receive an empty file name.
	ErrInvalidFileName = errors.New("file name is required")

	// ErrMissingProperties indicates that a JS value has no enumerable properties when they are
	// required.
	ErrMissingProperties = errors.New("value has no properties")

	// ErrInvalidPointer indicates a null pointer dereference in WebAssembly memory operations.
	ErrInvalidPointer = errors.New("null pointer dereference")

	// ErrIndexOutOfRange indicates an address/length is out of the WebAssembly memory bounds.
	ErrIndexOutOfRange = errors.New("index out of range")

	// ErrNoNullTerminator indicates a null-terminated string was read without finding a terminator.
	ErrNoNullTerminator = errors.New("no NUL terminator")

	// ErrInvalidContext indicates a missing or invalid QuickJS execution context.
	ErrInvalidContext = errors.New("invalid context")

	// ErrNotANumber indicates a JS value is not a numeric type when a number was expected.
	ErrNotANumber = errors.New("js value is not a number")

	// ErrAsyncFuncRequirePromise is returned when an async function is proxied without a Promise
	// handle.
	ErrAsyncFuncRequirePromise = errors.New("jsFunctionProxy: async function requires a promise")

	// ErrEmptyStringToNumber indicates an empty string cannot be converted to a number.
	ErrEmptyStringToNumber = errors.New("empty string cannot be converted to number")

	// ErrJsFuncDeallocated indicates the backing JS function or its context has been deallocated.
	ErrJsFuncDeallocated = errors.New("js function context has been deallocated")

	// ErrNotByteArray indicates an invalid TypedArray: its ArrayBuffer is not backed by a byte
	// array.
	ErrNotByteArray = errors.New("invalid TypedArray: buffer is not a byte array")

	// ErrNotArrayBuffer indicates a value that was expected to be an ArrayBuffer is not one.
	ErrNotArrayBuffer = errors.New("input is not an ArrayBuffer")

	// ErrMissingBufferProperty indicates a TypedArray/DataView is missing the 'buffer' property.
	ErrMissingBufferProperty = errors.New("invalid TypedArray: missing buffer property")

	// ErrRuntimeClosed indicates operations were attempted on a closed runtime.
	ErrRuntimeClosed = errors.New("runtime is closed")

	// ErrNilModule indicates a nil WebAssembly module was provided where a module is required.
	ErrNilModule = errors.New("WASM module is nil")

	// ErrNilHandle indicates a nil QuickJS handle was provided where a handle is required.
	ErrNilHandle = errors.New("handle is nil")

	// ErrChanClosed indicates a channel receive found the channel closed.
	ErrChanClosed = errors.New("channel is closed")

	// ErrChanSend indicates a non-blocking send would have blocked (buffer full or no receiver
	// ready).
	ErrChanSend = errors.New("channel send would block: buffer full or no receiver ready")

	// ErrChanReceive indicates a non-blocking receive would have blocked (buffer empty or no sender
	// ready).
	ErrChanReceive = errors.New("channel receive would block: buffer empty or no sender ready")

	// ErrChanCloseReceiveOnly indicates an attempt to close a receive-only channel.
	ErrChanCloseReceiveOnly = errors.New("cannot close receive-only channel")
)

// combineErrors returns a single error containing the messages of all non-nil errors joined
// by newlines. It returns nil only if no errors are provided.
func combineErrors(errs ...error) error {
	if len(errs) == 0 {
		return nil
	}

	var errStr string

	for _, err := range errs {
		if err != nil {
			errStr += err.Error() + "\n"
		}
	}

	return errors.New(errStr)
}

// newMaxLengthExceededErr reports that a requested length exceeds the available maximum at
// a given index.
func newMaxLengthExceededErr(request uint, maxLen int64, index int) error {
	return fmt.Errorf("length %d exceeds max %d at index %d", request, maxLen, index)
}

// newOverflowErr reports that value cannot be represented in the target type.
func newOverflowErr(value any, targetType string) error {
	return fmt.Errorf("value %v overflows %s", value, targetType)
}

// newGoToJsErr formats a conversion error describing a failure to convert a Go value to JavaScript.
// kind should describe the Go value or type (ex: "unsafe.Pointer", "slice: int") and is shown
// quoted in the message. If err is non-nil, it is wrapped with %w. If err is nil, a plain
// error is returned. Only the first element of details is used; when provided, it is inserted
// after "Go" in the message to add context (ex: "recursive pointer", "map key: K").
func newGoToJsErr(kind string, err error, details ...string) error {
	detail := ""
	if len(details) > 0 {
		detail = " " + details[0]
	}

	if err == nil {
		return fmt.Errorf("cannot convert Go%s '%s' to JS", detail, kind)
	}

	return fmt.Errorf("cannot convert Go%s '%s' to JS: %w", detail, kind, err)
}

// newJsToGoErr returns a descriptive error indicating that a JS value could not be converted
// to Go. It optionally includes a detail string, embeds a JSON or fallback rendering of the
// value, and wraps an underlying error when provided.
func newJsToGoErr(kind *Value, err error, details ...string) error {
	detail := ""
	if len(details) > 0 {
		detail = " " + details[0]
	}

	kindStr := ""

	var kindErr error

	if kind != nil {
		kindStr, kindErr = kind.JSONStringify()
		if kindErr != nil {
			kindStr = fmt.Errorf("(%w), %s", kindErr, kind.String()).Error()
		}
	}

	if kindStr == "undefined" || kindStr == "null" {
		kindStr = kind.Type()
	}

	if kindStr != "" {
		kindStr = " " + kindStr
	}

	if err == nil {
		return fmt.Errorf("cannot convert JS%s%s to Go", detail, kindStr)
	}

	return fmt.Errorf("cannot convert JS%s%s to Go: %w", detail, kindStr, err)
}

// newArgConversionErr wraps an error that occurred while converting a JS function argument
// at the given 0-based index.
func newArgConversionErr(index int, err error) error {
	return fmt.Errorf("cannot convert JS function argument at index %d: %w", index, err)
}

// newInvalidGoTargetErr returns an error describing a mismatch between the expected Go target
// (expect) and the dynamic type of the provided value (got). The message includes the runtime
// type of got using %T and is used across conversions to report incompatible target types.
func newInvalidGoTargetErr(expect string, got any) error {
	return fmt.Errorf("expected GO target %s, got %T", expect, got)
}

// newInvalidJsInputErr reports a mismatched JavaScript input type. It includes the actual
// JS type and a JSON (or fallback) representation of the value for diagnostics.
func newInvalidJsInputErr(kind string, input *Value) (err error) {
	var detail string
	if detail, err = input.JSONStringify(); err != nil {
		detail = fmt.Sprintf("(JSONStringify failed: %v), (.String()) %s", err, input.String())
	}

	return fmt.Errorf("expected JS %s, got %s=%s", kind, input.Type(), detail)
}

// newJsStringifyErr wraps an error produced while JSON-stringifying a JavaScript value. kind
// names the value being stringified (ex: "array" or "object"). The returned error wraps the
// original with %w so it can be unwrapped.
func newJsStringifyErr(kind string, err error) error {
	return fmt.Errorf("js %s: %w", kind, err)
}

// newProxyErr converts a recovered panic value into an error tagged with the proxy id and
// a Go stack trace. If r is an error, it is wrapped with %w; otherwise r is formatted as a
// string or value.
func newProxyErr(id uint64, r any) error {
	if err, ok := r.(error); ok {
		return fmt.Errorf("functionProxy [%d]: %w\n%s", id, err, debug.Stack())
	}

	if str, ok := r.(string); ok {
		return fmt.Errorf("functionProxy [%d]: %s\n%s", id, str, debug.Stack())
	}

	return fmt.Errorf("functionProxy [%d]: %v\n%s", id, r, debug.Stack())
}

// newInvokeErr wraps a method-invocation error on a JS value into a consistent error message.
// It is used by JsTimeToGo when getTime cannot be invoked on the provided value.
func newInvokeErr(input *Value, err error) error {
	return fmt.Errorf("cannot call getTime on JS value '%s', err=%w", input.String(), err)
}
