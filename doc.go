// Package qjs embeds the QuickJS engine in Go via WebAssembly. It lets you evaluate JavaScript,
// call JS from Go and Go from JS, and convert values in both directions with predictable resource
// management.
//
// The core types are Runtime, Context, and Value. A Runtime owns the WebAssembly module and
// memory; a Context executes JavaScript and creates Value handles; a Value is a handle to
// a JS value associated with a Context. Unless stated otherwise, functions that return a *Value
// transfer ownership to the caller; call Value.Free when you are finished with it. Some APIs
// return borrowed values (ex: arguments passed into a Go-exposed function); do not Free borrowed
// values.
//
// Conversions.
//   - To JavaScript: use ToJSValue for arbitrary Go values; helpers exist for slices/arrays/maps,
//     numerics (GoNumberToJS), complex numbers, and structs. ChannelToJSObjectValue wraps a
//     Go channel as a JS object with async methods.
//   - To Go: use the generic JsValueToGo[T]. Specialized helpers handle numbers, arrays, sets,
//     maps, and plain objects (ex: JsArrayToGo, JsSetToGo, JsObjectToGo). When T is an interface
//     or needs disambiguation, supply a typed sample to guide the concrete type selection (ex:
//     pass []int{} to produce []int). Converters detect circular references and report contextual
//     errors (element index, key vs value, etc.).
//
// Functions.
//   - Go to JS: FuncToJS converts a Go function to a JS function. Async functions can resolve
//     or reject a provided Promise.
//   - JS to Go: JsFuncToGo binds a JS function to a Go function of type T. When T ends with
//     an error result, JS exceptions and conversion failures are returned via that error.
//
// Evaluation.
//   - Code can be evaluated in global scope or as an ES module, with strict mode, async top-level
//     await, and compile-only options. Use EvalOption functional options (ex: Code, Bytecode,
//     TypeModule, FlagAsync) to configure evaluation.
//
// Types and memory.
//   - Atom represents interned property names and must be freed. Mem safely wraps WASM memory
//     operations. Handle abstracts raw engine handles.
//   - Numeric conversion helpers (ex: FloatToInt, NumericBoundsCheck, StringToNumeric) perform
//     coercions with optional bounds checks and platform-aware limits (Is32BitPlatform).
//
// Performance and safety.
//   - Struct field mapping honors JSON tags and is cached (FieldMapper). Circular reference
//     tracking prevents infinite recursion during conversion. ProxyRegistry manages Go callables
//     referenced from JavaScript with thread-safe access. A Pool can reuse runtimes.
//
// Read each API's documentation for ownership rules, error behavior, and any special cases
// (ex: borrowed values or typed samples for generics).
package qjs
