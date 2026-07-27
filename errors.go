package filtersql

import "errors"

// Sentinel errors returned (wrapped) by Compile and friends. Match them with
// errors.Is to map failures onto HTTP status codes:
//
//	switch {
//	case errors.Is(err, filtersql.ErrUnknownField),
//	     errors.Is(err, filtersql.ErrBadOperator),
//	     errors.Is(err, filtersql.ErrBadValue),
//	     errors.Is(err, filtersql.ErrInvalidCondition):
//	    http.Error(w, err.Error(), http.StatusBadRequest) // 400 — client's filter is malformed
//	default:
//	    http.Error(w, "internal error", http.StatusInternalServerError) // 500
//	}
//
// The four request-domain errors above all mean "the caller sent a bad filter".
// Everything else (join misconfiguration surfaced by Validate, cycles, etc.) is
// a server/config fault.
var (
	// ErrUnknownField: a filter/sort key is not in the registry.
	ErrUnknownField = errors.New("unknown filter field")
	// ErrBadOperator: the operator is not valid for the field's type.
	ErrBadOperator = errors.New("operator not valid for field")
	// ErrBadValue: a value failed coercion or is outside a field's enum.
	ErrBadValue = errors.New("invalid filter value")
	// ErrInvalidCondition: a Condition is malformed (sets more than one form, or
	// mixes WHERE and HAVING fields in one boolean group).
	ErrInvalidCondition = errors.New("invalid condition")
	// ErrTooComplex: the filter blew a configured Limit (depth, condition count,
	// or values per filter). Also a 400-class failure — the payload is abusive.
	ErrTooComplex = errors.New("filter too complex")
)
