package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime/debug"
	"slices"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// These revisions are embedded by the Docker build because its context omits .git.
var appRevision, tdxRevision string

type BuildVersions struct {
	Application string `json:"application"`
	TDX         string `json:"tdx"`
}

func readBuildVersions() BuildVersions {
	v := BuildVersions{Application: "unknown", TDX: "unknown"}
	if b, ok := debug.ReadBuildInfo(); ok {
		if b.Main.Version != "" && b.Main.Version != "(devel)" {
			v.Application = b.Main.Version
		}
		dirty := false
		for _, setting := range b.Settings {
			switch setting.Key {
			case "vcs.revision":
				v.Application = setting.Value
			case "vcs.modified":
				dirty = setting.Value == "true"
			}
		}
		if dirty {
			v.Application += "+dirty"
		}
		for _, dep := range b.Deps {
			if dep.Path != "github.com/injoyai/tdx/v2" {
				continue
			}
			if dep.Replace != nil {
				dep = dep.Replace
			}
			if dep.Version != "" && dep.Version != "(devel)" {
				v.TDX = dep.Version
			}
			break
		}
	}
	if appRevision != "" {
		v.Application = appRevision
	}
	if tdxRevision != "" {
		v.TDX = tdxRevision
	}
	return v
}

type RunInputs struct {
	Build         BuildVersions                    `json:"build"`
	Data          DataProvenance                   `json:"data"`
	Codes         []string                         `json:"codes"`
	Calendar      []string                         `json:"calendar,omitempty"`
	CalendarBasis string                           `json:"calendarBasis,omitempty"`
	Costs         *core.Cost                       `json:"costs,omitempty"`
	Execution     *portfolioresearch.ExecutionSpec `json:"execution,omitempty"`
	DailyInputSHA string                           `json:"dailyInputSha256,omitempty"`
}

func (r *Runner) runInputs(codes, dates []string, basis string) *RunInputs {
	ordered := slices.Clone(codes)
	slices.Sort(ordered)
	return &RunInputs{Build: readBuildVersions(), Data: r.dataProvenance(), Codes: slices.Compact(ordered),
		Calendar: slices.Clone(dates), CalendarBasis: basis}
}

func marketInputHash(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return "" // Do not publish a partial fingerprint for nonfinite data.
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func portfolioInputHash(data *portfolioRunData) string {
	type prices struct {
		Open, Close, Previous map[string]float64
		Volume                map[string]int64
	}
	hashes := make(map[string]string, len(data.dates))
	for _, day := range data.dates {
		hashes[day] = marketInputHash(prices{data.open[day], data.close[day], data.stockLast[day], data.volume[day]})
	}
	return marketInputHash(hashes)
}

func factorDailyInputHash(d researchrun.YearData, eventDays []string) string {
	return marketInputHash(struct {
		Bars   [3]extend.Klines
		Events []string
	}{[3]extend.Klines{d.His, d.Dks, d.Future}, eventDays})
}
