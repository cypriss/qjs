package qjs

import (
	"context"
	"fmt"
	"io"
	"os"
)

const (
	// JsEvalTypeGlobal evaluates code in global scope (default).
	JsEvalTypeGlobal = (0 << 0)
	// JsEvalTypeModule evaluates code as ES6 module.
	JsEvalTypeModule = (1 << 0)
	// JsEvalTypeDirect performs direct call (internal use).
	JsEvalTypeDirect = (2 << 0)
	// JsEvalTypeInDirect performs indirect call (internal use).
	JsEvalTypeInDirect = (3 << 0)
	// JsEvalTypeMask masks the eval type bits.
	JsEvalTypeMask = (3 << 0)
	// JsEvalFlagStrict forces strict mode execution.
	JsEvalFlagStrict = (1 << 3)
	// JsEvalFlagUnUsed is reserved for future use.
	JsEvalFlagUnUsed = (1 << 4)
	// JsEvalFlagCompileOnly returns a JS bytecode/module for JS_EvalFunction().
	JsEvalFlagCompileOnly = (1 << 5)
	// JsEvalFlagBackTraceBarrier prevents the stack frames before this eval in the Error() backtraces.
	JsEvalFlagBackTraceBarrier = (1 << 6)
	// JsEvalFlagAsync enables top-level await (global scope only).
	JsEvalFlagAsync = (1 << 7)
)

type Option struct {
	// CWD is the host working directory mounted into the WASI FS root ("/").
	// It affects module resolution and file access inside the QuickJS runtime.
	CWD string

	// StartFunctionName is an optional name of a start function to run when
	// instantiating the WASM module. Leave empty for default behavior.
	StartFunctionName string

	// Context controls lifecycle and cancellation for the runtime and WASM calls.
	// If nil, context.Background() is used.
	Context context.Context

	// CloseOnContextDone closes the underlying wazero Runtime when the Context is done.
	// Enabling this option significantly increases evaluation time because every operation
	// must check the done context, which introduces additional overhead.
	CloseOnContextDone bool

	// DisableBuildCache forces recompilation of the embedded/module bytes instead of using
	// a cached compiled module in-process.
	DisableBuildCache bool

	// MemoryLimit caps the QuickJS memory (in bytes) managed by its GC.
	MemoryLimit int

	// MaxStackSize caps the QuickJS stack size (in bytes).
	MaxStackSize int

	// MaxExecutionTime sets a soft execution time limit (milliseconds) enforced by QuickJS.
	MaxExecutionTime int

	// GCThreshold sets the QuickJS GC threshold (in bytes) before automatic collection.
	GCThreshold int

	// QuickJSWasmBytes overrides the embedded `qjs.wasm` bytes used to instantiate the runtime.
	QuickJSWasmBytes []byte

	// ProxyFunction is the host function exported to WASM as env.jsFunctionProxy.
	// It bridges JS function calls back into Go.
	ProxyFunction any

	// Stdout is wired to the WASM module's stdout.
	Stdout io.Writer

	// Stderr is wired to the WASM module's stderr.
	Stderr io.Writer
}

// EvalOption configures JavaScript evaluation behavior in QuickJS context.
type EvalOption struct {
	// c is the execution context used to allocate strings/values and make calls.
	c *Context

	// file is the logical filename used by QuickJS for stack traces and module resolution.
	file string

	// code holds the JavaScript source to evaluate. Mutually exclusive with bytecode.
	code string

	// bytecode holds precompiled bytecode to execute. Mutually exclusive with code.
	bytecode []byte

	// bytecodeLen caches len(bytecode) for efficient WASM interop.
	bytecodeLen int

	// flags is a bitmask of JsEvalType* and JsEvalFlag* values.
	flags uint64

	// QuickJS value handles for memory management
	// fileValue is a handle to the file name string.
	fileValue *Value
	// codeValue is a handle to the source code string.
	codeValue *Value
	// byteCodeValue is a handle to the bytecode buffer.
	byteCodeValue *Value
}

// EvalOptionFunc configures evaluation behavior using functional option pattern.
type EvalOptionFunc func(*EvalOption)

// createEvalOption initializes default option with global scope and strict mode.
func createEvalOption(c *Context, file string, flags ...EvalOptionFunc) *EvalOption {
	evalOption := &EvalOption{
		c:     c,
		file:  file,
		flags: JsEvalTypeGlobal | JsEvalFlagStrict,
	}

	for _, flag := range flags {
		flag(evalOption)
	}

	return evalOption
}

// Code sets the JavaScript source code to evaluate.
func Code(code string) EvalOptionFunc {
	return func(o *EvalOption) {
		o.code = code
	}
}

