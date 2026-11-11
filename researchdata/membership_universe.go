package researchdata

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const universeDateLayout = "2006-01-02"

// MembershipRecord 一条上市与退市成员记录。
// DelistedAt 零值表示尚未退市；成员资格为闭区间 [ListedAt, DelistedAt]，
// 即退市日当天仍视为成员（保守包含，避免漏掉退市前最后几个交易日）。
// AvailableAt 是本条记录最早可知的时间，用于质量标记与上游 PIT 确认，
// 不参与成员资格判断——成员资格按客观事实回答。
type MembershipRecord struct {
	Code        string
	ListedAt    time.Time
	DelistedAt  time.Time
	AvailableAt time.Time
}

// Validate 拒绝无法回答成员关系的记录。
func (r MembershipRecord) Validate() error {
	if strings.TrimSpace(r.Code) == "" {
		return fmt.Errorf("researchdata: membership code is empty")
	}
	if r.ListedAt.IsZero() {
		return fmt.Errorf("researchdata: membership %q listed time is empty", r.Code)
	}
	if r.AvailableAt.IsZero() {
		return fmt.Errorf("researchdata: membership %q available time is empty", r.Code)
	}
	if !r.DelistedAt.IsZero() && r.DelistedAt.Before(r.ListedAt) {
		return fmt.Errorf("researchdata: membership %q delisted %s precedes listed %s",
			r.Code, r.DelistedAt.Format(universeDateLayout), r.ListedAt.Format(universeDateLayout))
	}
	return nil
}

// member 该代码在 asOf 日（日期粒度）是否为成员。
func (r MembershipRecord) member(asOf time.Time) bool {
	day := dateIn(asOf, r.ListedAt.Location())
	return !day.Before(r.ListedAt) && (r.DelistedAt.IsZero() || !day.After(r.DelistedAt))
}

func dateIn(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// MembershipUniverse is an immutable listing and delisting snapshot.
type MembershipUniverse struct {
	records         map[string]MembershipRecord
	coverageStart   time.Time
	includeDelisted bool
}

// Codes asOf 当日成员，升序去重。
func (u *MembershipUniverse) Codes(asOf time.Time) ([]string, error) {
	out := make([]string, 0, len(u.records))
	for _, rec := range u.records {
		if rec.member(asOf) {
			out = append(out, rec.Code)
		}
	}
	sort.Strings(out)
	return out, nil
}

// CodesBetween 区间内任一天曾是成员的代码并集（含已退市），升序去重。
// 调用方不得把该并集当作每日截面；每日成员仍按 Codes/Contains 过滤。
func (u *MembershipUniverse) CodesBetween(start, end time.Time) ([]string, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("researchdata: universe interval end %s precedes start %s",
			end.Format(universeDateLayout), start.Format(universeDateLayout))
	}
	out := make([]string, 0, len(u.records))
	for _, rec := range u.records {
		// [listed, delisted] 与 [start, end] 相交。
		if rec.ListedAt.After(end) {
			continue
		}
		if !rec.DelistedAt.IsZero() && rec.DelistedAt.Before(dateIn(start, rec.ListedAt.Location())) {
			continue
		}
		out = append(out, rec.Code)
	}
	sort.Strings(out)
	return out, nil
}

// Contains 单代码成员判断；未知代码恒为 false。
func (u *MembershipUniverse) Contains(code string, asOf time.Time) (bool, error) {
	rec, ok := u.records[code]
	if !ok {
		return false, nil
	}
	return rec.member(asOf), nil
}

// Snapshot 质量快照：覆盖日期、是否含退市与显式 PIT 声明。
func (u *MembershipUniverse) Snapshot() UniverseSnapshot {
	return UniverseSnapshot{
		ID:              "mysql_membership",
		Source:          "mysql",
		Mode:            UniverseModeHistoricalMembership,
		CoverageStart:   u.coverageStart.Format(universeDateLayout),
		IncludeDelisted: u.includeDelisted,
		MembershipPIT:   UniversePITUnverified,
		Size:            len(u.records),
	}
}

// NewMembershipUniverse builds a snapshot from rows supplied by the MySQL data owner.
func NewMembershipUniverse(rows []MembershipRecord) (*MembershipUniverse, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("researchdata: 股票池无成员记录")
	}
	u := &MembershipUniverse{records: make(map[string]MembershipRecord, len(rows))}
	for _, rec := range rows {
		if err := rec.Validate(); err != nil {
			return nil, err
		}
		if _, ok := u.records[rec.Code]; ok {
			return nil, fmt.Errorf("researchdata: 重复股票池成员 %s", rec.Code)
		}
		u.records[rec.Code] = rec
		if u.coverageStart.IsZero() || rec.ListedAt.Before(u.coverageStart) {
			u.coverageStart = rec.ListedAt
		}
		if !rec.DelistedAt.IsZero() {
			u.includeDelisted = true
		}
	}
	return u, nil
}
