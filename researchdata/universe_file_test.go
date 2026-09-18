package researchdata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const universeCSVFixture = `code,listed_at,delisted_at,board,available_at,source,version
sh600000,1999-11-10,,main,1999-11-10,manual,v1
sz000001,1991-04-03,,main,1991-04-03,manual,v1
sz000003,1992-04-27,2002-06-21,main,1992-04-27,manual,v1
sh688001,2020-01-01,,sci,2020-01-01,manual,v1
`

func mustLoadUniverse(t *testing.T, name, content string, cfg FileUniverseConfig, strict bool) *FileUniverse {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写临时文件失败: %v", err)
	}
	u, err := LoadUniverseFile(path, cfg, strict)
	if err != nil {
		t.Fatalf("LoadUniverseFile(%s) 失败: %v", name, err)
	}
	return u
}

func assertUniverseCodes(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("codes = %v, want %v", got, want)
	}
}

func TestLoadUniverseFileCSV(t *testing.T) {
	u := mustLoadUniverse(t, "manual_universe.csv", universeCSVFixture,
		FileUniverseConfig{Version: "v1"}, true)

	// 闭区间成员资格：上市日与退市日当天都是成员。
	checks := []struct {
		code string
		asOf time.Time
		want bool
	}{
		{"sh600000", time.Date(2005, 6, 1, 10, 0, 0, 0, time.Local), true},
		{"sz000003", time.Date(2002, 6, 21, 0, 0, 0, 0, time.Local), true},  // 退市日当天
		{"sz000003", time.Date(2002, 6, 22, 0, 0, 0, 0, time.Local), false}, // 退市次日
		{"sh688001", time.Date(2019, 12, 31, 0, 0, 0, 0, time.Local), false},
		{"sh688001", time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local), true},
		{"sz999999", time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local), false}, // 未知代码
	}
	for _, c := range checks {
		got, err := u.Contains(c.code, c.asOf)
		if err != nil {
			t.Fatalf("Contains(%s,%s) 错误: %v", c.code, c.asOf, err)
		}
		if got != c.want {
			t.Fatalf("Contains(%s,%s) = %v, want %v", c.code, c.asOf.Format("2006-01-02"), got, c.want)
		}
	}

	codes, err := u.Codes(time.Date(2000, 6, 1, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("Codes 错误: %v", err)
	}
	assertUniverseCodes(t, codes, "sh600000", "sz000001", "sz000003")

	codes, err = u.Codes(time.Date(2021, 1, 1, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("Codes 错误: %v", err)
	}
	assertUniverseCodes(t, codes, "sh600000", "sh688001", "sz000001")

	// 区间并集包含已退市代码；每日截面仍须按 Contains 过滤。
	codes, err = u.CodesBetween(time.Date(1991, 1, 1, 0, 0, 0, 0, time.Local), time.Date(2021, 1, 1, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("CodesBetween 错误: %v", err)
	}
	assertUniverseCodes(t, codes, "sh600000", "sh688001", "sz000001", "sz000003")

	if _, err := u.CodesBetween(time.Date(2021, 1, 1, 0, 0, 0, 0, time.Local), time.Date(2000, 1, 1, 0, 0, 0, 0, time.Local)); err == nil {
		t.Fatal("区间 end<start 应报错")
	}

	snap := u.Snapshot()
	if err := snap.Validate(); err != nil {
		t.Fatalf("Snapshot 校验失败: %v", err)
	}
	if snap.ID != "manual_universe" || snap.Version != "v1" || snap.Source != "local_file" {
		t.Fatalf("快照标识不符: %+v", snap)
	}
	if snap.Mode != UniverseModeHistoricalMembership {
		t.Fatalf("Mode = %q, want historical_membership", snap.Mode)
	}
	if snap.CoverageStart != "1991-04-03" || snap.CoverageEnd != "2020-01-01" {
		t.Fatalf("覆盖日期 = %s ~ %s", snap.CoverageStart, snap.CoverageEnd)
	}
	if !snap.IncludeDelisted || snap.Size != 4 || snap.MembershipPIT != UniversePITUnverified {
		t.Fatalf("快照质量字段不符: %+v", snap)
	}
	if u.Skipped() != 0 {
		t.Fatalf("严格加载不应跳过行: %d", u.Skipped())
	}
}

func TestLoadUniverseFileJSONL(t *testing.T) {
	content := `{"code":"sh600000","listed_at":"1999-11-10","board":"main","available_at":"1999-11-10","source":"manual","version":"v1"}
{"code":"sz000003","listed_at":"1992-04-27","delisted_at":"2002-06-21","board":"main","available_at":"1992-04-27","source":"manual","version":"v1"}
`
	u := mustLoadUniverse(t, "manual_universe.jsonl", content,
		FileUniverseConfig{ID: "j", PIT: UniversePITVerified}, true)

	ok, err := u.Contains("sz000003", time.Date(2002, 6, 21, 0, 0, 0, 0, time.Local))
	if err != nil || !ok {
		t.Fatalf("退市日当天应为成员: %v, %v", ok, err)
	}
	ok, err = u.Contains("sz000003", time.Date(2002, 6, 22, 0, 0, 0, 0, time.Local))
	if err != nil || ok {
		t.Fatalf("退市次日不应为成员: %v, %v", ok, err)
	}
	codes, err := u.CodesBetween(time.Date(1990, 1, 1, 0, 0, 0, 0, time.Local), time.Date(2010, 1, 1, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("CodesBetween 错误: %v", err)
	}
	assertUniverseCodes(t, codes, "sh600000", "sz000003")

	// 显式 verified 声明透传（前提是记录带 available_at）。
	if snap := u.Snapshot(); snap.MembershipPIT != UniversePITVerified || !snap.IncludeDelisted {
		t.Fatalf("快照 = %+v", snap)
	}
}

func TestLoadUniverseFileUnsupportedExt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUniverseFile(path, FileUniverseConfig{}, true); err == nil {
		t.Fatal("不支持的扩展名应报错")
	}
}

func TestLoadUniverseFileStrictRejects(t *testing.T) {
	header := "code,listed_at,delisted_at,board,available_at,source,version\n"
	cases := []struct {
		name string
		row  string
	}{
		{"重复代码", "sh600000,1999-11-10,,main,1999-11-10,m,v1\nsh600000,2000-01-01,,main,2000-01-01,m,v1\n"},
		{"上市晚于退市", "sh600000,2002-06-21,1999-11-10,main,1999-11-10,m,v1\n"},
		{"缺available_at", "sh600000,1999-11-10,,main,,m,v1\n"},
		{"非法日期", "sh600000,1999/11/10,,main,1999-11-10,m,v1\n"},
		{"缺code", ",1999-11-10,,main,1999-11-10,m,v1\n"},
	}
	for _, tc := range cases {
		path := filepath.Join(t.TempDir(), "u.csv")
		if err := os.WriteFile(path, []byte(header+tc.row), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadUniverseFile(path, FileUniverseConfig{}, true); err == nil {
			t.Fatalf("%s: 严格模式应拒绝", tc.name)
		}
	}
}

func TestLoadUniverseFileLenientSkips(t *testing.T) {
	content := "code,listed_at,delisted_at,board,available_at,source,version\n" +
		"sz000001,1991-04-03,,main,1991-04-03,m,v1\n" + // 有效
		"sz000001,2000-01-01,,main,2000-01-01,m,v1\n" + // 重复：保留首条
		"sh600000,2002-06-21,1999-11-10,main,1999-11-10,m,v1\n" + // listed>delisted
		"sh600001,1999-11-10,,main,,m,v1\n" + // 缺 available_at
		"sh600002,1999/11/10,,main,1999-11-10,m,v1\n" // 非法日期
	u := mustLoadUniverse(t, "u.csv", content, FileUniverseConfig{}, false)

	if u.Skipped() != 4 {
		t.Fatalf("Skipped = %d, want 4", u.Skipped())
	}
	codes, err := u.Codes(time.Date(2005, 1, 1, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("Codes 错误: %v", err)
	}
	assertUniverseCodes(t, codes, "sz000001")
	if snap := u.Snapshot(); snap.Size != 1 {
		t.Fatalf("Size = %d, want 1（仅保留首条）", snap.Size)
	}
}

func TestLoadUniverseOrDefault(t *testing.T) {
	dir := t.TempDir()

	// 文件不存在 → 显式降级到当前静态股票池。
	u, degraded, err := LoadUniverseOrDefault(filepath.Join(dir, "missing.csv"),
		FileUniverseConfig{}, true, func() []string { return []string{"sh600000", "sz000001"} })
	if err != nil {
		t.Fatalf("缺文件应回退而非报错: %v", err)
	}
	if !degraded {
		t.Fatal("缺文件应标记 degraded")
	}
	snap := u.Snapshot()
	if snap.Mode != UniverseModeCurrentStatic || snap.MembershipPIT != UniversePITUnverified {
		t.Fatalf("回退快照 = %+v", snap)
	}
	ok, err := u.Contains("sh600000", time.Date(1990, 1, 1, 0, 0, 0, 0, time.Local))
	if err != nil || !ok {
		t.Fatalf("回退池 Contains = %v, %v; want true", ok, err)
	}

	// 文件存在 → 正常加载，不降级。
	path := filepath.Join(dir, "u.csv")
	if err := os.WriteFile(path, []byte(universeCSVFixture), 0644); err != nil {
		t.Fatal(err)
	}
	u, degraded, err = LoadUniverseOrDefault(path, FileUniverseConfig{}, true, nil)
	if err != nil || degraded {
		t.Fatalf("正常加载 = %v, %v, %v", u, degraded, err)
	}
	if _, ok := u.(*FileUniverse); !ok {
		t.Fatalf("应为 FileUniverse, got %T", u)
	}

	// 文件存在但损坏 → 报错，不得静默降级。
	bad := filepath.Join(dir, "bad.csv")
	if err := os.WriteFile(bad, []byte("code,listed_at\nsh600000,1999/11/10\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadUniverseOrDefault(bad, FileUniverseConfig{}, true, nil); err == nil {
		t.Fatal("坏文件应报错")
	}
}
