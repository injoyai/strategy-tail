package core

// Variant 描述一个命名策略变体：策略实验室脚本一次返回多个，即多组合对比。
type Variant struct {
	Name  string
	Buyer Buyer
}
