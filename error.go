// Package goa standardizes on structured error responses: a request that fails because of
// invalid input or unexpected condition produces a response that contains a structured error.
// Error objects has four keys: a code, a status, a detail and metadata. The code defines the class
// of error (e.g. "invalid_parameter_type") and the status is the corresponding HTTP status
// (e.g. 400). The detail is specific to the error occurrence. The medata provides additional
// values that provide contextual information (name of parameters etc.).
//
// The basic data structure backing errors is Error. Instances of Error can be created via Error
// Class functions. See http://goa.design/implement/error_handling.html
//
// The code generated by goagen calls the helper functions exposed in this file when it encounters
// invalid data (wrong type, validation errors etc.) such as InvalidParamTypeError,
// InvalidAttributeTypeError etc. These methods return errors that get merged with any previously
// encountered error via the Error Merge method.
//
// goa includes an error handler middleware that takes care of mapping back any error returned by
// previously called middleware or action handler into HTTP responses. If the error is an instance
// of Error then the corresponding content including the HTTP status is used otherwise an internal
// error is returned. Errors that bubble up all the way to the top (i.e. not handled by the error
// middleware) also generate an internal error response.
package goa

import (
	"fmt"
	"strings"
)

var (
	// ErrInvalidRequest is the class of errors produced by the generated code when
	// a request parameter or payload fails to validate.
	ErrInvalidRequest = NewErrorClass("invalid_request", 400)

	// ErrInvalidEncoding is the error produced when a request body fails
	// to be decoded.
	ErrInvalidEncoding = NewErrorClass("invalid_encoding", 400)

	// ErrNoSecurityScheme is the error produced when no security scheme has been
	// registered for a name defined in the design.
	ErrNoSecurityScheme = NewErrorClass("no_security_scheme", 500)

	// ErrBadRequest is a generic bad request error.
	ErrBadRequest = NewErrorClass("bad_request", 400)

	// ErrInvalidFile is the error produced by ServeFiles when requested to serve non-existant
	// or non-readable files.
	ErrInvalidFile = NewErrorClass("invalid_file", 400)

	// ErrInternal is the class of error used for non Error.
	ErrInternal = NewErrorClass("internal", 500)
)

type (
	// Error contains the details of a error response.
	Error struct {
		// Code identifies the class of errors for client programs.
		Code string `json:"code" xml:"code"`
		// Status is the HTTP status code used by responses that cary the error.
		Status int `json:"status" xml:"status"`
		// Detail describes the specific error occurrence.
		Detail string `json:"detail" xml:"detail"`
		// MetaValues contains additional key/value pairs useful to clients.
		MetaValues map[string]interface{} `json:"meta,omitempty" xml:"meta,omitempty"`
	}

	// ErrorClass is an error generating function.
	// It accepts a format and values and produces errors with the resulting string.
	// If the format is a string or a Stringer then the string value is used.
	// If the format is an error then the string returned by Error() is used.
	// Otherwise the string produced using fmt.Sprintf("%v") is used.
	ErrorClass func(fm interface{}, v ...interface{}) *Error
)

// NewErrorClass creates a new error class.
// It is the responsability of the client to guarantee uniqueness of code.
func NewErrorClass(code string, status int) ErrorClass {
	return func(fm interface{}, v ...interface{}) *Error {
		var f string
		switch actual := fm.(type) {
		case string:
			f = actual
		case error:
			f = actual.Error()
		case fmt.Stringer:
			f = actual.String()
		default:
			f = fmt.Sprintf("%v", actual)
		}
		return &Error{Code: code, Status: status, Detail: fmt.Sprintf(f, v...)}
	}
}

// InvalidParamTypeError creates a Error with class ID ErrInvalidParamType
func InvalidParamTypeError(name string, val interface{}, expected string) *Error {
	return ErrInvalidRequest("invalid value %#v for parameter %#v, must be a %s", val, name, expected)
}

// MissingParamError creates a Error with class ID ErrMissingParam
func MissingParamError(name string) *Error {
	return ErrInvalidRequest("missing required parameter %#v", name)
}

// InvalidAttributeTypeError creates a Error with class ID ErrInvalidAttributeType
func InvalidAttributeTypeError(ctx string, val interface{}, expected string) *Error {
	return ErrInvalidRequest("type of %s must be %s but got value %#v", ctx, expected, val)
}

