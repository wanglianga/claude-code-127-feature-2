package rules

import "testing"

func TestSuggestSkiBeginner(t *testing.T) {
	s := SuggestSki(Profile{HeightCM: 170, WeightKG: 65, Level: "beginner"})
	// 170*0.85 = 144.5 -> 145
	if s.Size != "145cm" {
		t.Fatalf("beginner ski = %s, want 145cm", s.Size)
	}
	if !SkiFitOK(145, 150) || !SkiFitOK(145, 140) {
		t.Fatal("±5cm should fit")
	}
	if SkiFitOK(145, 151) {
		t.Fatal("+6cm should not fit")
	}
}

func TestSuggestSkiAdvancedHeavy(t *testing.T) {
	// 身高180 体重90 -> BMI 27.8 >=26, 高级 0.95: 171+5=176 -> 175
	s := SuggestSki(Profile{HeightCM: 180, WeightKG: 90, Level: "advanced"})
	if s.Size != "175cm" {
		t.Fatalf("advanced heavy ski = %s, want 175cm", s.Size)
	}
}

func TestBootFit(t *testing.T) {
	s := SuggestBoot(Profile{FootEU: 42})
	if s.Size != "EU42" {
		t.Fatalf("boot = %s", s.Size)
	}
	if !BootFitOK(42, 42.5) || BootFitOK(42, 43) {
		t.Fatal("boot tolerance ±0.5")
	}
	s2 := SuggestBoot(Profile{FootEU: 42.5})
	if s2.Size != "EU42.5" {
		t.Fatalf("boot half size = %s", s2.Size)
	}
}

func TestHelmetSizes(t *testing.T) {
	cases := map[int]string{50: "S", 56: "M", 59: "L", 62: "XL", 0: "M"}
	for head, want := range cases {
		if got, _ := helmetSize(head); got != want {
			t.Fatalf("head %d = %s want %s", head, got, want)
		}
	}
}

func TestPole(t *testing.T) {
	s := SuggestPole(Profile{HeightCM: 170})
	// 170*0.7=119 -> 120
	if s.Size != "120cm" {
		t.Fatalf("pole = %s want 120cm", s.Size)
	}
	b := SuggestPole(Profile{HeightCM: 170, Level: "beginner"})
	if !b.Optional {
		t.Fatal("beginner pole should be optional")
	}
}

func TestProtector(t *testing.T) {
	if SuggestProtector(Profile{HeightCM: 180, WeightKG: 70}).Size != "XL" {
		t.Fatal("180cm -> XL")
	}
	if SuggestProtector(Profile{HeightCM: 140, WeightKG: 50}).Size != "S" {
		t.Fatal("140cm -> S")
	}
	if SuggestProtector(Profile{HeightCM: 160, WeightKG: 55}).Size != "M" {
		t.Fatal("160cm/55kg -> M")
	}
	if SuggestProtector(Profile{HeightCM: 170, WeightKG: 70}).Size != "L" {
		t.Fatal("170cm/70kg -> L")
	}
}

func TestAllSuggestionsCount(t *testing.T) {
	if n := len(AllSuggestions(Profile{HeightCM: 170, WeightKG: 65, FootEU: 42, HeadCM: 57, Level: "beginner"})); n != 5 {
		t.Fatalf("got %d suggestions, want 5", n)
	}
}

func TestDamageDeduction(t *testing.T) {
	d, r := DamageDeduction("edge_damage", "medium", 1000)
	if d != 350 || r.RetireItem {
		t.Fatalf("edge medium = %v retire=%v", d, r.RetireItem)
	}
	d, _ = DamageDeduction("edge_damage", "light", 1000)
	if d != 175 {
		t.Fatalf("edge light = %v want 175", d)
	}
	d, _ = DamageDeduction("edge_damage", "heavy", 1000)
	if d != 525 {
		t.Fatalf("edge heavy = %v want 525", d)
	}
	d, r = DamageDeduction("lost", "lost", 800)
	if d != 800 || !r.RetireItem {
		t.Fatalf("lost = %v", d)
	}
	d, _ = DamageDeduction("shell_crack", "heavy", 1000)
	if d != 750 { // 0.5*1.5 capped below deposit
		t.Fatalf("crack heavy = %v want 750", d)
	}
}

func TestClaim(t *testing.T) {
	// 覆盖：扣 350，免赔 100，上限 1000 -> 赔 250
	c := CalcClaim(350, 100, 1000, true)
	if c.Payout != 250 {
		t.Fatalf("payout = %v want 250", c.Payout)
	}
	// 低于免赔
	c = CalcClaim(50, 100, 1000, true)
	if c.Payout != 0 {
		t.Fatalf("low claim payout = %v want 0", c.Payout)
	}
	// 超保额
	c = CalcClaim(1200, 100, 500, true)
	if c.Payout != 500 {
		t.Fatalf("capped payout = %v want 500", c.Payout)
	}
	// 不覆盖
	c = CalcClaim(350, 0, 1000, false)
	if c.Payout != 0 || c.Covered {
		t.Fatal("uncovered claim must not pay")
	}
}

func TestLateFeeAndNight(t *testing.T) {
	if LateFee(2, 400) != 200 {
		t.Fatalf("late fee = %v want 200", LateFee(2, 400))
	}
	if LateFee(0, 400) != 0 {
		t.Fatal("zero hours = zero fee")
	}
}

func TestEdgeAppraisalDecision(t *testing.T) {
	// 游客责任：板刃 35%，押金 1500 -> 525；开工单、可立案
	d := EdgeAppraisal("guest", "medium", 1500)
	if d.Deduction != 525 || !d.CreateRepair || !d.ClaimEligible {
		t.Fatalf("guest medium decision = %+v", d)
	}
	// 轻微 ×0.5 -> 262.5
	d = EdgeAppraisal("guest", "light", 1500)
	if d.Deduction != 262.5 {
		t.Fatalf("guest light deduction = %v want 262.5", d.Deduction)
	}
	// 严重 ×1.5 -> 787.5
	d = EdgeAppraisal("guest", "heavy", 1500)
	if d.Deduction != 787.5 {
		t.Fatalf("guest heavy deduction = %v want 787.5", d.Deduction)
	}
	// 出库前已存在：不扣、不修保险，但开工单由平台承担
	d = EdgeAppraisal("preexist", "medium", 1500)
	if d.Deduction != 0 || d.ClaimEligible || !d.CreateRepair {
		t.Fatalf("preexist decision = %+v", d)
	}
	// 无责：不扣、不立案
	d = EdgeAppraisal("none", "medium", 1500)
	if d.Deduction != 0 || d.ClaimEligible || d.CreateRepair {
		t.Fatalf("none decision = %+v", d)
	}
	if ResponsibilityLabel("guest") != "游客责任" || ResponsibilityLabel("preexist") != "出库前已存在" {
		t.Fatal("responsibility labels wrong")
	}
}
