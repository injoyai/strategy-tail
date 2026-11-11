// Package xorms opens the screen service's local trade history database.
package xorms

import (
	"github.com/injoyai/tdx/v2/extend/storage/sqlite"
	"xorm.io/xorm"
)

type Engine struct{ *xorm.Engine }

func NewSQLite(filename string) (*Engine, error) {
	db, err := sqlite.Open(filename)
	if err != nil {
		return nil, err
	}
	return &Engine{db}, nil
}

func (e *Engine) SessionFunc(fn func(*xorm.Session) error) error {
	_, err := e.Transaction(func(tx *xorm.Session) (any, error) {
		return nil, fn(tx)
	})
	return err
}
