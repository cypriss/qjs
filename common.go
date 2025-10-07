package qjs

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MinMapForeachArgs is the minimum number of arguments for Map forEach callback (value, key).
	MinMapForeachArgs = 2

	// NanosToMillis is the conversion factor from nanoseconds to milliseconds.
	NanosToMillis = 1e6

	// Uint64ByteSize is the size of a uint64 value in bytes.
	Uint64ByteSize = 8

	// Uint64SignBitPosition is the bit position of the sign bit in a 64-bit unsigned integer.
	Uint64SignBitPosition = 63

	// PackedPtrSize is the size in bytes of a packed pointer structure.
	PackedPtrSize = 8

	// NullPtr represents a null pointer value.
	NullPtr = uint32(0)

	// StringTerminator is the null terminator byte for C-style strings.
	StringTerminator = byte(0)
)

// NumberType is a type constraint that matches all Go numeric types supported by GoNumberToJS.
type NumberType interface {
	int |
		int8 |
		int16 |
		int32 |
		int64 |
		uint |
		uint8 |
		uint16 |
		uint32 |
		uint64 |
		uintptr |
		float32 |
		float64
}

var (
	// Overflow bounds for numeric types.
	numericBounds = map[reflect.Kind][2]float64{
		reflect.Int8:    {math.MinInt8, math.MaxInt8},
		reflect.Int16:   {math.MinInt16, math.MaxInt16},
		reflect.Int32:   {math.MinInt32, math.MaxInt32},
		reflect.Int64:   {math.MinInt64, math.MaxInt64},
		reflect.Uint8:   {0, math.MaxUint8},
		reflect.Uint16:  {0, math.MaxUint16},
		reflect.Uint32:  {0, math.MaxUint32},
		reflect.Uint64:  {0, math.MaxUint64},
		reflect.Float32: {-math.MaxFloat32, math.MaxFloat32},
		reflect.Float64: {-math.MaxFloat64, math.MaxFloat64},
	}

	// TypedArray types for validation.
	typedArrayTypes = []string{
		"Uint8Array",
		"Int8Array",
		"Uint16Array",
		"Int16Array",
		"Uint32Array",
		"Int32Array",
		"Float32Array",
		"Float64Array",
		"BigInt64Array",
		"BigUint64Array",
		"DataView",
	}
)

// ObjectOrMap interface for unified object/map handling.
type ObjectOrMap interface {
	// IsObject reports whether the receiver wraps a plain JavaScript object (not a Map).
	IsObject() bool

	// IsMap reports whether the receiver wraps a JavaScript Map.
	IsMap() bool

	// ToMap returns a Map wrapper for the receiver. Call only when IsMap reports true.
	ToMap() *Map

	// JSONStringify returns the JSON encoding of the value or an error if stringification fails.
	JSONStringify() (string, error)

	// IsNull reports whether the underlying JavaScript value is null.
	IsNull() bool

	// ForEach calls callback for each key/value pair. For objects, keys are property names; for
	// Maps, keys are entry keys. The Value arguments are valid only during the callback and must
	// not be freed by the caller. Iteration order is unspecified.
	ForEach(callback func(*Value, *Value))
}

// FieldMapper handles struct field mapping with caching for performance.
type FieldMapper struct {
	// mu protects concurrent access to the field map cache.
	mu sync.RWMutex

	// cache stores, per struct type, the mapping from JSON field names to FieldPath.
	cache map[reflect.Type]map[string]FieldPath
}

// FieldPath stores the path to a field through embedded structs.
type FieldPath struct {
	indices []int               // Path to the field through embedded structs
	field   reflect.StructField // Field info
}

// NewFieldMapper creates a new field mapper with initialized cache.
func NewFieldMapper() *FieldMapper {
	return &FieldMapper{
		cache: make(map[reflect.Type]map[string]FieldPath),
	}
}

// Global field mapper instance for backward compatibility.
var globalFieldMapper = NewFieldMapper()

// GetFieldMap returns or builds a field map for a struct type.
func (fm *FieldMapper) GetFieldMap(structType reflect.Type) map[string]FieldPath {
	// First try read lock for existing cache
	fm.mu.RLock()

	if fieldMap, exists := fm.cache[structType]; exists {
		fm.mu.RUnlock()

		return fieldMap
	}

	fm.mu.RUnlock()

	// Upgrade to write lock to build the map
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Double-check after acquiring write lock
	if fieldMap, exists := fm.cache[structType]; exists {
		return fieldMap
	}

	fieldMap := make(map[string]FieldPath)
	seen := make(map[string]bool)

	fm.processFields(structType, []int{}, seen, fieldMap)

	// Cache the field map
	fm.cache[structType] = fieldMap

	return fieldMap
}

