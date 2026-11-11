package marketstore

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/injoyai/strategy-tail/internal/xorms"
	"github.com/injoyai/tdx/v2/extend/model"
)

// SQLiteSource selects the on-disk layout without opening a writable SDK backend.
type SQLiteSource struct {
	root    string
	version int
}

func OpenSQLiteSource(root string) (*SQLiteSource, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	format, err := os.ReadFile(filepath.Join(root, ".time-format"))
	if err != nil {
		return nil, err
	}
	switch string(format) {
	case "source-time-v1\n":
		return &SQLiteSource{root: root, version: 1}, nil
	case "source-time-v2\n":
		return &SQLiteSource{root: root, version: 2}, nil
	default:
		return nil, fmt.Errorf("未知 SQLite 源行情格式 %q", format)
	}
}

func (s *SQLiteSource) Identity() string { return s.root }

func readSourceDay(filename, end string, legacy bool) ([]*DayRecord, error) {
	if _, err := os.Stat(filename); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	db, err := xorms.OpenReadOnly(filename)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	cols := "`Time`,`Open`,`High`,`Low`,`Close`,`Volume`,`Amount`"
	if legacy {
		cols += ",`Turnover`,`FloatStock`,`TotalStock`"
	}
	query := "SELECT " + cols + " FROM `KlineDay`"
	var args []any
	if end != "" {
		query += " WHERE `Time`<=?"
		args = append(args, end)
	}
	rows, err := db.DB().Query(query+" ORDER BY `Time`", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DayRecord
	for rows.Next() {
		r := new(DayRecord)
		dests := []any{&r.Time, &r.Open, &r.High, &r.Low, &r.Close, &r.Volume, &r.Amount}
		if legacy {
			dests = append(dests, &r.Turnover, &r.FloatStock, &r.TotalStock)
		}
		if err := rows.Scan(dests...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func decodeSQLiteName(s string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '~' {
			out.WriteByte(s[i])
			continue
		}
		if i+3 > len(s) {
			return "", fmt.Errorf("非法 SQLite 路径编码 %q", s)
		}
		var b [1]byte
		if _, err := hex.Decode(b[:], []byte(s[i+1:i+3])); err != nil {
			return "", err
		}
		out.WriteByte(b[0])
		i += 2
	}
	return out.String(), nil
}

func readSQLiteCoverage(filename string) (*model.MinuteCoverage, error) {
	db, err := xorms.OpenReadOnly(filename)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	exists, err := db.IsTableExist("minuteCoverage")
	if err != nil || !exists {
		return nil, err
	}
	var r model.MinuteCoverage
	has, err := db.Table("minuteCoverage").Where("ID=1").Get(&r)
	if err != nil || !has {
		return nil, err
	}
	return &r, nil
}
