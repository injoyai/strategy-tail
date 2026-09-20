// Package eastmoney normalizes Eastmoney public historical valuation data into
// the provider-neutral researchdata contracts.
package eastmoney

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/injoyai/strategy-tail/researchdata"
)

const (
	ProviderID            = "eastmoney"
	DatasetValuationDaily = "valuation.daily"
	defaultBaseURL        = "https://datacenter-web.eastmoney.com/api/data/v1/get"
)

var valuationSpec = researchdata.DatasetSpec{
	ID:          DatasetValuationDaily,
	Kind:        researchdata.KindFundamental,
	Source:      ProviderID,
	Version:     "1",
	Description: "A股日频历史估值与市值快照；供应商历史接口无修订日志，PIT 证据标记为未验证",
	Fields: []researchdata.FieldSpec{
		{Name: "pe_ttm", Unit: "multiple", Description: "滚动市盈率（TTM）"},
		{Name: "pe_static", Unit: "multiple", Description: "静态市盈率"},
		{Name: "pb_mrq", Unit: "multiple", Description: "市净率（最近报告期）"},
		{Name: "ps_ttm", Unit: "multiple", Description: "滚动市销率（TTM）"},
		{Name: "pcf_ocf_ttm", Unit: "multiple", Description: "经营现金流口径滚动市现率（TTM）"},
		{Name: "pcf_ocf_static", Unit: "multiple", Description: "经营现金流口径静态市现率"},
		{Name: "peg", Unit: "multiple", Description: "供应商口径 PEG"},
		{Name: "close", Unit: "CNY", Description: "当日收盘价"},
		{Name: "total_market_cap", Unit: "CNY", Description: "总市值"},
		{Name: "float_market_cap", Unit: "CNY", Description: "流通市值"},
		{Name: "total_shares", Unit: "share", Description: "总股本"},
		{Name: "float_shares", Unit: "share", Description: "流通股本"},
	},
}

// Client fetches the historical daily series one security at a time. Codes in
// Query are preserved in normalized records (for example sh600519), while the
// upstream request uses the six-digit security code.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	PageSize   int
	Location   *time.Location
}

func New() *Client {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return &Client{
		BaseURL:    defaultBaseURL,
		HTTPClient: &http.Client{Timeout: 20 * time.Second},
		PageSize:   500,
		Location:   loc,
	}
}

func (c *Client) ID() string { return ProviderID }

func (c *Client) Catalog() []researchdata.DatasetSpec {
	spec := valuationSpec
	spec.Fields = append([]researchdata.FieldSpec(nil), valuationSpec.Fields...)
	return []researchdata.DatasetSpec{spec}
}

func (c *Client) Fetch(ctx context.Context, query researchdata.Query) ([]researchdata.Record, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	if query.Dataset != DatasetValuationDaily {
		return nil, fmt.Errorf("eastmoney: unsupported dataset %q", query.Dataset)
	}
	if len(query.Codes) == 0 {
		return nil, fmt.Errorf("eastmoney: valuation query requires explicit codes")
	}
	end := query.End
	if end.IsZero() || query.AsOf.Before(end) {
		end = query.AsOf
	}
	var out []researchdata.Record
	for _, code := range query.Codes {
		records, err := c.fetchCode(ctx, code, query.Start, end, query.AsOf)
		if err != nil {
			return nil, err
		}
		out = append(out, records...)
	}
	return out, nil
}