// processFields recursively processes struct fields including embedded struct promotion.
func (fm *FieldMapper) processFields(
	structType reflect.Type,
	indexPrefix []int,
	seen map[string]bool,
	fieldMap map[string]FieldPath,
) {
	for i := range structType.NumField() {
		field := structType.Field(i)
		fieldIndices := append(append([]int{}, indexPrefix...), i)

		// Handle non-embedded fields
		if !field.Anonymous {
			if !field.IsExported() {
				continue
			}

			fieldName := fm.getJSONFieldName(field)
			if fieldName == "" { // Skip fields marked with json:"-"
				continue
			}

			// Respect Go's field promotion: shallowest field wins
			if !seen[fieldName] {
				fieldMap[fieldName] = FieldPath{indices: fieldIndices, field: field}
				seen[fieldName] = true
			}

			continue
		}

		// Handle embedded fields
		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		if fieldType.Kind() == reflect.Struct {
			// Process fields from embedded struct
			fm.processFields(fieldType, fieldIndices, seen, fieldMap)
		}
	}
}

// getJSONFieldName extracts the JSON field name from struct tags.
func (fm *FieldMapper) getJSONFieldName(field reflect.StructField) string {
	fieldName := field.Name

	if tag := field.Tag.Get("json"); tag != "" {
		parts := strings.Split(tag, ",")
		if parts[0] == "-" {
			return "" // Skip this field
		}

		if parts[0] != "" {
			fieldName = parts[0]
		}
	}

	return fieldName
}

// getFieldMap provides backward compatibility for the old API.
func getFieldMap(structType reflect.Type) map[string]FieldPath {
	return globalFieldMapper.GetFieldMap(structType)
}

// Tracker tracks objects during Go-JS conversion to detect circular references.
type Tracker[T uintptr | uint64] struct {
	// processing holds the set of object identities currently being converted.
	processing map[T]bool
}

// NewTracker creates a new conversion context for tracking circular references.
func NewTracker[T uintptr | uint64]() *Tracker[T] {
	return &Tracker[T]{
		processing: make(map[T]bool),
	}
}

// Track registers an object for circular reference detection. Returns true if the object is
// already being processed (circular reference detected).
func (tracker *Tracker[T]) Track(ptr T) bool {
	if tracker.processing[ptr] {
		return true
	}

	tracker.processing[ptr] = true

	return false
}

// UnTrack removes an object from circular reference tracking.
func (tracker *Tracker[T]) UnTrack(ptr T) {
	delete(tracker.processing, ptr)
}

// CircularTracker manages the lifecycle of circular reference tracking for a single object.
type CircularTracker[T uintptr | uint64] struct {
	// ctx is the owning Tracker used for registration and unregistration.
	ctx *Tracker[T]

	// ptr is the tracked object identity.
	ptr T

	// needsUnregister reports whether cleanup should call UnTrack.
	needsUnregister bool
}

// trackPtr sets up circular reference tracking for a pointer.
func (ct *CircularTracker[T]) trackPtr(ctx *Tracker[T], ptr T) error {
	if ctx.Track(ptr) {
		return newGoToJsErr("", nil, "recursive pointer")
	}

	ct.ctx = ctx
	ct.ptr = ptr
	ct.needsUnregister = true

	return nil
}

// // trackValue sets up circular reference tracking for a JS value.
func (ct *CircularTracker[T]) trackValue(ctx *Tracker[T], value *Value) error {
	var ptr any = value.Raw()

	typeT, _ := ptr.(T)
	if ctx.Track(typeT) {
		return newJsToGoErr(value, nil, "circular reference")
	}

	ct.ctx = ctx
	ct.ptr = typeT
	ct.needsUnregister = true

	return nil
}

// cleanup removes the object from circular reference tracking.
func (ct *CircularTracker[T]) cleanup() {
	if ct.needsUnregister && ct.ctx != nil {
		ct.ctx.UnTrack(ct.ptr)
	}
}

