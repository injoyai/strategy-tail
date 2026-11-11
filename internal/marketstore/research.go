package marketstore

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/tdx/v2/protocol"
)

func (r *MySQLReader) ResearchView() (researchdata.View, error) {
	var exists bool
	err := r.db.QueryRow("SELECT EXISTS(SELECT 1 FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='research_records')").Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("检查 MySQL 研究数据表: %w", err)
	}
	if !exists {
		return researchdata.EmptyView{}, nil
	}
	return researchdata.NewMySQLView(r.db), nil
}

// MembershipUniverse reads date-based membership supplied by the data owner.
func (r *MySQLReader) MembershipUniverse() (researchdata.Universe, error) {
	rows, err := r.db.Query("SELECT code, DATE_FORMAT(listed_at,'%Y-%m-%d'), DATE_FORMAT(delisted_at,'%Y-%m-%d'), available_at FROM universe_members ORDER BY code")
	if missingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询 MySQL 历史股票池: %w", err)
	}
	defer rows.Close()
	var records []researchdata.MembershipRecord
	for rows.Next() {
		var record researchdata.MembershipRecord
		var listed string
		var delisted sql.NullString
		var available int64
		if err := rows.Scan(&record.Code, &listed, &delisted, &available); err != nil {
			return nil, err
		}
		record.ListedAt, err = time.ParseInLocation(time.DateOnly, listed, protocol.Location())
		if err != nil {
			return nil, err
		}
		if delisted.Valid {
			record.DelistedAt, err = time.ParseInLocation(time.DateOnly, delisted.String, protocol.Location())
			if err != nil {
				return nil, err
			}
		}
		if available > 0 {
			record.AvailableAt = time.Unix(0, available)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return researchdata.NewMembershipUniverse(records)
}
