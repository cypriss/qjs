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

	JsEvalTypeMask   = (3 << 0) // JsEvalTypeMask masks the eval type bits.
	JsEvalFlagStrict = (1 << 3) // JsEvalFlagStrict forces strict mode execution.
	JsEvalFlagUnUsed = (1 << 4) // JsEvalFlagUnUsed is reserved for future use.

	// JsEvalFlagCompileOnly returns a JS bytecode/module for JS_EvalFunction().
	JsEvalFlagCompileOnly = (1 << 5)

	// JsEvalFlagBackTraceBarrier prevents the stack frames before this eval in the Error() backtraces.
	JsEvalFlagBackTraceBarrier = (1 << 6)

	// JsEvalFlagAsync enables top-level await (global scope only).
	JsEvalFlagAsync = (1 << 7)
)

// Option configures creation and instantiation of a QuickJS runtime and module.
type Option struct {
	// CWD is the host working directory mounted as "/" in the guest FS.
	CWD string

	// StartFunctionName names a WASM start function to invoke on instantiation (optional).
	StartFunctionName string

	// Context is the base context for runtime creation, execution, and cancellation.
	Context context.Context

	// Enabling this option significantly increases evaluation time because every operation must
	// check the done context, which introduces additional overhead.
	CloseOnContextDone bool

	// DisableBuildCache disables reuse of compiled artifacts and forces recompilation.
	DisableBuildCache bool

	// MemoryLimit sets the engine's memory limit. The unit and interpretation are engine-specific.
	MemoryLimit int

	// MaxStackSize sets the maximum stack size enforced by the engine.
	MaxStackSize int

	// MaxExecutionTime sets the maximum allowed execution time. Enforcement is engine-specific.
	MaxExecutionTime int

	// GCThreshold controls when the engine triggers garbage collection.
	GCThreshold int

	// QuickJSWasmBytes overrides the embedded qjs.wasm bytes used for compilation (optional).
	QuickJSWasmBytes []byte

	// ProxyFunction is exported to WASM as "jsFunctionProxy" and bridges JS calls into Go.
	ProxyFunction any

	// Stdout is the writer wired to the module's standard output.
	Stdout io.Writer

	// Stderr is the writer wired to the module's standard error.
	Stderr io.Writer
}

// EvalOption configures JavaScript evaluation behavior in QuickJS context.
type EvalOption struct {
	// Context used to allocate values and perform WASM calls. Must be non-nil.
	c *Context

	// Logical filename for the code (used in stack traces and module identity).
	file string

	code        string // JavaScript source to evaluate. Ignored if bytecode is provided.
	bytecode    []byte // Precompiled QuickJS bytecode to execute.
	bytecodeLen int    // Length of bytecode; should match len(bytecode).
	flags       uint64 // Bitmask of JsEvalType*/JsEvalFlag* controlling evaluation mode.
	fileValue   *Value // QuickJS value handles for memory management

	// QuickJS value for code; created by Handle when code != "" and freed by Free.
	codeValue *Value

	// QuickJS value for bytecode buffer; created by Handle when set and freed by Free.
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

// FlagAsync enables top-level await in global scripts. Returns a promise from JS_Eval(). Only
// valid with TypeGlobal.
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

// FlagCompileOnly compiles code without execution. Returns bytecode object for later execution
// with JS_EvalFunction().
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

// Free releases QuickJS value handles to prevent memory leaks. Must be called after Handle()
// to clean up WASM memory.
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

// getRuntimeOption returns the first provided Option or a new one with sensible defaults.
// It ensures CWD, Context, ProxyFunction, Stdout, and Stderr are set, returning an error if
// the current working directory cannot be determined.
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