// MissingAttributeError creates a Error with class ID ErrMissingAttribute
func MissingAttributeError(ctx, name string) *Error {
	return ErrInvalidRequest("attribute %#v of %s is missing and required", name, ctx)
}

// MissingHeaderError creates a Error with class ID ErrMissingHeader
func MissingHeaderError(name string) *Error {
	return ErrInvalidRequest("missing required HTTP header %#v", name)
}

// InvalidEnumValueError creates a Error with class ID ErrInvalidEnumValue
func InvalidEnumValueError(ctx string, val interface{}, allowed []interface{}) *Error {
	elems := make([]string, len(allowed))
	for i, a := range allowed {
		elems[i] = fmt.Sprintf("%#v", a)
	}
	return ErrInvalidRequest("value of %s must be one of %s but got value %#v", ctx, strings.Join(elems, ", "), val)
}

// InvalidFormatError creates a Error with class ID ErrInvalidFormat
func InvalidFormatError(ctx, target string, format Format, formatError error) *Error {
	return ErrInvalidRequest("%s must be formatted as a %s but got value %#v, %s", ctx, format, target, formatError.Error())
}

// InvalidPatternError creates a Error with class ID ErrInvalidPattern
func InvalidPatternError(ctx, target string, pattern string) *Error {
	return ErrInvalidRequest("%s must match the regexp %#v but got value %#v", ctx, pattern, target)
}

// InvalidRangeError creates a Error with class ID ErrInvalidRange
func InvalidRangeError(ctx string, target interface{}, value int, min bool) *Error {
	comp := "greater or equal"
	if !min {
		comp = "lesser or equal"
	}
	return ErrInvalidRequest("%s must be %s than %d but got value %#v", ctx, comp, value, target)
}

// InvalidLengthError creates a Error with class ID ErrInvalidLength
func InvalidLengthError(ctx string, target interface{}, ln, value int, min bool) *Error {
	comp := "greater or equal"
	if !min {
		comp = "lesser or equal"
	}
	return ErrInvalidRequest("length of %s must be %s than %d but got value %#v (len=%d)", ctx, comp, value, target, ln)
}

// NoSecurityScheme creates a Error with class ID ErrNoSecurityScheme
func NoSecurityScheme(schemeName string) *Error {
	return ErrNoSecurityScheme("invalid security scheme %s", schemeName)
}

// Error returns the error occurrence details.
func (e *Error) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Detail)
}

// Meta adds to the error metadata.
func (e *Error) Meta(keyvals ...interface{}) *Error {
	for i := 0; i < len(keyvals); i += 2 {
		k := keyvals[i]
		var v interface{} = "MISSING"
		if i+1 < len(keyvals) {
			v = keyvals[i+1]
		}
		e.MetaValues[fmt.Sprintf("%v", k)] = v
	}
	return e
}

// MergeErrors updates an error by merging another into it. It first converts other into an Error
// if not already one - producing an internal error in that case. The merge algorithm is then:
//
// * If any of e or other is an internal error then the result is an internal error
//
// * If the status or code of e and other don't match then the result is a 400 "bad_request"
//
// The Detail field is updated by concatenating the Detail fields of e and other separated
// by a newline. The MetaValues field of is updated by merging the map of other MetaValues
// into e's where values in e with identical keys to values in other get overwritten.
//
// Merge returns the updated error. This is useful in case the error was initially nil in
// which case other is returned.
func MergeErrors(err, other error) error {
	if err == nil {
		if other == nil {
			return nil
		}
		return asError(other)
	}
	if other == nil {
		return asError(err)
	}
	e := asError(err)
	o := asError(other)
	switch {
	case e.Status == 500 || o.Status == 500:
		if e.Status != 500 {
			e.Status = 500
			e.Code = "internal_error"
		}
	case e.Status != o.Status || e.Code != o.Code:
		e.Status = 400
		e.Code = "bad_request"
	}
	e.Detail = e.Detail + "\n" + o.Detail
	for n, v := range o.MetaValues {
		e.MetaValues[n] = v
	}
	return e
}

func asError(err error) *Error {
	e, ok := err.(*Error)
	if !ok {
		return &Error{Status: 500, Code: "internal_error", Detail: err.Error()}
	}
	return e
}
