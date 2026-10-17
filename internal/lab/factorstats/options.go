package factorstats

const (
	NeutralizationNone         = "none"
	NeutralizationIndustrySize = "industry_size"
	WinsorizationNone          = "none"
	WinsorizationMAD           = "mad"
	StandardizationRank        = "rank"
	StandardizationZScore      = "zscore"
)

// NeutralizationSpec 可选中性化；industry_size 需要行业与市值数据集标识。
type NeutralizationSpec struct {
	Mode            string `json:"mode"`
	IndustryDataset string `json:"industryDataset,omitempty"`
	SizeDataset     string `json:"sizeDataset,omitempty"`
	Winsorization   string `json:"winsorization"`
	Standardization string `json:"standardization"`
}
