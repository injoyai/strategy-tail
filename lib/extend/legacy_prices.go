package extend

import (
	"database/sql"
	"math"
	"strings"

	"github.com/injoyai/tdx/v2/protocol"
)

// legacyPriceScale identifies existing integer-milli SQLite columns without rewriting the source database.
func legacyPriceScale(q minuteQuery) (bool, error) {
	rows, e := q.Query("PRAGMA table_info(Kline)")
	if e != nil {
		return false, e
	}
	defer rows.Close()
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var def sql.NullString
		if e = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); e != nil {
			return false, e
		}
		if strings.EqualFold(name, "Close") {
			return strings.Contains(strings.ToUpper(typ), "INT"), nil
		}
	}
	return false, rows.Err()
}
func scaleBar(k *protocol.Kline, factor float64) {
	if k == nil {
		return
	}
	if factor == .001 {
		k.Last /= 1000
		k.Open /= 1000
		k.High /= 1000
		k.Low /= 1000
		k.Close /= 1000
		k.Amount /= 1000
		return
	}
	k.Last *= factor
	k.Open *= factor
	k.High *= factor
	k.Low *= factor
	k.Close *= factor
	k.Amount *= factor
}
func normalizeLegacyDays(q minuteQuery, ks Klines) error {
	legacy, e := legacyPriceScale(q)
	if e != nil {
		return e
	}
	if legacy {
		for _, k := range ks {
			scaleBar(k.Kline, .001)
		}
	}
	return nil
}
func legacyWriteBar(k *protocol.Kline, legacy bool) *protocol.Kline {
	if !legacy {
		return k
	}
	copy := *k
	scaleBar(&copy, 1000)
	// Preserve integer affinity and the legacy milli precision after floating conversion.
	copy.Last = math.Round(copy.Last)
	copy.Open = math.Round(copy.Open)
	copy.High = math.Round(copy.High)
	copy.Low = math.Round(copy.Low)
	copy.Close = math.Round(copy.Close)
	copy.Amount = math.Round(copy.Amount)
	return &copy
}
