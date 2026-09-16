package lab

import (
	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

// topNReq 一次横截面快照填充请求。asc 与 A因子TopN.Asc 一致，
// 排名时直接使用，无需解析 key 后缀。
type topNReq struct {
	factor core.Factor
	key    string
	asc    bool
}

// collectTopN 递归遍历全部变体的买入策略树（CompositeBuyer 展开，与
// core diagnose 同款），收集 A因子TopN 所需的快照请求。
//
// Yaegi 脚本复合字面量可能退化为值类型，故 *A因子TopN 与 A因子TopN
// 双断言。Factor==nil 或 N<=0 永不触发买入，跳过。同一 key（因子+方向）
// 的值与排名完全一致，去重后只算一次；保序返回。
func collectTopN(variants []core.Variant) []topNReq {
	seen := map[string]bool{}
	var reqs []topNReq
	var walk func(b core.Buyer)
	walk = func(b core.Buyer) {
		switch t := b.(type) {
		case *sb.A因子TopN:
			if t.Factor != nil && t.N > 0 {
				key := core.TopNKey(t.Factor.Name(), t.Asc)
				if !seen[key] {
					seen[key] = true
					reqs = append(reqs, topNReq{factor: t.Factor, key: key, asc: t.Asc})
				}
			}
		case sb.A因子TopN:
			walk(&t)
		case core.CompositeBuyer:
			for _, child := range t.Children() {
				walk(child)
			}
		}
	}
	for _, v := range variants {
		walk(v.Buyer)
	}
	return reqs
}