// JsNumericToGoConverter handles conversion from float64 to various numeric types.
type JsNumericToGoConverter struct {
	// targetType is the concrete (non-pointer) numeric type to convert into.
	targetType reflect.Type

	// isPointer indicates Convert should return a pointer to the target type.
	isPointer bool
}

// NewJsNumericToGoConverter returns a converter configured to produce values of targetType.
// If targetType is a pointer, the converter will return a pointer to the converted value.
func NewJsNumericToGoConverter(targetType reflect.Type) *JsNumericToGoConverter {
	isPointer := targetType.Kind() == reflect.Ptr
	if isPointer {
		targetType = targetType.Elem()
	}

	return &JsNumericToGoConverter{
		targetType: targetType,
		isPointer:  isPointer,
	}
}

// Convert converts floatVal to the converter's target numeric type. It first validates the
// value with NumericBoundsCheck, then performs the conversion via FloatToInt. If the converter
// is configured to return a pointer, Convert allocates and returns a pointer to the target
// value. It returns an error on overflow or if the target kind is unsupported.
func (nc *JsNumericToGoConverter) Convert(floatVal float64) (any, error) {
	targetKind := nc.targetType.Kind()
	if err := NumericBoundsCheck(floatVal, targetKind); err != nil {
		return nil, err
	}

	result, err := FloatToInt(floatVal, targetKind)
	if err != nil {
		return nil, err
	}

	if nc.isPointer {
		ptr := reflect.New(nc.targetType)
		ptr.Elem().Set(reflect.ValueOf(result))
		result = ptr.Interface()
	}

	return result, nil
}

// JsArrayToGoConverter handles array conversions with better error handling and performance.
type JsArrayToGoConverter[T any] struct {
	// tracker detects circular references during conversion.
	tracker *Tracker[uint64]

	// input is the source JavaScript value to convert (ex: a JS array).
	input *Value

	// targetType is the Go type to materialize, derived from sample.
	targetType reflect.Type

	// sample is an exemplar of T used to infer targetType; zero if none was provided.
	sample T
}

// NewJsArrayToGoConverter constructs a converter that materializes a JavaScript Array in input
// as a Go value of type T. Use the returned converter's Convert method to perform the conversion.
//
// If samples is provided, the first value is used as an exemplar to infer or refine the concrete
// target type (ex: pass []int{} to produce []int). When no sample is supplied, specify T explicitly
// (ex: NewJsArrayToGoConverter[[]int](input)). An exemplar is often required when T is an
// interface to indicate the desired concrete type.
//
// input should refer to a JavaScript Array; converting a non-array value will cause Convert
// to report an error. The converter detects circular references during conversion.
func NewJsArrayToGoConverter[T any](input *Value, samples ...T) *JsArrayToGoConverter[T] {
	var sample T
	if len(samples) > 0 {
		sample = samples[0]
	}

	return &JsArrayToGoConverter[T]{
		tracker:    NewTracker[uint64](),
		input:      input,
		targetType: reflect.TypeOf(sample),
		sample:     sample,
	}
}

// Convert materializes ac.input into a Go value of type T. It first coerces the input to a
// JavaScript Array and then chooses a conversion strategy based on ac.targetType:
//   - interface or unspecified: builds a []any;
//   - slice: builds a slice with element-wise conversion;
//   - array: fills a fixed-size array (failing if the JS length exceeds the Go length);
//   - otherwise: falls back to JSON stringify/unmarshal into ac.targetType.
//
// On error, Convert returns the zero value of T and a wrapped error that includes the failing
// element or input where available.
func (ac *JsArrayToGoConverter[T]) Convert() (T, error) {
	var zero T

	jsArray, err := ac.input.ToArray()
	if err != nil {
		return zero, newJsToGoErr(ac.input, err, "Array")
	}

	jsLen := jsArray.Len()
	if ac.targetType == nil || ac.targetType.Kind() == reflect.Interface {
		return ac.convertToInterface(jsArray, jsLen)
	}

	switch ac.targetType.Kind() {
	case reflect.Slice:
		return ac.convertToSlice(jsArray, jsLen)
	case reflect.Array:
		return ac.convertToArray(jsArray, jsLen)
	default:
		return ac.convertViaJSON(jsArray)
	}
}

