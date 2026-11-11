// Package xorms opens the application's legacy SQLite databases with their original column names.
package xorms

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/injoyai/tdx/v2/extend/storage/sqlite"
	"xorm.io/xorm"
	"xorm.io/xorm/names"
)

type Engine struct{ *xorm.Engine }

func NewSQLite(filename string) (*Engine, error) {
	db, err := sqlite.Open(filename)
	if err != nil {
		return nil, err
	}
	return &Engine{db}, nil
}

func OpenReadOnly(filename string) (*Engine, error) {
	filename, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(filename), "/"), RawQuery: "mode=ro"}
	db, err := xorm.NewEngine("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMapper(names.SameMapper{})
	db.SetMaxOpenConns(1)
	return &Engine{db}, nil
}

func (e *Engine) SessionFunc(fn func(*xorm.Session) error) error {
	_, err := e.Transaction(func(tx *xorm.Session) (any, error) {
		return nil, fn(tx)
	})
	return err
}
