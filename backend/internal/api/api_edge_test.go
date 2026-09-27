package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// 板刃损伤鉴定全链路：
// 种子单 R-DEMO-B 已有一张「待判定」鉴定 → 确认游客责任（扣减/工单）→ 游客申诉 → 柜台维持；
// 新建单走完整链路：摔倒记录 → 维修间发现缺口暂停出租+自动换发（库存推荐排除暂停件）
// → 判定无责（不扣押金/平台承担维修）→ 修复上架恢复库存。
func TestEdgeDamageAppraisal(t *testing.T) {
	g2 := login(t, "13900000002")
	counter := login(t, "13800000003")
	coach := login(t, "13800000002")
	repair := login(t, "13800000004")

	// 0. 权限：游客不能进入鉴定工作台
	if code, _ := req(t, "GET", "/repair/edge-assessments", g2, nil, nil); code != http.StatusForbidden {
		t.Fatalf("游客访问鉴定列表应 403，实际 %d", code)
	}

	// 1. 维修间看到种子单 EA-DEMO-B（证据包已自动汇总）
	var openList []map[string]interface{}
	req(t, "GET", "/repair/edge-assessments?status=open", repair, nil, &openList)
	var demo map[string]interface{}
	for _, a := range openList {
		if a["code"] == "EA-DEMO-B" {
			demo = a
		}
	}
	if demo == nil {
		t.Fatal("种子数据应包含待判定鉴定 EA-DEMO-B")
	}
	if !strings.Contains(fmt.Sprint(demo["fall_summary"]), "初级道A段") {
		t.Fatalf("摔倒记录摘要应包含课程区域，实际 %v", demo["fall_summary"])
	}
	if cp, _ := demo["checkout_photos"].([]interface{}); len(cp) != 1 {
		t.Fatalf("出库照片应自动带入 1 张，实际 %v", demo["checkout_photos"])
	}
	if demo["suspended_at"] == "" {
		t.Fatal("发起鉴定的雪具应记录暂停出租起始时间")
	}
	eaID := int64(demo["id"].(float64))

	// 2. 证据对比后确认游客责任：板刃 35%（押金 1500 → 扣减 525）
	var confirmed struct {
		Deduction     float64 `json:"deduction"`
		RepairOrderID int64   `json:"repair_order_id"`
		ClaimID       int64   `json:"claim_id"`
		DamageID      int64   `json:"damage_id"`
	}
	code, e := req(t, "POST", fmt.Sprintf("/repair/edge-assessments/%d/confirm", eaID), repair,
		map[string]interface{}{
			"responsibility": "guest_liable", "severity": "medium",
			"staff_finding": "左板刃中段 3cm 缺口，卷边",
		}, &confirmed)
	mustOK(t, code, e["error"])
	if confirmed.Deduction != 525 || confirmed.RepairOrderID == 0 || confirmed.DamageID == 0 {
		t.Fatalf("确认责任后应扣减 525 并生成工单/损伤记录：%+v", confirmed)
	}
	if confirmed.ClaimID != 0 {
		t.Fatalf("R-DEMO-B 无保险，不应生成理赔，实际 %d", confirmed.ClaimID)
	}

	// 3. 游客申诉
	var appeal struct {
		AppealID int64 `json:"appeal_id"`
	}
	code, e = req(t, "POST", "/rentals/2/edge-appeal", g2, map[string]interface{}{
		"assessment_id": eaID, "reason": "缺口是上一个租客留下的",
		"evidence_note": "出库时没有给我看过板刃",
	}, &appeal)
	mustOK(t, code, e["error"])
	// 已申诉不能重复申诉
	if code, _ = req(t, "POST", "/rentals/2/edge-appeal", g2, map[string]interface{}{
		"assessment_id": eaID, "reason": "再申诉一次",
	}, nil); code != http.StatusConflict {
		t.Fatalf("重复申诉应 409，实际 %d", code)
	}

	// 4. 柜台同步看到鉴定，并在申诉队列调课程区域/归还照片后维持原判
	var counterEdges []map[string]interface{}
	req(t, "GET", "/counter/edge-assessments?status=appealed", counter, nil, &counterEdges)
	found := false
	for _, a := range counterEdges {
		if int64(a["id"].(float64)) == eaID {
			found = true
			if !strings.Contains(fmt.Sprint(a["course_zones"]), "初级道") {
				t.Fatalf("柜台应能调出课程区域，实际 %v", a["course_zones"])
			}
		}
	}
	if !found {
		t.Fatal("柜台应同步看到申诉中的鉴定单")
	}
	var appeals []map[string]interface{}
	req(t, "GET", "/counter/appeals", counter, nil, &appeals)
	var appealID int64
	for _, a := range appeals {
		if int64(a["assessment_id"].(float64)) == eaID && a["status"] == "submitted" {
			appealID = int64(a["id"].(float64))
		}
	}
	if appealID == 0 {
		t.Fatal("柜台申诉队列应出现该申诉")
	}
	code, e = req(t, "POST", fmt.Sprintf("/counter/appeals/%d/resolve", appealID), counter,
		map[string]interface{}{"decision": "upheld",
			"note": "出库照片刃口完好、摔倒区域与缺口吻合，维持判责"}, nil)
	mustOK(t, code, e["error"])
	var after map[string]interface{}
	req(t, "GET", fmt.Sprintf("/repair/edge-assessments/%d", eaID), repair, nil, &after)
	if after["status"] != "upheld" {
		t.Fatalf("维持后鉴定状态应为 upheld，实际 %v", after["status"])
	}

	// ---------- 新单：完整「暂停出租 → 无责 → 修复上架」链路 ----------
	g1 := login(t, "13900000001")
	var slots []struct {
		ID        int64 `json:"id"`
		Remaining int   `json:"remaining"`
	}
	req(t, "GET", "/slots?session=day", g1, nil, &slots)
	var slotID int64
	for _, s := range slots {
		if s.Remaining >= 1 {
			slotID = s.ID
			break
		}
	}
	if slotID == 0 {
		t.Skip("无可用日场时段，跳过新单链路")
	}
	var created struct {
		Rentals []struct {
			RentalID int64 `json:"rental_id"`
		} `json:"rentals"`
	}
	req(t, "POST", "/rentals", g1, map[string]interface{}{
		"session_type": "day", "slot_id": slotID,
		"include": map[string]bool{"pole": false, "protector": false},
	}, &created)
	rid := created.Rentals[0].RentalID
	req(t, "POST", fmt.Sprintf("/rentals/%d/confirm", rid), g1, map[string]string{}, nil)
	req(t, "POST", "/counter/checkout", counter, map[string]interface{}{"rental_id": rid}, nil)

	// 教练登记课程区域摔倒记录
	var fall struct {
		ID int64 `json:"id"`
	}
	code, e = req(t, "POST", "/coach/fall-records", coach, map[string]interface{}{
		"rental_id": rid, "zone": "魔毯练习区", "severity": "light",
		"description": "上魔毯时板刃卡了一下",
	}, &fall)
	mustOK(t, code, e["error"])

	// 租赁明细：定位雪板
	var detail struct {
		Items []struct {
			ID            int64  `json:"id"`
			EquipmentType string `json:"equipment_type"`
			QRCode        string `json:"qr_code"`
			ModelID       int64  `json:"model_id"`
		} `json:"items"`
	}
	req(t, "GET", fmt.Sprintf("/rentals/%d", rid), g1, nil, &detail)
	var skiRI int64
	var skiModel int64
	for _, i := range detail.Items {
		if i.EquipmentType == "ski" {
			skiRI, skiModel = i.ID, i.ModelID
		}
	}

	// 维修间发现缺口并发起鉴定（使用中：暂停旧件 + 自动换发）
	var opened struct {
		ID int64 `json:"id"`
	}
	code, e = req(t, "POST", "/repair/edge-assessments", repair, map[string]interface{}{
		"rental_item_id": skiRI, "stage": "during_rental",
		"guest_statement": "我没摔过，可能是石头磕的",
		"staff_finding":   "右板刃小缺口，无卷边",
		"return_photos":   []string{"/uploads/edge-new.png"},
	}, &opened)
	mustOK(t, code, e["error"])

	// 库存推荐：暂停件不计入可用，型号出现 suspended 计数
	var models []map[string]interface{}
	req(t, "GET", "/models?type=ski", counter, nil, &models)
	suspended := 0.0
	for _, m := range models {
		if int64(m["id"].(float64)) == skiModel {
			suspended = m["suspended"].(float64)
		}
	}
	if suspended < 1 {
		t.Fatalf("暂停出租的雪具应在型号库存中体现 suspended>=1，实际 %.0f", suspended)
	}

	// 明细已自动换发一件新雪具
	var afterOpen struct {
		Items []struct {
			EquipmentType string `json:"equipment_type"`
			QRCode        string `json:"qr_code"`
			Status        string `json:"status"`
			Suspended     bool   `json:"suspended"`
		} `json:"items"`
		EdgeAssessments []map[string]interface{} `json:"edge_assessments"`
	}
	req(t, "GET", fmt.Sprintf("/rentals/%d", rid), g1, nil, &afterOpen)
	newSkiOut := false
	for _, i := range afterOpen.Items {
		if i.EquipmentType == "ski" && i.Status == "out" && !i.Suspended {
			newSkiOut = true
		}
	}
	if !newSkiOut {
		t.Fatal("鉴定暂停旧件后应自动换发一件可用雪具继续课程")
	}
	if len(afterOpen.EdgeAssessments) != 1 || afterOpen.EdgeAssessments[0]["status"] != "open" {
		t.Fatalf("游客单据应展示 1 张进行中的鉴定，实际 %+v", afterOpen.EdgeAssessments)
	}

	// 判定无责：不扣押金、不立案，但仍生成维修工单
	var noLiable struct {
		Deduction     float64 `json:"deduction"`
		ClaimID       int64   `json:"claim_id"`
		RepairOrderID int64   `json:"repair_order_id"`
	}
	code, e = req(t, "POST", fmt.Sprintf("/repair/edge-assessments/%d/confirm", opened.ID), repair,
		map[string]interface{}{"responsibility": "not_liable", "severity": "light",
			"conclusion": "出库照片已有同样微小旧痕，非本次造成"}, &noLiable)
	mustOK(t, code, e["error"])
	if noLiable.Deduction != 0 || noLiable.ClaimID != 0 || noLiable.RepairOrderID == 0 {
		t.Fatalf("无责判定应 0 扣减、0 理赔但仍有工单：%+v", noLiable)
	}
	// 无责鉴定不可申诉
	if code, _ = req(t, "POST", fmt.Sprintf("/rentals/%d/edge-appeal", rid), g1, map[string]interface{}{
		"assessment_id": opened.ID, "reason": "test",
	}, nil); code != http.StatusConflict {
		t.Fatalf("无责鉴定不可申诉，实际 %d", code)
	}

	// 维修完成上架：恢复可出租、结束暂停计时
	var roID = noLiable.RepairOrderID
	code, e = req(t, "POST", fmt.Sprintf("/repair/orders/%d/update", roID), repair,
		map[string]interface{}{"status": "done", "repair_cost": 80, "advice": "磨刃完成"}, nil)
	mustOK(t, code, e["error"])
	req(t, "GET", "/models?type=ski", counter, nil, &models)
	for _, m := range models {
		if int64(m["id"].(float64)) == skiModel && m["suspended"].(float64) != 0 {
			// 注：种子 EA-DEMO-B 是另一型号 SKI160，不影响本型号 SKI145
			t.Fatalf("修复上架后该型号暂停数应为 0，实际 %v", m["suspended"])
		}
	}
	var eaNow map[string]interface{}
	req(t, "GET", fmt.Sprintf("/repair/edge-assessments/%d", opened.ID), repair, nil, &eaNow)
	if eaNow["resumed_at"] == "" {
		t.Fatal("修复上架后应记录恢复出租时间")
	}
	if eaNow["status"] != "closed" {
		t.Fatalf("无责鉴定闭环后状态应为 closed，实际 %v", eaNow["status"])
	}
}
