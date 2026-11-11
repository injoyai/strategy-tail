package marketstore

import (
	"fmt"
	"time"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

func (r *MySQLReader) Codes() ([]*model.CodeModel, error) {
	rows, err := r.db.Query("SELECT `exchange`,`code`,`name`,`multiple`,`decimal`,`last_price` FROM `codes` ORDER BY `exchange`,`code`")
	if missingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询证券目录: %w", err)
	}
	defer rows.Close()
	var out []*model.CodeModel
	for rows.Next() {
		c := new(model.CodeModel)
		if err := rows.Scan(&c.Exchange, &c.Code, &c.Name, &c.Multiple, &c.Decimal, &c.LastPrice); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *MySQLReader) EquityEvents() ([]*model.Gbbq, error) {
	rows, err := r.db.Query("SELECT `code`,DATE_FORMAT(`time`,'%Y-%m-%d %H:%i:%s'),`category`,`c1`,`c2`,`c3`,`c4` FROM `gbbq` ORDER BY `code`,`time`,`id`")
	if missingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询股本事件: %w", err)
	}
	defer rows.Close()
	var out []*model.Gbbq
	for rows.Next() {
		event := new(model.Gbbq)
		var label string
		if err := rows.Scan(&event.Code, &label, &event.Category, &event.C1, &event.C2, &event.C3, &event.C4); err != nil {
			return nil, err
		}
		event.Time, err = time.ParseInLocation(time.DateTime, label, protocol.Location())
		if err != nil {
			return nil, fmt.Errorf("解析 %s 股本事件日期: %w", event.Code, err)
		}
		out = append(out, event)
	}
	return out, rows.Err()
}