// convertToInterface converts jsArray into a []any and returns it as T. Each element is converted
// using jsValueToGo[any]. Non-function JS element values are freed after conversion. Errors
// include the element index to aid debugging.
func (ac *JsArrayToGoConverter[T]) convertToInterface(jsArray *Array, jsLen int64) (T, error) {
	result := make([]any, 0, jsLen)

	for i := range jsLen {
		jsElem := jsArray.Get(i)

		goElem, convErr := jsValueToGo[any](ac.tracker, jsElem)

		if !jsElem.IsFunction() {
			jsElem.Free()
		}

		if convErr != nil {
			return ac.sample, newJsToGoErr(
				jsArray.Value,
				convErr,
				fmt.Sprintf("array/set element at index %d", int(i)),
			)
		}

		result = append(result, goElem)
	}

	resultT, _ := any(result).(T)

	return resultT, nil
}

// convertToSlice converts jsArray into a Go slice of ac.targetType. It appends element-wise
// conversions to a newly allocated slice with capacity jsLen. Conversion respects the slice
// element type; nil results store the element type's zero value. Non-function JS element values
// are freed after conversion. Errors include the element index.
func (ac *JsArrayToGoConverter[T]) convertToSlice(jsArray *Array, jsLen int64) (T, error) {
	elemType := ac.targetType.Elem()
	sliceValue := reflect.MakeSlice(ac.targetType, 0, int(jsLen))

	for i := range jsLen {
		jsElem := jsArray.Get(i)
		elemSample := reflect.New(elemType).Elem().Interface()
		goElem, convErr := jsValueToGo(ac.tracker, jsElem, elemSample)

		if !jsElem.IsFunction() {
			jsElem.Free()
		}

		if convErr != nil {
			return ac.sample, newJsToGoErr(
				jsArray.Value,
				convErr,
				fmt.Sprintf("array/set element at index %d", int(i)),
			)
		}

		elemValue := ac.convertElementValue(goElem, elemType)
		sliceValue = reflect.Append(sliceValue, elemValue)
	}

	sliceT, _ := sliceValue.Interface().(T)

	return sliceT, nil
}

// convertToArray converts jsArray into a fixed-size Go array of ac.targetType. It returns
// an error when jsLen exceeds the Go array length. If jsLen is smaller, the remaining elements
// keep their zero values. Each element is converted to the array element type; nil results
// produce the element type's zero value. Non-function JS element values are freed after conversion.
func (ac *JsArrayToGoConverter[T]) convertToArray(jsArray *Array, jsLen int64) (T, error) {
	elemType := ac.targetType.Elem()
	goArrayLen := ac.targetType.Len()
	goArrayValue := reflect.New(ac.targetType).Elem()

	if jsLen > int64(goArrayLen) {
		return ac.sample, fmt.Errorf("JS array/set length (%d) exceeds Go array length (%d)", jsLen, goArrayLen)
	}

	for i := range jsLen {
		jsElem := jsArray.Get(i)
		elemSample := reflect.New(elemType).Elem().Interface()
		goElem, convErr := jsValueToGo(ac.tracker, jsElem, elemSample)

		if !jsElem.IsFunction() {
			jsElem.Free()
		}

		if convErr != nil {
			return ac.sample, newJsToGoErr(
				jsArray.Value,
				convErr,
				fmt.Sprintf("array/set element at index %d", int(i)),
			)
		}

		elemValue := ac.convertElementValue(goElem, elemType)
		goArrayValue.Index(int(i)).Set(elemValue)
	}

	arrayT, _ := goArrayValue.Interface().(T)

	return arrayT, nil
}

// convertElementValue returns the reflect.Value to store for one array element. If goElem
// is nil, it returns the zero value of elemType; otherwise it returns reflect.ValueOf(goElem).
func (ac *JsArrayToGoConverter[T]) convertElementValue(goElem any, elemType reflect.Type) reflect.Value {
	if goElem == nil {
		return reflect.Zero(elemType)
	}

	return reflect.ValueOf(goElem)
}

// convertViaJSON converts a JavaScript array to T by JSON-stringifying the array and unmarshaling
// into ac.targetType. It is used as a fallback when the target is neither a slice nor an array.
// On stringify failure, it returns a wrapped error via newJsStringifyErr("array", err). On
// unmarshal failure, the returned error includes the raw JSON payload.
func (ac *JsArrayToGoConverter[T]) convertViaJSON(jsArray *Array) (T, error) {
	jsonString, err := jsArray.JSONStringify()
	if err != nil {
		return ac.sample, newJsStringifyErr("array", err)
	}

	temp := reflect.New(ac.targetType).Interface()
	if err = json.Unmarshal([]byte(jsonString), temp); err != nil {
		return ac.sample, fmt.Errorf("can not unmarshal json: %w, input=%s", err, jsonString)
	}

	arrayT, _ := reflect.ValueOf(temp).Elem().Interface().(T)

	return arrayT, nil
}

