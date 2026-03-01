package api

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRows struct {
	rows   [][]any
	index  int
	closed bool
	err    error
}

func newFakeRows(rows [][]any) *fakeRows {
	return &fakeRows{
		rows:  rows,
		index: -1,
	}
}

func (r *fakeRows) Close() {
	r.closed = true
}

func (r *fakeRows) Err() error {
	return r.err
}

func (r *fakeRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}

func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}

func (r *fakeRows) Next() bool {
	if r.closed {
		return false
	}
	if r.index+1 >= len(r.rows) {
		r.closed = true
		return false
	}
	r.index++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.index < 0 || r.index >= len(r.rows) {
		return errors.New("Scan called without Next")
	}
	values := r.rows[r.index]
	if len(dest) != len(values) {
		return fmt.Errorf("scan dest len = %d, values len = %d", len(dest), len(values))
	}

	for i := range dest {
		if dest[i] == nil {
			continue
		}
		if err := assignScanValue(dest[i], values[i]); err != nil {
			return fmt.Errorf("scan[%d]: %w", i, err)
		}
	}
	return nil
}

func assignScanValue(dest any, value any) error {
	destPtr := reflect.ValueOf(dest)
	if destPtr.Kind() != reflect.Ptr || destPtr.IsNil() {
		return fmt.Errorf("dest must be non-nil pointer, got %T", dest)
	}

	destVal := destPtr.Elem()
	if !destVal.CanSet() {
		return fmt.Errorf("dest is not settable: %T", dest)
	}

	if value == nil {
		destVal.Set(reflect.Zero(destVal.Type()))
		return nil
	}

	// Handle pointer-to-pointer destinations for NULL mapping.
	if destVal.Kind() == reflect.Ptr {
		allocated := reflect.New(destVal.Type().Elem())
		src := reflect.ValueOf(value)
		if !src.Type().AssignableTo(allocated.Elem().Type()) {
			if src.Type().ConvertibleTo(allocated.Elem().Type()) {
				src = src.Convert(allocated.Elem().Type())
			} else {
				return fmt.Errorf("cannot assign %T to %s", value, allocated.Elem().Type())
			}
		}
		allocated.Elem().Set(src)
		destVal.Set(allocated)
		return nil
	}

	src := reflect.ValueOf(value)
	if !src.Type().AssignableTo(destVal.Type()) {
		if src.Type().ConvertibleTo(destVal.Type()) {
			src = src.Convert(destVal.Type())
		} else {
			return fmt.Errorf("cannot assign %T to %s", value, destVal.Type())
		}
	}
	destVal.Set(src)
	return nil
}

func (r *fakeRows) Values() ([]any, error) {
	if r.index < 0 || r.index >= len(r.rows) {
		return nil, errors.New("Values called without Next")
	}
	return r.rows[r.index], nil
}

func (r *fakeRows) RawValues() [][]byte {
	return nil
}

func (r *fakeRows) Conn() *pgx.Conn {
	return nil
}
