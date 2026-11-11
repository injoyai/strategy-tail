package lab

import "github.com/injoyai/strategy-tail/internal/lab/builder"

// 别名保留 Lab 既有配置与 API 合同，条件构建只在 builder 包实现。
type BuilderSpec = builder.BuilderSpec
type BuilderNode = builder.BuilderNode
type ParamDef = builder.ParamDef
type ComponentEntry = builder.ComponentEntry
type UnavailableComponent = builder.UnavailableComponent
type builderComponents = builder.CatalogResponse

const builderSpecV1 = builder.SpecVersion