// FloatToInt converts floatVal to a value of the numeric kind targetKind. Integer conversions
// truncate toward zero; complex conversions set the imaginary part to 0. No range checking
// is performed; call NumericBoundsCheck first if you need overflow protection. Returns an
// error if targetKind is unsupported.
func FloatToInt(floatVal float64, targetKind reflect.Kind) (any, error) {
	var result any

	switch targetKind {
	case reflect.Int:
		result = int(floatVal)
	case reflect.Int8:
		result = int8(floatVal)
	case reflect.Int16:
		result = int16(floatVal)
	case reflect.Int32:
		result = int32(floatVal)
	case reflect.Int64:
		result = int64(floatVal)
	case reflect.Uint:
		result = uint(floatVal)
	case reflect.Uint8:
		result = uint8(floatVal)
	case reflect.Uint16:
		result = uint16(floatVal)
	case reflect.Uint32:
		result = uint32(floatVal)
	case reflect.Uint64:
		result = uint64(floatVal)
	case reflect.Uintptr:
		result = uintptr(floatVal)
	case reflect.Float32:
		result = float32(floatVal)
	case reflect.Float64:
		result = floatVal
	case reflect.Complex64:
		result = complex(float32(floatVal), 0)
	case reflect.Complex128:
		result = complex(floatVal, 0)
	default:
		return nil, fmt.Errorf("unsupported numeric type: %s", targetKind.String())
	}

	return result, nil
}

// IsValid32BitFloat checks whether floatVal fits into a 32-bit int or uint, depending on targetKind.
// It returns an overflow error if floatVal is outside the valid range.
func IsValid32BitFloat(floatVal float64, targetKind reflect.Kind) error {
	var bounds [2]float64
	if targetKind == reflect.Int {
		bounds = [2]float64{math.MinInt32, math.MaxInt32}
	} else {
		bounds = [2]float64{0, math.MaxUint32}
	}

	if floatVal < bounds[0] || floatVal > bounds[1] {
		return newOverflowErr(floatVal, targetKind.String())
	}

	return nil
}

// NumericBoundsCheck reports an overflow if floatVal cannot be represented by targetKind.
// It checks platform-independent bounds for the fixed-width integer and float kinds, and treats
// reflect.Int and reflect.Uint as 32-bit on 32-bit platforms (validated via IsValid32BitFloat).
// It returns nil if the value is in range. For kinds not covered, no check is performed and
// nil is returned.
func NumericBoundsCheck(floatVal float64, targetKind reflect.Kind) error {
	if bounds, ok := numericBounds[targetKind]; ok {
		if floatVal < bounds[0] || floatVal > bounds[1] {
			return newOverflowErr(floatVal, targetKind.String())
		}
	}

	// Special handling for int/uint on 32-bit platforms
	isIntUint := targetKind == reflect.Int || targetKind == reflect.Uint
	if isIntUint && Is32BitPlatform() {
		return IsValid32BitFloat(floatVal, targetKind)
	}

	return nil
}

// IsTypedArray returns true if the input is TypedArray or DataView.
func IsTypedArray(input *Value) bool {
	for _, typeName := range typedArrayTypes {
		if input.IsGlobalInstanceOf(typeName) {
			return true
		}
	}

	return false
}

// processTempValue validates if temp is a valid result for the given T type.
func processTempValue[T any](prefix string, temp any, err error, samples ...T) (v T, _ error) {
	if err != nil {
		return v, fmt.Errorf("[%s] %w", prefix, err)
	}

	// Handle interface{} target types by accepting any converted value
	_, sample := createTemp(samples...)

	sampleRType := reflect.TypeOf(sample)
	if sampleRType == nil || sampleRType.Kind() == reflect.Interface {
		if temp == nil {
			return v, nil
		}

		tempT, _ := temp.(T)

		return tempT, nil
	}

	tempRType := reflect.TypeOf(temp)
	if tempRType != sampleRType {
		tempRValue := reflect.ValueOf(temp)
		if tempRValue.IsValid() && tempRValue.Type().ConvertibleTo(sampleRType) {
			temp = tempRValue.Convert(sampleRType).Interface()
			tempT, _ := temp.(T)

			return tempT, nil
		}

		return v, newInvalidGoTargetErr(GetGoTypeName(sample), temp)
	}

	valueT, _ := temp.(T)

	return valueT, nil
}

