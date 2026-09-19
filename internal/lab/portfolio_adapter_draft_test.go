package lab

import (
	"errors"
	"testing"
)

// portfolio_adapter_draft_test.go v2 Task 0 适配层草案测试：ValidationInput
// 契约可读写、记录缺失返回明确错误、List 过滤白名单枚举。

func TestValidationStoreAdapterGetAndList(t *testing.T) {
	f := newValidationStoreFixture(t)
	vr, _, err := f.Store.Create(validCreateReq(f, testUUID1), f.deps())
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewValidationStoreAdapter(f.Store)

	// 类型化视图可读（不暴露 v1 JSON 布局）。
	view, err := adapter.Get(vr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Request.ID != vr.ID || view.State != ValidationStateFrozen {
		t.Fatalf("Get 视图不符: id=%s state=%q", view.Request.ID, view.State)
	}

	// List 按证据等级过滤（retrospective 白名单）。
	sums, err := adapter.List(ValidationFilter{EvidenceClass: validationEvidenceRetrospective})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].ID != vr.ID {
		t.Fatalf("List 结果不符: %+v", sums)
	}

	// 缺失记录：明确错误，不 panic。
	if _, err := adapter.Get("fv_20260918T000000000Z_ffffffff"); !errors.Is(err, errValidationNotFound) {
		t.Fatalf("缺失记录应返回 errValidationNotFound: %v", err)
	}
}
