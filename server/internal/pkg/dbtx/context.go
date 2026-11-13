// Package dbtx keeps GORM transaction handles inside infrastructure code while
// allowing repository calls that already accept context to share one database
// transaction. Application code only passes context.Context.
package dbtx

import (
	"context"

	"gorm.io/gorm"
)

type transactionKey struct{}

// WithTransaction attaches an infrastructure-owned transaction to ctx.
func WithTransaction(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, transactionKey{}, tx)
}

// Resolve returns the transaction attached to ctx, or root when none exists.
func Resolve(ctx context.Context, root *gorm.DB) *gorm.DB {
	if ctx != nil {
		if tx, ok := ctx.Value(transactionKey{}).(*gorm.DB); ok && tx != nil {
			return tx
		}
	}
	return root
}

// WithinTransaction reuses an ambient transaction when application commands
// are composed, preserving one connection and commit boundary.
func WithinTransaction(ctx context.Context, root *gorm.DB, callback func(context.Context, *gorm.DB) error) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx, ok := ctx.Value(transactionKey{}).(*gorm.DB); ok && tx != nil {
		if err := callback(ctx, tx.WithContext(ctx)); err != nil {
			return err
		}
		return ctx.Err()
	}
	return root.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := WithTransaction(ctx, tx)
		if err := callback(txCtx, tx); err != nil {
			return err
		}
		return txCtx.Err()
	})
}