// Bytecode sets precompiled JavaScript bytecode to execute.
func Bytecode(buf []byte) EvalOptionFunc {
	return func(o *EvalOption) {
		o.bytecode = buf
		o.bytecodeLen = len(buf)
	}
}

// TypeGlobal sets evaluation to run in global scope (default behavior).
func TypeGlobal() EvalOptionFunc {
	return func(o *EvalOption) {
		o.flags |= JsEvalTypeGlobal
	}
}

// TypeModule sets evaluation to run as ES6 module.
func TypeModule() EvalOptionFunc {
	return func(o *EvalOption) {
		o.flags |= JsEvalTypeModule
	}
}

// FlagAsync enables top-level await in global scripts.
// Returns a promise from JS_Eval(). Only valid with TypeGlobal.
func FlagAsync() EvalOptionFunc {
	return func(o *EvalOption) {
		o.flags |= JsEvalFlagAsync
	}
}

// FlagStrict forces strict mode execution.
func FlagStrict() EvalOptionFunc {
	return func(o *EvalOption) {
		o.flags |= JsEvalFlagStrict
	}
}

// FlagCompileOnly compiles code without execution.
// Returns bytecode object for later execution with JS_EvalFunction().
func FlagCompileOnly() EvalOptionFunc {
	return func(o *EvalOption) {
		o.flags |= JsEvalFlagCompileOnly
	}
}

// TypeDirect sets direct call mode (internal QuickJS use).
func TypeDirect() EvalOptionFunc {
	return func(o *EvalOption) {
		o.flags |= JsEvalTypeDirect
	}
}

// TypeIndirect sets indirect call mode (internal QuickJS use).
func TypeIndirect() EvalOptionFunc {
	return func(o *EvalOption) {
		o.flags |= JsEvalTypeInDirect
	}
}

// TypeMask applies eval type mask (internal QuickJS use).
func TypeMask() EvalOptionFunc {
	return func(o *EvalOption) {
		o.flags |= JsEvalTypeMask
	}
}

// FlagUnused is reserved for future QuickJS features.
func FlagUnused() EvalOptionFunc {
	return func(o *EvalOption) {
		o.flags |= JsEvalFlagUnUsed
	}
}

// FlagBacktraceBarrier excludes stack frames before this eval from error backtraces.
func FlagBacktraceBarrier() EvalOptionFunc {
	return func(o *EvalOption) {
		o.flags |= JsEvalFlagBackTraceBarrier
	}
}

// Handle creates QuickJS evaluation option handle for WASM function calls.
func (o *EvalOption) Handle() (handle uint64) {
	codeHandle := uint64(0)
	byteCodeHandle := uint64(0)
	o.fileValue = o.c.NewStringHandle(o.file)

	if o.code != "" {
		o.codeValue = o.c.NewStringHandle(o.code)
		codeHandle = o.codeValue.Raw()
	}

	if o.bytecode != nil {
		o.byteCodeValue = o.c.NewBytes(o.bytecode)
		byteCodeHandle = o.byteCodeValue.Raw()
	}

	// Create QuickJS option struct via WASM call
	option := o.c.Call(
		"QJS_CreateEvalOption",
		codeHandle,
		byteCodeHandle,
		uint64(o.bytecodeLen),
		o.fileValue.Raw(),
		o.flags,
	)

	return option.Raw()
}

// Free releases QuickJS value handles to prevent memory leaks.
// Must be called after Handle() to clean up WASM memory.
func (o *EvalOption) Free() {
	if o.fileValue.Raw() != 0 {
		o.c.Call("JS_FreeValue", o.c.Raw(), o.fileValue.Raw())
	}

	if o.codeValue != nil && o.codeValue.Raw() != 0 {
		o.c.Call("JS_FreeValue", o.c.Raw(), o.codeValue.Raw())
	}

	if o.byteCodeValue != nil && o.byteCodeValue.Raw() != 0 {
		o.c.Call("JS_FreeValue", o.c.Raw(), o.byteCodeValue.Raw())
	}
}

func getRuntimeOption(registry *ProxyRegistry, options ...*Option) (option *Option, err error) {
	if len(options) == 0 || options[0] == nil {
		option = &Option{}
	} else {
		option = options[0]
	}

	if option.CWD == "" {
		if option.CWD, err = os.Getwd(); err != nil {
			return nil, fmt.Errorf("cannot get current working directory: %w", err)
		}
	}

	if option.Context == nil {
		option.Context = context.Background()
	}

	if option.ProxyFunction == nil {
		option.ProxyFunction = createFuncProxyWithRegistry(registry)
	}

	if option.Stdout == nil {
		option.Stdout = os.Stdout
	}

	if option.Stderr == nil {
		option.Stderr = os.Stderr
	}

	return option, nil
}
