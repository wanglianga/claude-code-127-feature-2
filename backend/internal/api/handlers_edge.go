package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"

	"skirental/internal/rules"
)

// 板刃损伤鉴定：维修间发现板刃缺口后，平台汇总出库照片、课程区域、摔倒记录、
// 游客说明进行证据对比；确认责任后押金扣减、维修工单、保险理赔分别生成，
// 同时记录雪具暂停出租时间。游客可申诉，柜台调出课程区域与归还照片复核裁决。

// ---------- 摔倒 / 碰撞记录（教练登记，鉴定客观依据之一） ----------

type fallRecordReq struct {
	RentalID    int64  `json:"rental_id"`
	Zone        string `json:"zone"`
	Severity    string `json:"severity"` // light|medium|heavy
	Description string `json:"description"`
}

func (s *Server) addFallRecord(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	var req fallRecordReq
	if err := decode(r, &req); err != nil || req.RentalID == 0 {
		writeError(w, http.StatusBadRequest, "需要指定租赁单")
		return
	}
	if req.Severity == "" {
		req.Severity = "light"
	}
	if req.Severity != "light" && req.Severity != "medium" && req.Severity != "heavy" {
		writeError(w, http.StatusBadRequest, "摔倒严重程度不正确")
		return
	}
	var guestID, courseCoach int64
	var courseID sql.NullInt64
	err := s.db.QueryRow(`SELECT r.guest_id, r.course_id, COALESCE(co.coach_id,0)
		FROM rentals r LEFT JOIN courses co ON co.id=r.course_id WHERE r.id=$1`,
		req.RentalID).Scan(&guestID, &courseID, &courseCoach)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "租赁单不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if courseCoach != 0 && courseCoach != c.UserID {
		writeError(w, http.StatusForbidden, "这不是你的课程学员")
		return
	}
	var id int64
	if err := s.db.QueryRow(`INSERT INTO fall_records
		(rental_id, course_id, guest_id, zone, severity, description, recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		req.RentalID, courseID, guestID, req.Zone, req.Severity, req.Description, c.UserID).Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id": id, "message": "摔倒/碰撞记录已登记，将作为板刃损伤鉴定的课程区域依据",
	})
}

// 员工查询摔倒记录（柜台申诉复核、维修间鉴定均可调）
func (s *Server) listFallRecords(w http.ResponseWriter, r *http.Request) {
	rentalID := r.URL.Query().Get("rental_id")
	args := []interface{}{}
	q := `SELECT COALESCE(json_agg(t ORDER BY t.occurred_at DESC), '[]') FROM (
		SELECT f.id, f.rental_id, f.zone, f.severity, f.description, f.occurred_at,
		       u.name AS guest_name, ru.name AS recorded_by_name
		FROM fall_records f
		JOIN users u ON u.id=f.guest_id
		LEFT JOIN users ru ON ru.id=f.recorded_by
	`
	if rentalID != "" {
		q += " WHERE f.rental_id=$1"
		args = append(args, rentalID)
	}
	q += ") t"
	s.listJSON(w, q, args...)
}

// ---------- 出库留档照片（扫码出库后逐件拍摄，鉴定对比基准） ----------

type checkoutPhotoReq struct {
	RentalItemID int64  `json:"rental_item_id"`
	URL          string `json:"url"`
}

func (s *Server) addCheckoutPhoto(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	var req checkoutPhotoReq
	if err := decode(r, &req); err != nil || req.RentalItemID == 0 || req.URL == "" {
		writeError(w, http.StatusBadRequest, "需要指定雪具明细与照片地址")
		return
	}
	if !strings.HasPrefix(req.URL, "/uploads/") {
		writeError(w, http.StatusBadRequest, "请先上传照片再登记")
		return
	}
	var itemID int64
	var riStatus string
	err := s.db.QueryRow(`SELECT item_id, status FROM rental_items WHERE id=$1`,
		req.RentalItemID).Scan(&itemID, &riStatus)
	if err == sql.ErrNoRows || itemID == 0 {
		writeError(w, http.StatusNotFound, "租赁明细不存在或尚未分配雪具")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if riStatus != "assigned" && riStatus != "out" {
		writeError(w, http.StatusConflict, "仅已预留或已出库的雪具可以补拍出库照片")
		return
	}
	if _, err := s.db.Exec(`INSERT INTO checkout_photos(rental_item_id, item_id, url, taken_by)
		VALUES ($1,$2,$3,$4)`, req.RentalItemID, itemID, req.URL, c.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"message": "出库留档照片已登记"})
}

// ---------- 鉴定单 ----------

const edgeSelectBase = `
	SELECT ea.id, ea.code, ea.rental_id, ea.rental_item_id, ea.item_id,
	       COALESCE(ea.damage_id,0), ea.stage, ea.checkout_photos, ea.return_photos,
	       ea.course_zones, ea.fall_summary, ea.guest_statement, ea.staff_finding,
	       ea.severity, ea.responsibility, ea.deduction, ea.conclusion, ea.status,
	       COALESCE(ea.repair_order_id,0), COALESCE(ea.claim_id,0), COALESCE(ea.deposit_tx_id,0),
	       to_char(ea.created_at,'MM-DD HH24:MI') AS created_at,
	       to_char(ea.confirmed_at,'MM-DD HH24:MI') AS confirmed_at,
	       r.code AS rental_code, r.session_type, u.name AS guest_name, u.phone AS guest_phone,
	       m.brand, m.name, m.size_label, m.type AS equipment_type, ei.qr_code,
	       ri.deposit,
	       to_char(ei.suspended_at,'MM-DD HH24:MI') AS suspended_at,
	       to_char(ei.resumed_at,'MM-DD HH24:MI') AS resumed_at,
	       COALESCE(ro.status,'') AS repair_status,
	       (SELECT json_agg(json_build_object(
	           'id', a.id, 'reason', a.reason, 'evidence_note', a.evidence_note,
	           'status', a.status, 'counter_note', a.counter_note,
	           'created_at', to_char(a.created_at,'MM-DD HH24:MI'))
	         ORDER BY a.id DESC) FROM assessment_appeals a WHERE a.assessment_id=ea.id) AS appeals
	FROM edge_assessments ea
	JOIN rentals r ON r.id=ea.rental_id
	JOIN users u ON u.id=r.guest_id
	JOIN rental_items ri ON ri.id=ea.rental_item_id
	JOIN equipment_models m ON m.id=ri.model_id
	JOIN equipment_items ei ON ei.id=ea.item_id
	LEFT JOIN repair_orders ro ON ro.id=ea.repair_order_id
`

func scanEdge(row interface {
	Scan(dest ...interface{}) error
}) (map[string]interface{}, error) {
	var (
		id, rentalID, riID, itemID, damageID, repairOrderID, claimID, depositTxID int64
		stage, courseZones, fallSummary, guestStatement, staffFinding             string
		severity, responsibility, conclusion, status, session                     string
		code, rentalCode, guestName, guestPhone, brand, mname, size, etype, qr    string
		createdAt, repairStatus                                                   string
		confirmedAt, suspendedAt, resumedAt                                       sql.NullString
		checkoutPhotos, returnPhotos, appeals                                     []byte
		deduction, itemDeposit                                                    float64
	)
	if err := row.Scan(&id, &code, &rentalID, &riID, &itemID, &damageID, &stage,
		&checkoutPhotos, &returnPhotos, &courseZones, &fallSummary, &guestStatement,
		&staffFinding, &severity, &responsibility, &deduction, &conclusion, &status,
		&repairOrderID, &claimID, &depositTxID, &createdAt, &confirmedAt,
		&rentalCode, &session, &guestName, &guestPhone, &brand, &mname, &size, &etype, &qr,
		&itemDeposit,
		&suspendedAt, &resumedAt, &repairStatus, &appeals); err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"id": id, "code": code, "rental_id": rentalID, "rental_item_id": riID,
		"item_id": itemID, "damage_id": damageID, "stage": stage,
		"checkout_photos": json.RawMessage(orEmptyArray(checkoutPhotos)),
		"return_photos":   json.RawMessage(orEmptyArray(returnPhotos)),
		"course_zones":    courseZones, "fall_summary": fallSummary,
		"guest_statement": guestStatement, "staff_finding": staffFinding,
		"severity": severity, "responsibility": responsibility,
		"deduction": deduction, "conclusion": conclusion, "status": status,
		"repair_order_id": repairOrderID, "claim_id": claimID, "deposit_tx_id": depositTxID,
		"created_at": createdAt, "confirmed_at": confirmedAt.String,
		"rental_code": rentalCode, "session_type": session,
		"guest_name": guestName, "guest_phone": guestPhone,
		"brand": brand, "name": mname, "size_label": size,
		"equipment_type": etype, "qr_code": qr, "deposit": itemDeposit,
		"suspended_at": suspendedAt.String, "resumed_at": resumedAt.String,
		"repair_status": repairStatus,
		"appeals":       json.RawMessage(orEmptyArray(appeals)),
	}, nil
}

func orEmptyArray(b []byte) string {
	if len(b) == 0 || string(b) == "null" {
		return "[]"
	}
	return string(b)
}

func (s *Server) listEdgeAssessments(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	args := []interface{}{}
	q := edgeSelectBase
	if status != "" {
		q += " WHERE ea.status=$1"
		args = append(args, status)
	}
	rows, err := s.db.Query(q+" ORDER BY ea.id DESC", args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]interface{}{}
	for rows.Next() {
		m, err := scanEdge(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getEdgeAssessment(w http.ResponseWriter, r *http.Request) {
	id := urlVar(r, "id")
	row := s.db.QueryRow(edgeSelectBase+" WHERE ea.id=$1", id)
	m, err := scanEdge(row)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "鉴定单不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

type edgeCreateReq struct {
	RentalItemID   int64    `json:"rental_item_id"`
	QRCode         string   `json:"qr_code"` // 维修间可直接扫在租雪板二维码发起鉴定
	Stage          string   `json:"stage"`
	GuestStatement string   `json:"guest_statement"`
	StaffFinding   string   `json:"staff_finding"`
	ReturnPhotos   []string `json:"return_photos"`
	CheckoutPhotos []string `json:"checkout_photos"`
	FallSummary    string   `json:"fall_summary"`
}

// openEdgeAssessment 维修间发现板刃缺口：建立鉴定单并暂停该雪具出租。
// 此阶段只汇总证据、不定责任、不扣押金、不开工单、不立案。
func (s *Server) openEdgeAssessment(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	var req edgeCreateReq
	if err := decode(r, &req); err != nil || (req.RentalItemID == 0 && req.QRCode == "") {
		writeError(w, http.StatusBadRequest, "需要指定发现缺口的雪板明细或二维码")
		return
	}

	tx, _ := s.db.Begin()
	defer tx.Rollback()

	// 支持直接扫雪板二维码发起鉴定
	if req.RentalItemID == 0 {
		if err := tx.QueryRow(`SELECT ri.id FROM equipment_items i
			JOIN rental_items ri ON ri.id=i.current_rental_item_id
			WHERE i.qr_code=$1 AND ri.equipment_type='ski' AND ri.status='out'`,
			req.QRCode).Scan(&req.RentalItemID); err != nil {
			writeError(w, http.StatusBadRequest, "该二维码没有对应的在租雪板")
			return
		}
	}

	var rentalID, itemID, modelID, guestID int64
	var etype, rStatus string
	if err := tx.QueryRow(`SELECT ri.rental_id, ri.item_id, ri.model_id,
		ri.equipment_type, r.status, r.guest_id
		FROM rental_items ri JOIN rentals r ON r.id=ri.rental_id
		WHERE ri.id=$1 FOR UPDATE OF ri`, req.RentalItemID).
		Scan(&rentalID, &itemID, &modelID, &etype, &rStatus, &guestID); err != nil {
		writeError(w, http.StatusNotFound, "租赁明细不存在")
		return
	}
	if etype != "ski" {
		writeError(w, http.StatusBadRequest, "板刃损伤鉴定仅适用于雪板")
		return
	}
	if itemID == 0 {
		writeError(w, http.StatusConflict, "该明细当前没有实物雪具")
		return
	}
	var active int
	tx.QueryRow(`SELECT count(*) FROM edge_assessments WHERE rental_item_id=$1
		AND status IN ('open','confirmed','appealed')`, req.RentalItemID).Scan(&active)
	if active > 0 {
		writeError(w, http.StatusConflict, "该雪板已有进行中的板刃鉴定")
		return
	}
	stage := req.Stage
	if stage == "" {
		if rStatus == "equipped" {
			stage = "during_rental"
		} else {
			stage = "at_return"
		}
	}
	if stage != "during_rental" && stage != "at_return" {
		writeError(w, http.StatusBadRequest, "stage 需为 during_rental 或 at_return")
		return
	}

	// 证据 1：出库留档照片（缺拍时允许人工补传）
	checkout := req.CheckoutPhotos
	if len(checkout) == 0 {
		_ = tx.QueryRow(`SELECT COALESCE(json_agg(url ORDER BY id),'[]'::json)
			FROM checkout_photos WHERE rental_item_id=$1`, req.RentalItemID).Scan(&checkout)
	}
	// 证据 2：课程区域
	var zones string
	tx.QueryRow(`SELECT COALESCE(array_to_string(co.zones, '、'),'')
		FROM rentals r LEFT JOIN courses co ON co.id=r.course_id WHERE r.id=$1`, rentalID).Scan(&zones)
	// 证据 3：摔倒记录摘要
	var fallSummary string
	tx.QueryRow(`SELECT COALESCE(string_agg(
		to_char(occurred_at,'HH24:MI')||' '||COALESCE(NULLIF(zone,''),'未登记区域')||
		'（'||CASE severity WHEN 'light' THEN '轻微' WHEN 'medium' THEN '中等' ELSE '较重' END||'）'||
		COALESCE('：'||NULLIF(description,''),''), '；' ),'')
		FROM fall_records WHERE rental_id=$1`, rentalID).Scan(&fallSummary)
	if fallSummary == "" {
		fallSummary = "本单暂无摔倒/碰撞记录"
	}
	if req.FallSummary != "" {
		fallSummary = fallSummary + "；人工补充：" + req.FallSummary
	}

	returnPhotos, _ := json.Marshal(req.ReturnPhotos)
	checkoutJSON, _ := json.Marshal(checkout)
	code := fmt.Sprintf("EA-%d-%d", time.Now().UnixNano()%1e10, req.RentalItemID)
	var aID int64
	if err := tx.QueryRow(`INSERT INTO edge_assessments
		(code, rental_id, rental_item_id, item_id, stage, checkout_photos, return_photos,
		 course_zones, fall_summary, guest_statement, staff_finding, status,
		 responsibility, opened_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'open','pending',$12) RETURNING id`,
		code, rentalID, req.RentalItemID, itemID, stage,
		string(checkoutJSON), string(returnPhotos), zones, fallSummary,
		req.GuestStatement, req.StaffFinding, c.UserID).Scan(&aID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 维修间确认损伤：雪具立即暂停出租并记录暂停起始时间（影响库存推荐/自动选码）
	tx.Exec(`UPDATE equipment_items SET status='repair', suspended_at=now(),
		suspended_reason='板刃缺口待损伤鉴定', current_rental_item_id=NULL WHERE id=$1`, itemID)
	if stage == "during_rental" {
		// 使用中发现：立即自动换发一件同型号雪具，保证课程继续
		s.replaceRentalItem(tx, req.RentalItemID, itemID, modelID)
	} else {
		// 归还环节发现：明细同步转维修，柜台可直接完成归还结算
		tx.Exec(`UPDATE rental_items SET status='repair' WHERE id=$1`, req.RentalItemID)
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id": aID, "code": code,
		"message": "板刃损伤鉴定已建立，雪具已暂停出租；请对比出库照片/课程区域/摔倒记录/游客说明后确认责任",
	})
}

type edgeConfirmReq struct {
	Responsibility string  `json:"responsibility"` // guest_liable|not_liable
	Severity       string  `json:"severity"`
	Conclusion     string  `json:"conclusion"`
	StaffFinding   string  `json:"staff_finding"`
	Deduction      float64 `json:"deduction"` // 可选，覆盖规则试算（仍封顶押金）
}

// confirmEdgeAssessment 证据对比完成、确认责任：押金扣减、维修工单、保险理赔分别生成。
func (s *Server) confirmEdgeAssessment(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	id := urlVar(r, "id")
	var req edgeConfirmReq
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Responsibility != "guest_liable" && req.Responsibility != "not_liable" {
		writeError(w, http.StatusBadRequest, "需给出责任判定：游客有责或无责")
		return
	}
	if req.Severity == "" {
		req.Severity = "medium"
	}
	if req.Severity != "light" && req.Severity != "medium" && req.Severity != "heavy" {
		writeError(w, http.StatusBadRequest, "严重程度不正确")
		return
	}

	tx, _ := s.db.Begin()
	defer tx.Rollback()

	var aStatus, stage, staffFinding string
	var rentalID, riID, itemID int64
	if err := tx.QueryRow(`SELECT status, stage, rental_id, rental_item_id, item_id,
		COALESCE(staff_finding,'') FROM edge_assessments WHERE id=$1 FOR UPDATE`,
		id).Scan(&aStatus, &stage, &rentalID, &riID, &itemID, &staffFinding); err != nil {
		writeError(w, http.StatusNotFound, "鉴定单不存在")
		return
	}
	if aStatus != "open" {
		writeError(w, http.StatusConflict, "该鉴定单已确认或已有申诉，不能重复判定")
		return
	}
	if req.StaffFinding != "" {
		staffFinding = req.StaffFinding
	}
	var deposit float64
	if err := tx.QueryRow(`SELECT deposit FROM rental_items WHERE id=$1`,
		riID).Scan(&deposit); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deduction, rule := rules.DamageDeduction("edge_damage", req.Severity, deposit)
	if req.Responsibility == "guest_liable" && req.Deduction > 0 {
		deduction = round2f(req.Deduction)
		if deduction > deposit {
			deduction = deposit
		}
	}
	if req.Responsibility == "not_liable" {
		deduction = 0
	}

	// 归还缺口照片作为损伤取证
	var returnPhotos []byte
	tx.QueryRow(`SELECT return_photos FROM edge_assessments WHERE id=$1`, id).Scan(&returnPhotos)

	// 1) 损伤记录（无责也留痕，计入型号损耗统计；扣减为 0）
	var dID int64
	if err := tx.QueryRow(`INSERT INTO damage_reports
		(rental_item_id, reporter_id, stage, damage_type, severity, description, photos,
		 status, deduction, assessed_by)
		VALUES ($1,$2,$3,'edge_damage',$4,$5,COALESCE($6,'[]'::jsonb),'assessed',$7,$8)
		RETURNING id`,
		riID, c.UserID, stage, req.Severity,
		"板刃鉴定#"+fmt.Sprint(id)+"："+staffFinding, returnPhotos, deduction, c.UserID).Scan(&dID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 2) 维修工单（无论责任归属，板刃缺口都需磨刃/补刃修复后才能恢复出租）
	var roID int64
	tx.QueryRow(`INSERT INTO repair_orders(item_id, damage_id, issue, advice, status, technician_id)
		VALUES ($1,$2,$3,$4,'queued',$5) RETURNING id`,
		itemID, dID, "板刃损伤："+staffFinding, rule.Advice, c.UserID).Scan(&roID)
	tx.Exec(`UPDATE damage_reports SET status='repair_created', repair_order_id=$1 WHERE id=$2`, roID, dID)

	// 3) 押金扣减与保险理赔：仅判定游客有责时生成
	var depositTxID, claimID int64
	conclusion := req.Conclusion
	if req.Responsibility == "guest_liable" {
		if err := tx.QueryRow(`INSERT INTO deposit_transactions
			(rental_id, type, amount, damage_id, note, created_by)
			VALUES ($1,'deduct_damage',$2,$3,$4,$5) RETURNING id`,
			rentalID, deduction, dID,
			fmt.Sprintf("板刃损伤鉴定确认责任：%s 按押金比例 %.0f%% 扣减", rule.Label, rule.DeductRate*100),
			c.UserID).Scan(&depositTxID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		claimID = s.autoFileClaim(tx, rentalID, dID, deduction)
		if conclusion == "" {
			conclusion = "证据对比后判定游客担责：出库照片刃口完好，课程区域/摔倒记录与缺口位置吻合"
		}
	} else {
		if conclusion == "" {
			conclusion = "证据不足或缺口非游客责任，押金不予扣减；维修成本由平台承担"
		}
		tx.Exec(`UPDATE damage_reports SET status='closed' WHERE id=$1`, dID)
	}
	// 明细与雪具状态在发起鉴定时已处理（使用中自动换发 / 归还时转维修），此处不变更

	eaStatus := "confirmed"
	if req.Responsibility == "not_liable" {
		eaStatus = "closed"
	}
	tx.Exec(`UPDATE edge_assessments SET damage_id=$1, repair_order_id=$2, claim_id=NULLIF($3,0),
		deposit_tx_id=NULLIF($4,0), severity=$5, responsibility=$6, deduction=$7, conclusion=$8,
		status=$9, staff_finding=$10, confirmed_by=$11, confirmed_at=now(), updated_at=now()
		WHERE id=$12`,
		dID, roID, claimID, depositTxID, req.Severity, req.Responsibility, deduction,
		conclusion, eaStatus, staffFinding, c.UserID, id)

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "责任已确认：维修工单已生成" +
			map[bool]string{true: "，押金扣减与保险理赔已分别生成", false: "（平台承担维修费用，不扣押金）"}[req.Responsibility == "guest_liable"],
		"damage_id": dID, "repair_order_id": roID, "claim_id": claimID,
		"deposit_tx_id": depositTxID, "deduction": deduction,
	})
}

// autoFileClaim 有效保单且覆盖该损坏类型时自动立案，返回理赔单 ID（无则 0）。
func (s *Server) autoFileClaim(tx *sql.Tx, rentalID, dID int64, deduction float64) int64 {
	var productID, policyID int64
	if err := tx.QueryRow(`SELECT ip.product_id, ip.id FROM insurance_policies ip
		WHERE ip.rental_id=$1 AND ip.status='active' LIMIT 1`, rentalID).
		Scan(&productID, &policyID); err != nil {
		return 0
	}
	var deductible, capAmt float64
	var covers pq.StringArray
	if err := tx.QueryRow(`SELECT deductible, coverage_cap, covers FROM insurance_products WHERE id=$1`,
		productID).Scan(&deductible, &capAmt, &covers); err != nil {
		return 0
	}
	covered := false
	for _, cv := range covers {
		if cv == "edge_damage" {
			covered = true
			break
		}
	}
	res := rules.CalcClaim(deduction, deductible, capAmt, covered)
	var claimID int64
	tx.QueryRow(`INSERT INTO insurance_claims
		(policy_id, damage_id, amount_requested, deductible, amount_approved, status, note)
		VALUES ($1,$2,$3,$4,$5,'filed',$6) RETURNING id`,
		policyID, dID, res.Requested, deductible, res.Approved,
		"板刃损伤鉴定自动立案："+res.Message).Scan(&claimID)
	tx.Exec(`UPDATE insurance_policies SET status='claimed' WHERE id=$1`, policyID)
	return claimID
}

// replaceRentalItem 中途损伤收回旧件后，自动换发一件同型号可用雪具。
func (s *Server) replaceRentalItem(tx *sql.Tx, riID, oldItemID, modelID int64) {
	var newItem int64
	if err := tx.QueryRow(`SELECT id FROM equipment_items
		WHERE model_id=$1 AND status='available' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`,
		modelID).Scan(&newItem); err != nil {
		tx.Exec(`UPDATE rental_items SET item_id=NULL WHERE id=$1`, riID)
		return
	}
	tx.Exec(`UPDATE equipment_items SET status='out', rental_count=rental_count+1,
		current_rental_item_id=$1 WHERE id=$2`, riID, newItem)
	tx.Exec(`UPDATE rental_items SET item_id=$1, status='out',
		exchanged_from_item_id=COALESCE(exchanged_from_item_id,$2) WHERE id=$3`,
		newItem, oldItemID, riID)
}

// ---------- 游客申诉 ----------

type appealReq struct {
	AssessmentID int64  `json:"assessment_id"`
	Reason       string `json:"reason"`
	EvidenceNote string `json:"evidence_note"`
}

func (s *Server) appealAssessment(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	rid := urlVar(r, "id")
	if !s.ownRental(w, r, rid, c.UserID) {
		return
	}
	var req appealReq
	if err := decode(r, &req); err != nil || req.AssessmentID == 0 || strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusBadRequest, "请填写申诉理由")
		return
	}
	tx, _ := s.db.Begin()
	defer tx.Rollback()
	var aRental int64
	var aStatus, responsibility string
	if err := tx.QueryRow(`SELECT rental_id, status, responsibility FROM edge_assessments
		WHERE id=$1 FOR UPDATE`, req.AssessmentID).Scan(&aRental, &aStatus, &responsibility); err != nil {
		writeError(w, http.StatusNotFound, "鉴定单不存在")
		return
	}
	if aRental != rid {
		writeError(w, http.StatusBadRequest, "申诉单与租赁单不匹配")
		return
	}
	if aStatus != "confirmed" || responsibility != "guest_liable" {
		writeError(w, http.StatusConflict, "仅已判定游客有责且未申诉的鉴定可以申诉")
		return
	}
	var open int
	tx.QueryRow(`SELECT count(*) FROM assessment_appeals WHERE assessment_id=$1
		AND status IN ('submitted','reviewing')`, req.AssessmentID).Scan(&open)
	if open > 0 {
		writeError(w, http.StatusConflict, "申诉已提交，请等待柜台复核")
		return
	}
	var apID int64
	tx.QueryRow(`INSERT INTO assessment_appeals(assessment_id, guest_id, reason, evidence_note)
		VALUES ($1,$2,$3,$4) RETURNING id`,
		req.AssessmentID, c.UserID, req.Reason, req.EvidenceNote).Scan(&apID)
	tx.Exec(`UPDATE edge_assessments SET status='appealed', updated_at=now() WHERE id=$1`,
		req.AssessmentID)
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"appeal_id": apID,
		"message":   "申诉已提交，柜台可调取课程区域记录与归还照片进行复核",
	})
}

// ---------- 柜台复核申诉 ----------

func (s *Server) listAppeals(w http.ResponseWriter, r *http.Request) {
	s.listJSON(w, `SELECT COALESCE(json_agg(t ORDER BY t.id DESC), '[]') FROM (
		SELECT a.id, a.assessment_id, a.reason, a.evidence_note, a.status,
		       a.counter_note,
		       to_char(a.created_at,'MM-DD HH24:MI') AS created_at,
		       to_char(a.handled_at,'MM-DD HH24:MI') AS handled_at,
		       ea.rental_id, ea.code AS assessment_code, ea.deduction,
		       ea.checkout_photos, ea.return_photos, ea.course_zones,
		       ea.fall_summary, ea.guest_statement, ea.staff_finding,
		       ea.conclusion, ea.status AS assessment_status,
		       r.code AS rental_code, u.name AS guest_name, u.phone AS guest_phone
		FROM assessment_appeals a
		JOIN edge_assessments ea ON ea.id=a.assessment_id
		JOIN rentals r ON r.id=ea.rental_id
		JOIN users u ON u.id=a.guest_id
	) t`)
}

type appealResolveReq struct {
	Decision string `json:"decision"` // upheld|overturned
	Note     string `json:"note"`
}

func (s *Server) resolveAppeal(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	id := urlVar(r, "id")
	var req appealResolveReq
	if err := decode(r, &req); err != nil || (req.Decision != "upheld" && req.Decision != "overturned") {
		writeError(w, http.StatusBadRequest, "decision 需为 upheld（维持）或 overturned（翻案）")
		return
	}
	tx, _ := s.db.Begin()
	defer tx.Rollback()

	var assessmentID int64
	var apStatus string
	if err := tx.QueryRow(`SELECT assessment_id, status FROM assessment_appeals
		WHERE id=$1 FOR UPDATE`, id).Scan(&assessmentID, &apStatus); err != nil {
		writeError(w, http.StatusNotFound, "申诉不存在")
		return
	}
	if apStatus != "submitted" && apStatus != "reviewing" {
		writeError(w, http.StatusConflict, "该申诉已处理")
		return
	}
	var aStatus string
	var rentalID, dID, claimID int64
	var deductionAmt float64
	if err := tx.QueryRow(`SELECT status, rental_id, COALESCE(damage_id,0),
		COALESCE(claim_id,0), deduction FROM edge_assessments WHERE id=$1 FOR UPDATE`,
		assessmentID).Scan(&aStatus, &rentalID, &dID, &claimID, &deductionAmt); err != nil {
		writeError(w, http.StatusNotFound, "鉴定单不存在")
		return
	}
	if aStatus != "appealed" {
		writeError(w, http.StatusConflict, "鉴定单当前状态不允许处理申诉："+aStatus)
		return
	}

	if req.Decision == "upheld" {
		tx.Exec(`UPDATE assessment_appeals SET status='upheld', counter_note=$1,
			handled_by=$2, handled_at=now() WHERE id=$3`, req.Note, c.UserID, id)
		tx.Exec(`UPDATE edge_assessments SET status='upheld', updated_at=now() WHERE id=$1`, assessmentID)
	} else {
		// 保险已赔付到账的案件涉及追偿，提示走线下流程
		if claimID > 0 {
			var paid string
			tx.QueryRow(`SELECT status FROM insurance_claims WHERE id=$1`, claimID).Scan(&paid)
			if paid == "paid" {
				writeError(w, http.StatusConflict, "保险理赔已到账，翻案需走线下追偿流程")
				return
			}
			tx.Exec(`UPDATE insurance_claims SET status='rejected',
				note=COALESCE(NULLIF($1,''),note)||'；申诉翻案，拒赔', updated_at=now() WHERE id=$2`,
				req.Note, claimID)
		}
		// 退回原扣减
		if deductionAmt > 0 {
			note := "板刃鉴定申诉成立（柜台复核课程区域与归还照片），退回原押金扣减"
			if req.Note != "" {
				note += "：" + req.Note
			}
			tx.Exec(`INSERT INTO deposit_transactions(rental_id, type, amount, damage_id, claim_id, note, created_by)
				VALUES ($1,'appeal_reversal',$2,$3,NULLIF($4,0),$5,$6)`,
				rentalID, deductionAmt, dID, claimID, note, c.UserID)
		}
		tx.Exec(`UPDATE assessment_appeals SET status='overturned', counter_note=$1,
			handled_by=$2, handled_at=now() WHERE id=$3`, req.Note, c.UserID, id)
		tx.Exec(`UPDATE edge_assessments SET status='overturned', responsibility='not_liable',
			conclusion=conclusion||$2, updated_at=now() WHERE id=$1`,
			assessmentID, "；申诉翻案："+req.Note)
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	msg := "维持原责任判定，押金扣减不变"
	if req.Decision == "overturned" {
		msg = "申诉成立，已退回押金扣减并撤回保险理赔"
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}