// StringToNumeric converts s to the numeric type described by targetType. Whitespace is trimmed.
// If targetType is a pointer, the function converts to its element type and returns a newly
// allocated pointer to the value.
//
// Integer kinds are parsed using a float first (JavaScript-style) and then truncated toward
// zero. Narrow integer and unsigned kinds are range-checked (unsigned values must be non-negative).
// Float32/Float64 use strconv.ParseFloat with 32/64-bit precision. Uintptr requires a non-negative
// value.
//
// An empty string returns ErrEmptyStringToNumber. If s cannot be converted to targetType,
// a conversion error is returned. Note that for Int/Int64 and Uint/Uint64, no explicit upper-bound
// checks are performed.
func StringToNumeric(s string, targetType reflect.Type) (result any, err error) {
	s = strings.TrimSpace(s)

	if targetType.Kind() == reflect.Ptr {
		elemType := targetType.Elem()

		result, err := StringToNumeric(s, elemType)
		if err != nil {
			return nil, err
		}

		ptrVal := reflect.New(elemType)
		ptrVal.Elem().Set(reflect.ValueOf(result))

		return ptrVal.Interface(), nil
	}

	if s == "" {
		return nil, ErrEmptyStringToNumber
	}

	// For integer types, try to parse as float first (JavaScript-style conversion)
	// then truncate to integer, which mimics JavaScript's Number() conversion
	switch targetType.Kind() {
	case reflect.Int:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			return int(val), nil
		}
	case reflect.Int8:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			intVal := int8(val)
			if float64(intVal) == val || (val >= -128 && val <= 127) {
				return intVal, nil
			}
		}
	case reflect.Int16:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			intVal := int16(val)
			if float64(intVal) == val || (val >= -32768 && val <= 32767) {
				return intVal, nil
			}
		}
	case reflect.Int32:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			intVal := int32(val)
			if float64(intVal) == val || (val >= -2147483648 && val <= 2147483647) {
				return intVal, nil
			}
		}
	case reflect.Int64:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(val), nil
		}
	case reflect.Uint:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			return uint(val), nil
		}
	case reflect.Uint8:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			intVal := uint8(val)
			if float64(intVal) == val || (val >= 0 && val <= 255) {
				return intVal, nil
			}
		}
	case reflect.Uint16:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			intVal := uint16(val)
			if float64(intVal) == val || (val >= 0 && val <= 65535) {
				return intVal, nil
			}
		}
	case reflect.Uint32:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			intVal := uint32(val)
			if float64(intVal) == val || (val >= 0 && val <= 4294967295) {
				return intVal, nil
			}
		}
	case reflect.Uint64:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			return uint64(val), nil
		}
	case reflect.Uintptr:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			return uintptr(val), nil
		}
	case reflect.Float32:
		if val, err := strconv.ParseFloat(s, 32); err == nil {
			return float32(val), nil
		}
	case reflect.Float64:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			return val, nil
		}
	}

	return nil, fmt.Errorf("cannot convert JS string %q to %s", s, targetType.String())
}

// createGoObjectTarget prepares conversion state for turning a JavaScript object or Map into
// a Go value of type T. It selects a sample (if provided), normalizes Map inputs to a Map
// wrapper, chooses the target type (defaulting to map[string]any when the sample is nil),
// and allocates a pointer to a zero value of that type.
//
// Returns:
//   - temp: a pointer to the allocated zero value of the target type.
//   - obj: the normalized input (Map wrapper if the input is a Map, otherwise the original
//     object).
//   - sample: the selected sample value used to infer the target type.
//   - target: the reflect.Type chosen for the conversion.
func createGoObjectTarget[T any](input ObjectOrMap, samples ...T) (
	temp any,
	obj ObjectOrMap,
	sample T,
	target reflect.Type,
) {
	if len(samples) > 0 {
		sample = samples[0]
	}

	obj = input
	if obj.IsMap() {
		obj = obj.ToMap()
	}

	target = reflect.TypeOf(sample)
	if target == nil {
		target = reflect.TypeOf(map[string]any{})
	}

	temp = reflect.New(target).Interface()

	return temp, obj, sample, target
}

