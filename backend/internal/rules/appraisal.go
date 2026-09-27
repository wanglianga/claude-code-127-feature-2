package rules

// 板刃损伤鉴定规则：维修间对比四方证据（出库照片 / 课程区域 / 摔倒记录 / 游客说明）
// 后认定责任，不同责任对应不同的押金扣减、维修工单与保险理赔结果。

// 责任归属
const (
	RespGuest    = "guest"    // 游客使用责任
	RespPreexist = "preexist" // 出库前已存在（平台承担）
	RespNone     = "none"     // 证据不足 / 正常磨损，无责
)

// EdgeDecision 鉴定结论试算
type EdgeDecision struct {
	Responsibility string  `json:"responsibility"`
	Severity       string  `json:"severity"`
	Deduction      float64 `json:"deduction"`
	ClaimEligible  bool    `json:"claim_eligible"` // 是否触发保险理赔
	CreateRepair   bool    `json:"create_repair"`  // 是否生成维修工单
	Message        string  `json:"message"`
}

// EdgeAppraisal 依据维修间认定的责任方与严重程度给出处置结论：
//   - guest：按板刃损坏矩阵（35%，轻微×0.5/严重×1.5）扣减押金，开工单，有保险自动立案；
//   - preexist：出库照片证明缺口出库前已存在，平台承担维修费，不扣游客押金、不走保险；
//   - none：证据不足或正常磨损，不扣押金、不立案；雪具由维修间核验后决定上架或转维修。
func EdgeAppraisal(responsibility, severity string, deposit float64) EdgeDecision {
	d := EdgeDecision{Responsibility: responsibility}
	if severity != "light" && severity != "heavy" {
		severity = "medium"
	}
	d.Severity = severity
	switch responsibility {
	case RespGuest:
		d.Deduction, _ = DamageDeduction("edge_damage", severity, deposit)
		d.ClaimEligible = true
		d.CreateRepair = true
		d.Message = "游客使用责任：按板刃损伤押金比例扣减，生成维修工单，有保险自动立案"
	case RespPreexist:
		d.CreateRepair = true
		d.Message = "出库前已存在缺口（出库照片为证）：平台承担维修，不扣游客押金，不走保险"
	case RespNone:
		d.Message = "证据不足/正常磨损：不扣押金、不立保险，雪具核验后上架或转维修"
	default:
		d.Message = "待维修间认定责任"
	}
	return d
}

// ResponsibilityLabel 责任方中文标签
func ResponsibilityLabel(resp string) string {
	switch resp {
	case RespGuest:
		return "游客责任"
	case RespPreexist:
		return "出库前已存在"
	case RespNone:
		return "无责"
	default:
		return "待认定"
	}
}
