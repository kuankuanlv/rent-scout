package models

import "testing"

func TestExtractRentPrice(t *testing.T) {
	cases := []struct {
		title, content, want string
	}{
		{"望京整租月租4500", "", "4500"},
		{"梨园两居", "房东直租，租金：3200，押一付三", "3200"},
		{"回龙观", "房租2800元/月，随时看房", "2800"},
		{"单间", "1500/月 可短租", "1500"},
		{"合租", "租 2000 元水电另算", "2000"},
		{"无价格帖", "近地铁拎包入住", PriceUnknown},
		{"年份干扰", "2024年搬家记录，无租金", PriceUnknown},
		{"区间", "月租3000-3500可谈", "3000"},
	}
	for _, c := range cases {
		got := ExtractRentPrice(c.title, c.content)
		if got != c.want {
			t.Errorf("title=%q content=%q got %q want %q", c.title, c.content, got, c.want)
		}
	}
}

func TestFillPostPriceKeepsExplicit(t *testing.T) {
	p := RentPost{Title: "月租9999", Price: "1234"}
	FillPostPrice(&p)
	if p.Price != "1234" {
		t.Errorf("已有价格不应覆盖: %s", p.Price)
	}
	p2 := RentPost{Title: "月租2600"}
	FillPostPrice(&p2)
	if p2.Price != "2600" {
		t.Errorf("空价格应抽取: %s", p2.Price)
	}
}
