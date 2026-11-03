package rule

import (
	"math"
	"testing"
)

func TestValueConstructors(t *testing.T) {
	cases := []struct {
		name  string
		got   Value
		kind  Kind
		valid bool
		price float64
		float float64
	}{
		{"浮点元价格", PriceValue(12.345), KindPrice, true, 12.345, 0},
		{"浮点有限值", FloatValue(12.5), KindFloat, true, 0.0, 12.5},
		{"浮点零是合法值", FloatValue(0), KindFloat, true, 0.0, 0},
		{"浮点负值", FloatValue(-7.5), KindFloat, true, 0.0, -7.5},
		{"NaN 归无效", FloatValue(math.NaN()), KindInvalid, false, 0.0, 0},
		{"正无穷归无效", FloatValue(math.Inf(1)), KindInvalid, false, 0.0, 0},
		{"负无穷归无效", FloatValue(math.Inf(-1)), KindInvalid, false, 0.0, 0},
		{"显式无效值", InvalidValue(), KindInvalid, false, 0.0, 0},
		{"零值为无效", Value{}, KindInvalid, false, 0.0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got.Kind() != c.kind {
				t.Fatalf("Kind = %v, 期望 %v", c.got.Kind(), c.kind)
			}
			if c.got.Valid() != c.valid {
				t.Fatalf("Valid = %v, 期望 %v", c.got.Valid(), c.valid)
			}
			if c.got.Price() != c.price {
				t.Fatalf("Price = %v, 期望 %v", c.got.Price(), c.price)
			}
			if c.kind == KindFloat && c.got.Float() != c.float {
				t.Fatalf("Float = %v, 期望 %v", c.got.Float(), c.float)
			}
		})
	}
}

func TestValueString(t *testing.T) {
	cases := []struct {
		v    Value
		want string
	}{
		{PriceValue(12.34), "12.34"},
		{FloatValue(12.5), "12.5"},
		{FloatValue(-0.05), "-0.05"},
		{InvalidValue(), "invalid"},
	}
	for _, c := range cases {
		if got := c.v.String(); got != c.want {
			t.Fatalf("String = %q, 期望 %q", got, c.want)
		}
	}
}

func TestV2PriceRejectsNonFinite(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if PriceValue(v).Valid() {
			t.Fatalf("non-finite price accepted: %v", v)
		}
	}
}