// canConvertToGoNumber performs common validation for number inputs.
func canConvertToGoNumber(input *Value) error {
	if !input.IsNumber() {
		return newJsToGoErr(input, nil, "number")
	}

	if input.IsNaN() {
		return newJsToGoErr(input, nil, "NaN")
	}

	if input.IsInfinity() {
		return newJsToGoErr(input, nil, "Infinity")
	}

	return nil
}

// createTemp returns a nil-initialized temporary value (temp) and a sample of type T. If samples
// contains at least one element, the first is returned as the sample; otherwise, the zero
// value of T is used. Callers typically assign to temp during conversion and later pass (temp,
// sample) to processTempValue to finalize typed results.
func createTemp[T any](samples ...T) (any, T) {
	var (
		tempValue any
		sample    T
	)

	if len(samples) > 0 {
		sample = samples[0]
	}

	return tempValue, sample
}

// isFloatWholeNumber reports whether floatVal has no fractional component and fits in int64.
func isFloatWholeNumber(floatVal float64) bool {
	return floatVal == float64(int64(floatVal)) &&
		floatVal >= math.MinInt64 &&
		floatVal <= math.MaxInt64
}

// isGoStruct reports whether goType is a struct or a pointer to a struct.
func isGoStruct(goType reflect.Type) bool {
	return goType.Kind() == reflect.Struct ||
		(goType.Kind() == reflect.Ptr && goType.Elem().Kind() == reflect.Struct)
}

// VerifyGoFunc validates that a function signature is compatible with JS conversion.
func VerifyGoFunc(fnType reflect.Type, sample any) error {
	if fnType == nil || fnType.Kind() != reflect.Func {
		return newInvalidGoTargetErr("function", sample)
	}

	// Validate that all return values are convertible to JS
	for i := range fnType.NumOut() {
		err := IsConvertibleToJs(fnType.Out(i), make(map[reflect.Type]bool), "func return")
		if err != nil {
			return err
		}
	}

	// Validate that all parameters are convertible from JS
	for i := range fnType.NumIn() {
		err := IsConvertibleToJs(fnType.In(i), make(map[reflect.Type]bool), "func param")
		if err != nil {
			return fmt.Errorf("parameter %d error: %w", i, err)
		}
	}

	return nil
}

// CreateGoBindFuncType extracts and validates the function type to bind to JavaScript. If
// sample is a function value (including a typed nil), fnType is set to its type. In all cases,
// the type is validated by VerifyGoFunc. The returned error describes any incompatibility
// for JS conversion.
func CreateGoBindFuncType[T any](sample T) (fnType reflect.Type, err error) {
	sampleVal := reflect.ValueOf(sample)
	if sampleVal.IsValid() && sampleVal.Kind() == reflect.Func {
		fnType = sampleVal.Type()
	}

	if err = VerifyGoFunc(fnType, sample); err != nil {
		return fnType, err
	}

	return fnType, nil
}

// AnyToError converts any value into an error, returning nil if the input is nil. If the input
// is an error, it is returned as-is. If it is a string, it is wrapped with a "recovered from
// panic" message. All other values are formatted with %v. This is intended for use in deferred
// panic-recovery paths.
func AnyToError(err any) error {
	if err == nil {
		return nil
	}

	if e, ok := err.(error); ok {
		return e
	}

	if str, ok := err.(string); ok {
		return fmt.Errorf("recovered from panic: %s", str)
	}

	return fmt.Errorf("recovered from panic: %v", err)
}

// hashBytes computes a simple hash of the byte slice.
func hashBytes(data []byte) uint64 {
	h := fnv.New64a()
	h.Write(data)

	return h.Sum64()
}