func (c *Client) fetchCode(ctx context.Context, code string, start, end, asOf time.Time) ([]researchdata.Record, error) {
	securityCode, err := sixDigitCode(code)
	if err != nil {
		return nil, err
	}
	pageSize := c.PageSize
	if pageSize <= 0 {
		pageSize = 500
	}
	pages := 1
	var out []researchdata.Record
	for page := 1; page <= pages; page++ {
		endpoint, err := c.requestURL(securityCode, start, end, page, pageSize)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "strategy-tail/valuation-sync")
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return nil, fmt.Errorf("eastmoney: fetch %s page %d: %w", code, page, err)
		}
		var payload valuationResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eastmoney: fetch %s page %d: HTTP %d", code, page, resp.StatusCode)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("eastmoney: decode %s page %d: %w", code, page, decodeErr)
		}
		if !payload.Success || payload.Result == nil {
			if payload.Result == nil && payload.Message == "" {
				return out, nil
			}
			return nil, fmt.Errorf("eastmoney: fetch %s page %d failed: %s", code, page, payload.Message)
		}
		if payload.Result.Pages > 0 {
			pages = payload.Result.Pages
		}
		for _, row := range payload.Result.Data {
			record, ok, err := c.normalize(code, securityCode, row, asOf)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (c *Client) requestURL(code string, start, end time.Time, page, pageSize int) (string, error) {
	u, err := url.Parse(c.baseURL())
	if err != nil {
		return "", fmt.Errorf("eastmoney: invalid base URL: %w", err)
	}
	filter := fmt.Sprintf(`(SECURITY_CODE="%s")`, code)
	if !start.IsZero() {
		filter += fmt.Sprintf(`(TRADE_DATE>='%s')`, start.Format(time.DateOnly))
	}
	if !end.IsZero() {
		filter += fmt.Sprintf(`(TRADE_DATE<='%s')`, end.Format(time.DateOnly))
	}
	q := u.Query()
	q.Set("reportName", "RPT_VALUEANALYSIS_DET")
	q.Set("columns", "ALL")
	q.Set("filter", filter)
	q.Set("pageNumber", fmt.Sprintf("%d", page))
	q.Set("pageSize", fmt.Sprintf("%d", pageSize))
	q.Set("sortTypes", "1")
	q.Set("sortColumns", "TRADE_DATE")
	q.Set("source", "WEB")
	q.Set("client", "WEB")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Client) normalize(code, securityCode string, row valuationRow, asOf time.Time) (researchdata.Record, bool, error) {
	if row.SecurityCode != securityCode {
		return researchdata.Record{}, false, fmt.Errorf("eastmoney: requested %s, received code %q", code, row.SecurityCode)
	}
	date, err := time.ParseInLocation("2006-01-02 15:04:05", row.TradeDate, c.location())
	if err != nil {
		return researchdata.Record{}, false, fmt.Errorf("eastmoney: parse %s trade date %q: %w", code, row.TradeDate, err)
	}
	eventAt := time.Date(date.Year(), date.Month(), date.Day(), 15, 0, 0, 0, c.location())
	if eventAt.After(asOf) {
		return researchdata.Record{}, false, fmt.Errorf("eastmoney: %s returned future valuation %s", code, eventAt.Format(time.RFC3339))
	}
	values := make(map[string]float64)
	put := func(name string, value *float64) {
		if value != nil && *value != 0 && !math.IsNaN(*value) && !math.IsInf(*value, 0) {
			values[name] = *value
		}
	}
	put("pe_ttm", row.PETTM)
	put("pe_static", row.PELAR)
	put("pb_mrq", row.PBMRQ)
	put("ps_ttm", row.PSTTM)
	put("pcf_ocf_ttm", row.PCFOCFTTM)
	put("pcf_ocf_static", row.PCFOCFLAR)
	put("peg", row.PEGCAR)
	put("close", row.ClosePrice)
	put("total_market_cap", row.TotalMarketCap)
	put("float_market_cap", row.FloatMarketCap)
	put("total_shares", row.TotalShares)
	put("float_shares", row.FloatShares)
	if len(values) == 0 {
		return researchdata.Record{}, false, nil
	}
	return researchdata.Record{
		Dataset:     DatasetValuationDaily,
		Code:        code,
		Key:         eventAt.Format(time.DateOnly),
		Source:      ProviderID,
		EventAt:     eventAt,
		AvailableAt: eventAt,
		Values:      values,
		Attributes: map[string]string{
			"security_name":      row.SecurityName,
			"industry":           row.BoardName,
			"pit_status":         "unverified_revision_history",
			"availability_basis": "assumed_available_at_trade_close",
		},
	}, true, nil
}

func (c *Client) baseURL() string {
	if strings.TrimSpace(c.BaseURL) == "" {
		return defaultBaseURL
	}
	return c.BaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient == nil {
		return &http.Client{Timeout: 20 * time.Second}
	}
	return c.HTTPClient
}

func (c *Client) location() *time.Location {
	if c.Location == nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return c.Location
}

func sixDigitCode(code string) (string, error) {
	digits := strings.Builder{}
	for _, r := range code {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	value := digits.String()
	if len(value) != 6 {
		return "", fmt.Errorf("eastmoney: invalid stock code %q", code)
	}
	return value, nil
}

type valuationResponse struct {
	Success bool             `json:"success"`
	Message string           `json:"message"`
	Result  *valuationResult `json:"result"`
}

type valuationResult struct {
	Pages int            `json:"pages"`
	Count int            `json:"count"`
	Data  []valuationRow `json:"data"`
}

type valuationRow struct {
	SecurityCode   string   `json:"SECURITY_CODE"`
	SecurityName   string   `json:"SECURITY_NAME_ABBR"`
	BoardName      string   `json:"BOARD_NAME"`
	TradeDate      string   `json:"TRADE_DATE"`
	PETTM          *float64 `json:"PE_TTM"`
	PELAR          *float64 `json:"PE_LAR"`
	PBMRQ          *float64 `json:"PB_MRQ"`
	PSTTM          *float64 `json:"PS_TTM"`
	PCFOCFTTM      *float64 `json:"PCF_OCF_TTM"`
	PCFOCFLAR      *float64 `json:"PCF_OCF_LAR"`
	PEGCAR         *float64 `json:"PEG_CAR"`
	ClosePrice     *float64 `json:"CLOSE_PRICE"`
	TotalMarketCap *float64 `json:"TOTAL_MARKET_CAP"`
	FloatMarketCap *float64 `json:"NOTLIMITED_MARKETCAP_A"`
	TotalShares    *float64 `json:"TOTAL_SHARES"`
	FloatShares    *float64 `json:"FREE_SHARES_A"`
}

var _ researchdata.Provider = (*Client)(nil)
