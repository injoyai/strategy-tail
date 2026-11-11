// Source-time validation adapted from injoyai/tdx (MIT); see LICENSE.tdx.
package marketstore

import (
	"fmt"
	"time"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

// parseTime 仅用 UTC 容器进行无时区的日历计算，不把结果当作源时间戳。
func parseTime(value, layout string) (time.Time, error) {
	t, err := time.Parse(layout, value)
	if err != nil || t.Format(layout) != value {
		return time.Time{}, fmt.Errorf("pull: 无效源时间 %q，格式须为 %s", value, layout)
	}
	return t, nil
}

func validateRange(start, end, layout string) error {
	for _, value := range []string{start, end} {
		if value != "" {
			if _, err := parseTime(value, layout); err != nil {
				return err
			}
		}
	}
	if start != "" && end != "" && start > end {
		return fmt.Errorf("pull: 起始时间晚于结束时间")
	}
	return nil
}

func validateDays(from string, rows []*DayRecord) error {
	if err := validateRange(from, "", time.DateOnly); err != nil {
		return err
	}
	for _, k := range rows {
		if k == nil {
			return fmt.Errorf("pull: nil 日线")
		}
		if _, err := parseTime(k.Time, time.DateOnly); err != nil {
			return err
		}
	}
	return nil
}

func validateMinutes(year int, from string, rows []*model.KlineMinute) error {
	if err := validateRange(from, "", protocol.TimeLayout); err != nil {
		return err
	}
	for _, k := range rows {
		if k == nil {
			return fmt.Errorf("pull: nil 分钟线")
		}
		t, err := parseTime(k.Time, protocol.TimeLayout)
		if err != nil {
			return err
		}
		if t.Year() != year {
			return fmt.Errorf("pull: 源时间 %s 不属于 %d 年", k.Time, year)
		}
	}
	return nil
}

func yearToString(year int) string { return fmt.Sprintf("%04d", year) }

func minMaxYear(start, end string) (int, int) {
	startYear, endYear := 0, 9999
	if start != "" {
		fmt.Sscanf(start[:4], "%d", &startYear)
	}
	if end != "" {
		fmt.Sscanf(end[:4], "%d", &endYear)
	}
	return startYear, endYear
}