// ParseTimezone attempts to parse a timezone string as either an IANA location name or a UTC
// offset format (+/-HH:MM). Returns UTC location if parsing fails.
func ParseTimezone(tz string) *time.Location {
	// First try to parse as IANA timezone name (e.g., "America/New_York", "Asia/Tokyo")
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}

	// If IANA parsing fails, try to parse as UTC offset (e.g., "+05:30", "-08:00")
	hasSign := tz[0] == '+' || tz[0] == '-'
	if len(tz) >= 3 && hasSign {
		// Parse formats like "+05:30", "-08:00", "+0530", "-0800"
		var sign = 1
		if tz[0] == '-' {
			sign = -1
		}

		offset := tz[1:]

		var hours, minutes int

		// Handle both "+05:30" and "+0530" formats
		switch {
		case len(offset) == 5 && offset[2] == ':':
			_, _ = fmt.Sscanf(offset, "%02d:%02d", &hours, &minutes)
		case len(offset) == 4:
			_, _ = fmt.Sscanf(offset, "%02d%02d", &hours, &minutes)
		case len(offset) == 2:
			_, _ = fmt.Sscanf(offset, "%02d", &hours)
		}

		if hours >= 0 && hours <= 23 && minutes >= 0 && minutes <= 59 {
			offsetSeconds := sign * (hours*3600 + minutes*60)

			return time.FixedZone(tz, offsetSeconds)
		}
	}

	// Fallback to UTC if parsing fails
	return time.UTC
}

// Is32BitPlatform check if the platform is 32-bit by comparing the size of uintptr.
func Is32BitPlatform() bool {
	return strconv.IntSize == 32
}

// ChannelToJSObjectValue converts a Go channel to a JavaScript object with async methods.
func ChannelToJSObjectValue(
	c *Context,
	rtype reflect.Type,
	rval reflect.Value,
) (*Value, error) {
	if rval.IsNil() {
		return c.NewNull(), nil
	}

	return withJSObject(c, func(obj *Value) error {
		// Check channel direction
		chanDir := rtype.ChanDir()
		canSend := chanDir == reflect.BothDir || chanDir == reflect.SendDir
		canRecv := chanDir == reflect.BothDir || chanDir == reflect.RecvDir
		objMethods := map[string]any{
			"close":    CreateChannelCloseFunc(rval),
			"length":   rval.Len,
			"capacity": rval.Cap,
		}

		obj.SetPropertyStr("type", c.NewString("channel"))
		obj.SetPropertyStr("elementType", c.NewString(GetGoTypeName(rtype.Elem())))
		obj.SetPropertyStr("canSend", c.NewBool(canSend))
		obj.SetPropertyStr("canReceive", c.NewBool(canRecv))

		if canSend {
			objMethods["send"] = CreateChannelSendFunc(rval)
		}

		if canRecv {
			objMethods["receive"] = CreateChannelReceiveFunc(rval)
		}

		for name, method := range objMethods {
			// Skip error check since we know the function signatures are correct
			jsMethod, _ := FuncToJS(c, method)
			obj.SetPropertyStr(name, jsMethod)
		}

		return nil
	})
}

// CreateChannelSendFunc creates 'func(T) error' for sending T to a channel.
func CreateChannelSendFunc(chanRValue reflect.Value) any {
	errValue := ErrZeroRValue
	chanElemRType := chanRValue.Type().Elem()
	funcRType := reflect.FuncOf(
		[]reflect.Type{chanElemRType}, // params
		[]reflect.Type{ErrRType},      // returns
		false,                         // variadic
	)

	fn := reflect.MakeFunc(funcRType, func(args []reflect.Value) []reflect.Value {
		valueToSend := args[0]
		chosen, _, _ := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectSend, Chan: chanRValue, Send: valueToSend},
			{Dir: reflect.SelectDefault},
		})

		// Default case was chosen - send would block
		if chosen == 1 {
			errValue = reflect.ValueOf(ErrChanSend)
		}

		return []reflect.Value{errValue}
	})

	// Successful send
	return fn.Interface()
}

// CreateChannelReceiveFunc creates a function for receiving values from a channel.
func CreateChannelReceiveFunc(chanRValue reflect.Value) func() (any, error) {
	return func() (any, error) {
		chosen, recv, ok := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: chanRValue},
			{Dir: reflect.SelectDefault},
		})

		// First: Check default case was chosen - no data available
		if chosen == 1 {
			return nil, ErrChanReceive
		}

		// Receive case was chosen but channel is closed
		if !ok {
			return nil, ErrChanClosed
		}

		// Successful receive
		return recv.Interface(), nil
	}
}

// CreateChannelCloseFunc creates a function for closing a channel.
func CreateChannelCloseFunc(rval reflect.Value) func() error {
	return func() error {
		// Only close if it's a send or bidirectional channel
		if rval.Type().ChanDir() == reflect.RecvDir {
			return ErrChanCloseReceiveOnly
		}

		rval.Close()

		return nil
	}
}
