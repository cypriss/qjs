package qjs

// load loads a JavaScript module without evaluating it. It forces module semantics and returns
// an unevaluated module value. It returns ErrInvalidFileName if file is empty.
func load(c *Context, file string, flags ...EvalOptionFunc) (*Value, error) {
	if file == "" {
		return nil, ErrInvalidFileName
	}

	// Module: Force TypeModule() since load only works with modules
	flags = append(flags, TypeModule())
	option := createEvalOption(c, file, flags...)

	evalOptions := option.Handle()

	defer option.Free()

	result := c.Call("QJS_Load", c.Raw(), evalOptions)

	return normalizeJsValue(c, result)
}

// eval evaluates a script or module in the given context using the supplied flags. It returns
// ErrInvalidFileName if file is empty.
func eval(c *Context, file string, flags ...EvalOptionFunc) (*Value, error) {
	if file == "" {
		return nil, ErrInvalidFileName
	}

	option := createEvalOption(c, file, flags...)

	evalOptions := option.Handle()
	defer option.Free()

	result := c.Call("QJS_Eval", c.Raw(), evalOptions)

	return normalizeJsValue(c, result)
}

// compile compiles a script or module and returns its bytecode. The returned slice is a copy
// and is safe to retain after the call returns.
func compile(c *Context, file string, flags ...EvalOptionFunc) (_ []byte, err error) {
	option := createEvalOption(c, file, flags...)

	evalOptions := option.Handle()
	defer option.Free()

	result := c.Call("QJS_Compile2", c.Raw(), evalOptions)
	if result, err = normalizeJsValue(c, result); err != nil {
		return nil, err
	}

	defer result.Free()

	bytecodeBytes := result.Bytes()

	// Bytecode: Create independent copy to avoid memory corruption
	bytes := make([]byte, len(bytecodeBytes))
	copy(bytes, bytecodeBytes)

	return bytes, nil
}

// normalizeJsValue converts a raw engine result into a (*Value, error). It frees value and
// returns an error on pending exceptions or JavaScript Error values; otherwise it returns
// the value and a nil error. On success, the caller must Free the returned Value.
func normalizeJsValue(c *Context, value *Value) (*Value, error) {
	hasException := c.HasException()
	if hasException {
		value.Free()

		return nil, c.Exception()
	}

	if value.IsError() {
		defer value.Free()

		return nil, value.Exception()
	}

	return value, nil
}
