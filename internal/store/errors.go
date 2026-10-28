package store

import "errors"

var (
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrExpired         = errors.New("expired")
	ErrUsed            = errors.New("used")
	ErrEmailMismatch   = errors.New("email mismatch")
	ErrInvalid         = errors.New("invalid")
	ErrLastOwner       = errors.New("last owner")
	ErrOpenBills       = errors.New("open bills")
	ErrPaymentTooLarge = errors.New("payment too large")
	ErrProviderUnpaid  = errors.New("provider unpaid")
	ErrHasPayments     = errors.New("has payments")
	ErrRevoked         = errors.New("revoked")
)
