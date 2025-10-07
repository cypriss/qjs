package qjs

import (
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
)

var (
	// ErrRType is the reflect.Type representing the Go error interface.
	ErrRType = reflect.TypeOf((*error)(nil)).Elem()
	// ErrZeroRValue provides a zero reflect.Value for the error type.
	ErrZeroRValue = reflect.Zero(ErrRType)
	// ErrCallFuncOnNonObject reports attempts to invoke functions on non-object values.
	ErrCallFuncOnNonObject = errors.New("cannot call function on non-object")
	// ErrNotAnObject indicates a value that is not a JavaScript object when one is required.
	ErrNotAnObject = errors.New("value is not an object")
	// ErrObjectNotAConstructor signals that a JavaScript object is not constructible.
	ErrObjectNotAConstructor = errors.New("object not a constructor")
	// ErrInvalidFileName is returned when a filename is missing or empty.
	ErrInvalidFileName = errors.New("file name is required")
	// ErrMissingProperties signals that a value lacks expected properties.
	ErrMissingProperties = errors.New("value has no properties")
	// ErrInvalidPointer indicates a nil or otherwise invalid pointer usage.
	ErrInvalidPointer = errors.New("null pointer dereference")
	// ErrIndexOutOfRange indicates an index exceeded allowed bounds.
	ErrIndexOutOfRange = errors.New("index out of range")
	// ErrNoNullTerminator indicates a missing C-style string terminator.
	ErrNoNullTerminator = errors.New("no NUL terminator")
	// ErrInvalidContext denotes operations attempted on an invalid context.
	ErrInvalidContext = errors.New("invalid context")
	// ErrNotANumber indicates a JavaScript value that is not numeric where expected.
	ErrNotANumber = errors.New("js value is not a number")
	// ErrAsyncFuncRequirePromise is returned when an async function lacks a promise.
	ErrAsyncFuncRequirePromise = errors.New("jsFunctionProxy: async function requires a promise")
	// ErrEmptyStringToNumber indicates empty strings cannot be converted into numbers.
	ErrEmptyStringToNumber = errors.New("empty string cannot be converted to number")
	// ErrJsFuncDeallocated indicates a JavaScript function proxy has been freed.
	ErrJsFuncDeallocated = errors.New("js function context has been deallocated")
	// ErrNotByteArray indicates a TypedArray whose buffer is not byte-based.
	ErrNotByteArray = errors.New("invalid TypedArray: buffer is not a byte array")
	// ErrNotArrayBuffer indicates an input that is not an ArrayBuffer.
	ErrNotArrayBuffer = errors.New("input is not an ArrayBuffer")
	// ErrMissingBufferProperty indicates a TypedArray lacking the buffer property.
	ErrMissingBufferProperty = errors.New("invalid TypedArray: missing buffer property")
	// ErrRuntimeClosed indicates the QuickJS runtime has been shut down.
	ErrRuntimeClosed = errors.New("runtime is closed")
	// ErrNilModule indicates a nil WASM module reference.
	ErrNilModule = errors.New("WASM module is nil")
	// ErrNilHandle indicates a nil value handle.
	ErrNilHandle = errors.New("handle is nil")
	// ErrChanClosed indicates a closed channel was used.
	ErrChanClosed = errors.New("channel is closed")
	// ErrChanSend indicates a channel send would block.
	ErrChanSend = errors.New("channel send would block: buffer full or no receiver ready")
	// ErrChanReceive indicates a channel receive would block.
	ErrChanReceive = errors.New("channel receive would block: buffer empty or no sender ready")
	// ErrChanCloseReceiveOnly reports attempts to close a receive-only channel.
	ErrChanCloseReceiveOnly = errors.New("cannot close receive-only channel")
)

// combineErrors merges multiple errors into a single newline-delimited error.
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

// newMaxLengthExceededErr reports when a requested length exceeds allowed bounds.
func newMaxLengthExceededErr(request uint, maxLen int64, index int) error {
	return fmt.Errorf("length %d exceeds max %d at index %d", request, maxLen, index)
}

// newOverflowErr creates an overflow error for the provided value and type.
func newOverflowErr(value any, targetType string) error {
	return fmt.Errorf("value %v overflows %s", value, targetType)
}

// newGoToJsErr wraps conversion failures from Go to JavaScript values.
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

// newJsToGoErr wraps conversion failures from JavaScript values to Go equivalents.
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

// newArgConversionErr reports a failure to convert a JavaScript argument at index.
func newArgConversionErr(index int, err error) error {
	return fmt.Errorf("cannot convert JS function argument at index %d: %w", index, err)
}

// newInvalidGoTargetErr reports a Go target that does not match expectations.
func newInvalidGoTargetErr(expect string, got any) error {
	return fmt.Errorf("expected GO target %s, got %T", expect, got)
}

// newInvalidJsInputErr reports that a JavaScript value does not match expected kind.
func newInvalidJsInputErr(kind string, input *Value) (err error) {
	var detail string
	if detail, err = input.JSONStringify(); err != nil {
		detail = fmt.Sprintf("(JSONStringify failed: %v), (.String()) %s", err, input.String())
	}

	return fmt.Errorf("expected JS %s, got %s=%s", kind, input.Type(), detail)
}

// newJsStringifyErr wraps JSON stringify failures for JavaScript values.
func newJsStringifyErr(kind string, err error) error {
	return fmt.Errorf("js %s: %w", kind, err)
}

// newProxyErr wraps panics that occur inside proxy function invocations.
func newProxyErr(id uint64, r any) error {
	if err, ok := r.(error); ok {
		return fmt.Errorf("functionProxy [%d]: %w\n%s", id, err, debug.Stack())
	}

	if str, ok := r.(string); ok {
		return fmt.Errorf("functionProxy [%d]: %s\n%s", id, str, debug.Stack())
	}

	return fmt.Errorf("functionProxy [%d]: %v\n%s", id, r, debug.Stack())
}

// newInvokeErr reports failures when invoking JavaScript functions from Go.
func newInvokeErr(input *Value, err error) error {
	return fmt.Errorf("cannot call getTime on JS value '%s', err=%w", input.String(), err)
}
