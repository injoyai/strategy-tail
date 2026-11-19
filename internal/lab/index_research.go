package lab

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/extend/pull"
	"github.com/injoyai/tdx/v2/protocol"
)

type concentrationPoint struct {
	Share    *float64 `json:"share"`
	Selected int      `json:"selected"`
	Shanghai int      `json:"shanghai"`
	Shenzhen int      `json:"shenzhen"`
	Beijing  int      `json:"beijing"`
	Excluded int      `json:"excluded"`
}

type researchIndex struct {
	Code string `json:"code"`
	Name string `json:"name,omitempty"`
}

type indexResearchPoint struct {
	Date          string              `json:"date"`
	Close         *float64            `json:"close"`
	RSI           *float64            `json:"rsi"`
	Concentration *concentrationPoint `json:"concentration"`
}

func concentrationForDay(rows []marketstore.StockTurnover, topPercent int) concentrationPoint {
	point := concentrationPoint{}
	amounts := make([]float64, 0, len(rows))
	for _, row := range rows {
		if !protocol.IsStock(row.Code) {
			continue
		}
		if !positiveFinite(row.Amount) {
			point.Excluded++
			continue
		}
		switch strings.ToLower(row.Code[:2]) {
		case "sh":
			point.Shanghai++
		case "sz":
			point.Shenzhen++
		case "bj":
			point.Beijing++
		}
		amounts = append(amounts, row.Amount)
	}
	slices.Sort(amounts)
	point.Selected = (len(amounts)*topPercent + 99) / 100
	var subtotal, total float64
	for i, amount := range amounts {
		if i >= len(amounts)-point.Selected {
			subtotal += amount
		}
		total += amount
	}
	if positiveFinite(total) {
		share := subtotal / total
		point.Share = &share
	}
	return point
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsInf(value, 0)
}

func (s *Server) handleIndexResearchCatalog(w http.ResponseWriter, _ *http.Request) {
	market := common.Pull
	if market == nil {
		writeErr(w, http.StatusServiceUnavailable, "行情库尚未连接")
		return
	}
	codes, err := market.LocalIndexCodes()
	if err != nil {
		log.Printf("[lab] 读取指数目录: %v", err)
		writeErr(w, http.StatusServiceUnavailable, "指数目录读取失败，请重试")
		return
	}
	indexes := make([]researchIndex, 0, len(codes))
	for _, code := range codes {
		index := researchIndex{Code: code.Code}
		if meta := market.Metadata(); meta != nil {
			index.Name = meta.Codes.GetName(code.Code)
		}
		if index.Name == "" {
			switch code.Code {
			case "sh000001":
				index.Name = "上证指数"
			case "sh000300":
				index.Name = "沪深300"
			}
		}
		indexes = append(indexes, index)
	}
	slices.SortFunc(indexes, func(a, b researchIndex) int { return strings.Compare(a.Code, b.Code) })
	writeJSON(w, indexes)
}

func parseIndexResearch(q url.Values) (string, time.Time, time.Time, int, error) {
	code := q.Get("index")
	index, err := pull.ParseCode(code)
	if err != nil || index.Market != protocol.MarketIndex || index.Code != code {
		return "", time.Time{}, time.Time{}, 0, fmt.Errorf("请选择行情库中的指数")
	}
	loc := protocol.Location()
	from, fromErr := time.ParseInLocation(time.DateOnly, q.Get("from"), loc)
	to, toErr := time.ParseInLocation(time.DateOnly, q.Get("to"), loc)
	if fromErr != nil || toErr != nil || from.After(to) {
		return "", time.Time{}, time.Time{}, 0, fmt.Errorf("请输入有效的起止日期，起始日期须早于或等于结束日期")
	}
	top, err := strconv.Atoi(q.Get("topPercent"))
	if err != nil || top < 1 || top > 100 {
		return "", time.Time{}, time.Time{}, 0, fmt.Errorf("前 N%% 请输入 1 至 100 的整数")
	}
	for _, field := range []string{q.Get("rankBy"), q.Get("shareOf")} {
		if field != "" && field != "amount" {
			return "", time.Time{}, time.Time{}, 0, fmt.Errorf("成交拥挤度按成交额排序并统计成交额占比")
		}
	}
	return code, from, to, top, nil
}

func (s *Server) handleIndexResearchSeries(w http.ResponseWriter, r *http.Request) {
	code, from, to, topPercent, err := parseIndexResearch(r.URL.Query())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	market := common.Pull
	if market == nil {
		writeErr(w, http.StatusServiceUnavailable, "行情库尚未连接")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	points, err := s.indexResearch.query(ctx, market, code, from, to, topPercent, time.Now())
	if err != nil {
		if r.Context().Err() == nil {
			log.Printf("[lab] 查询指数研究 (%s): %v", code, err)
			writeErr(w, http.StatusServiceUnavailable, "指数研究数据读取或缓存失败，请重试或缩短日期范围")
		}
		return
	}
	writeJSON(w, points)
}

func readIndexResearch(ctx context.Context, market *extend.MarketReader, code string, from, to time.Time, topPercent int) ([]indexResearchPoint, error) {
	bars, err := market.DayKlines(code, from, to)
	if err != nil {
		return nil, err
	}
	byDate := make(map[string]indexResearchPoint, len(bars))
	for _, bar := range bars {
		date := bar.Time.Format(time.DateOnly)
		point := indexResearchPoint{Date: date}
		if positiveFinite(bar.Close) {
			point.Close = &bar.Close
		}
		byDate[date] = point
	}
	var date string
	var day []marketstore.StockTurnover
	flush := func() {
		point := byDate[date]
		point.Date = date
		value := concentrationForDay(day, topPercent)
		point.Concentration = &value
		byDate[date] = point
	}
	err = market.ReadStockTurnover(ctx, from.Format(time.DateOnly), to.Format(time.DateOnly), func(row marketstore.StockTurnover) {
		if row.Date != date && len(day) > 0 {
			flush()
			day = day[:0]
		}
		date = row.Date
		day = append(day, row)
	})
	if err != nil {
		return nil, err
	}
	if len(day) > 0 {
		flush()
	}
	points := make([]indexResearchPoint, 0, len(byDate))
	for _, point := range byDate {
		points = append(points, point)
	}
	slices.SortFunc(points, func(a, b indexResearchPoint) int { return strings.Compare(a.Date, b.Date) })
	return points, nil
}
