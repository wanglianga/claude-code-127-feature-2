package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/lib/pq"

	"skirental/internal/rules"
)

// 板刃损伤鉴定：归还发现板刃缺口 -> 暂扣雪具 -> 维修间对比四方证据
// （出库照片 / 课程区域 / 摔倒记录 / 游客说明）认定责任 ->
// 押金扣减、维修工单、保险理赔分别生成；游客可申诉，柜台复核。

// ---------- 摔倒记录（教练登记 / 游客自报） ----------

type fallRecordReq struct {
	RentalID     int64  `json:"rental_id"`
	RentalItemID int64  `json:"rental_item_id"`
	CourseArea   string `json:"course_area"`
	HappenedAt   string `json:"happened_at"` // "2006-01-02 15:04"，可空默认现在
	Description  string `json:"description"`
}

func (req fallRecordReq) happenedAt() time.Time {
	if t, err := time.ParseInLocation("2006-01-02 15:04", req.HappenedAt, time.Local); err == nil {
		return t
	}
	return time.Now()
}

// 教练为课程学员登记摔倒（课程区域是鉴定证据）
func (s *Server) coachFallRecord(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	var req fallRecordReq
	if err := decode(r, &req); err != nil || req.RentalID == 0 || req.CourseArea == "" {
		writeError(w, http.StatusBadRequest, "租赁单与课程区域必填")
		return
	}
	var guestID, coachID int64
	err := s.db.QueryRow(`SELECT r.guest_id, COALESCE(co.coach_id,0) FROM rentals r
		LEFT JOIN courses co ON co.id=r.course_id WHERE r.id=$1`, req.RentalID).
		Scan(&guestID, &coachID)
	if err != nil {
		writeError(w, http.StatusNotFound, "租赁单不存在")
		return
	}
	if coachID != 0 && coachID != c.UserID {
		writeError(w, http.StatusForbidden, "这不是你的课程学员")
		return
	}
	var id int64
	err = s.db.QueryRow(`INSERT INTO fall_records
		(rental_id, rental_item_id, guest_id, course_area, happened_at, description, source, recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,'coach',$7) RETURNING id`,
		req.RentalID, nz(req.RentalItemID), guestID, req.CourseArea,
		req.happenedAt(), req.Description, c.UserID).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"fall_id": id, "message": "摔倒记录已登记，板刃鉴定时将作为证据调取",
	})
}

// 游客自报摔倒（说明来源，鉴定时一并汇总）
func (s *Server) guestFallRecord(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	rid := urlVar(r, "id")
	if !s.ownRental(w, r, rid, c.UserID) {
		return
	}
	var req fallRecordReq
	if err := decode(r, &req); err != nil || req.CourseArea == "" {
		writeError(w, http.StatusBadRequest, "课程区域必填")
		return
	}
	var id int64
	err := s.db.QueryRow(`INSERT INTO fall_records
		(rental_id, rental_item_id, guest_id, course_area, happened_at, description, source, recorded_by)
		VALUES ($1,$2,$3,$4,$5,$6,'guest',$7) RETURNING id`,
		rid, nz(req.RentalItemID), c.UserID, req.CourseArea,
		req.happenedAt(), req.Description, c.UserID).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"fall_id": id, "message": "摔倒记录已提交，将纳入本单鉴定证据",
	})
}

// ---------- 出库照片（柜台出库/补拍，鉴定时与归还照片对比） ----------

type checkoutPhotosReq struct {
	RentalItemID int64    `json:"rental_item_id"`
	Photos       []string `json:"photos"`
}

func (s *Server) saveCheckoutPhotos(w http.ResponseWriter, r *http.Request) {
	var req checkoutPhotosReq
	if err := decode(r, &req); err != nil || req.RentalItemID == 0 || len(req.Photos) == 0 {
		writeError(w, http.StatusBadRequest, "需要指定租赁明细与照片")
		return
	}
	photos, _ := json.Marshal(req.Photos)
	if _, ok := s.execOne(w, `UPDATE rental_items SET checkout_photos=$2
		WHERE id=$1 AND status IN ('assigned','out')
		  AND (SELECT r.status FROM rentals r WHERE r.id=rental_items.rental_id)
		      IN ('reserved','equipped')`, req.RentalItemID, photos); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "出库照片已留存，归还鉴定时可对比",
	})
}

// ---------- 归还验具：板刃缺口存疑 -> 转维修间鉴定 ----------

// transferToEdgeAppraisal 暂扣雪具、汇总四方证据快照、生成待鉴定单。
// 雪具转 inspection（暂停出租并记录 suspended_at），明细转 edge_appraisal。
func (s *Server) transferToEdgeAppraisal(tx *sql.Tx, rentalID int64, req inspectReq, counterID int64) (int64, error) {
	var itemID int64
	var checkoutPhotos []byte
	if err := tx.QueryRow(`SELECT COALESCE(item_id,0), checkout_photos
		FROM rental_items WHERE id=$1`, req.RentalItemID).
		Scan(&itemID, &checkoutPhotos); err != nil {
		return 0, err
	}
	if itemID == 0 {
		return 0, fmt.Errorf("该明细没有关联雪具，无法鉴定")
	}
	severity := req.Severity
	if severity != "light" && severity != "heavy" {
		severity = "medium"
	}
	// 摔倒记录快照 + 课程区域汇总
	var fallSnapshot []byte
	tx.QueryRow(`SELECT COALESCE(json_agg(json_build_object(
		'course_area', course_area, 'happened_at', to_char(happened_at,'MM-DD HH24:MI'),
		'description', description, 'source', source) ORDER BY happened_at), '[]')
		FROM fall_records WHERE rental_id=$1`, rentalID).Scan(&fallSnapshot)
	areas := ""
	tx.QueryRow(`SELECT COALESCE(string_agg(DISTINCT course_area, '、'),'')
		FROM fall_records WHERE rental_id=$1`, rentalID).Scan(&areas)
	returnPhotos, _ := json.Marshal(req.Photos)

	var appraisalID int64
	if err := tx.QueryRow(`INSERT INTO edge_appraisals
		(rental_id, rental_item_id, item_id, checkout_photos, fall_snapshot,
		 course_areas, return_photos, return_note, severity)
		VALUES ($1,$2,$3,COALESCE($4,'[]'::jsonb),COALESCE($5,'[]'::jsonb),$6,
			COALESCE($7,'[]'::jsonb),$8,$9) RETURNING id`,
		rentalID, req.RentalItemID, itemID, checkoutPhotos, fallSnapshot,
		areas, returnPhotos, req.Description, severity).Scan(&appraisalID); err != nil {
		return 0, err
	}
	// 雪具暂扣：暂停出租，记录暂停开始时间
	tx.Exec(`UPDATE equipment_items SET status='inspection', current_rental_item_id=NULL,
		suspended_at=now(), resumed_at=NULL WHERE id=$1`, itemID)
	tx.Exec(`UPDATE rental_items SET status='edge_appraisal' WHERE id=$1`, req.RentalItemID)
	return appraisalID, nil
}

// ---------- 维修间：鉴定工作台 ----------

const edgeAppraisalSQL = `SELECT COALESCE(json_agg(t ORDER BY
	CASE t.status WHEN 'pending' THEN 0 WHEN 'disputed' THEN 1 ELSE 2 END, t.id DESC), '[]') FROM (
	SELECT ea.id, ea.status, ea.responsibility, ea.severity, ea.deduction,
	       ea.checkout_photos, ea.return_photos, ea.fall_snapshot, ea.course_areas,
	       ea.guest_statement, ea.return_note, ea.confirm_note,
	       ea.appeal_status, ea.appeal_reason, ea.handle_note,
	       ea.damage_id, ea.repair_order_id, ea.claim_id,
	       to_char(ea.created_at,'MM-DD HH24:MI') AS created_at,
	       to_char(ea.confirmed_at,'MM-DD HH24:MI') AS confirmed_at,
	       to_char(ea.appealed_at,'MM-DD HH24:MI') AS appealed_at,
	       ea.rental_id, ea.rental_item_id, ea.item_id,
	       r.code AS rental_code, r.session_type, r.status AS rental_status,
	       u.name AS guest_name, u.phone AS guest_phone,
	       co.name AS course_name,
	       i.qr_code, i.serial_no,
	       to_char(i.suspended_at,'MM-DD HH24:MI') AS suspended_at,
	       to_char(i.resumed_at,'MM-DD HH24:MI') AS resumed_at,
	       m.brand, m.name, m.size_label, ri.deposit,
	       cf.name AS confirmed_by_name, hf.name AS handled_by_name
	FROM edge_appraisals ea
	JOIN rentals r ON r.id=ea.rental_id
	JOIN users u ON u.id=r.guest_id
	LEFT JOIN courses co ON co.id=r.course_id
	JOIN equipment_items i ON i.id=ea.item_id
	JOIN rental_items ri ON ri.id=ea.rental_item_id
	JOIN equipment_models m ON m.id=ri.model_id
	LEFT JOIN users cf ON cf.id=ea.confirmed_by
	LEFT JOIN users hf ON hf.id=ea.handled_by
) t`

func (s *Server) repairEdgeAppraisals(w http.ResponseWriter, r *http.Request) {
	s.listJSON(w, edgeAppraisalSQL)
}

func (s *Server) counterEdgeAppraisals(w http.ResponseWriter, r *http.Request) {
	s.listJSON(w, edgeAppraisalSQL)
}

// ---------- 维修间：确认责任（押金扣减/维修工单/保险理赔分别生成） ----------

type confirmAppraisalReq struct {
	Responsibility string `json:"responsibility"` // guest|preexist|none
	Severity       string `json:"severity"`       // light|medium|heavy
	ConfirmNote    string `json:"confirm_note"`
	CreateRepair   bool   `json:"create_repair"` // responsibility=none 时可选转维修
}

func (s *Server) confirmEdgeAppraisal(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	id := urlVar(r, "id")
	var req confirmAppraisalReq
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Responsibility != rules.RespGuest && req.Responsibility != rules.RespPreexist &&
		req.Responsibility != rules.RespNone {
		writeError(w, http.StatusBadRequest, "责任认定需为 guest / preexist / none")
		return
	}
	tx, _ := s.db.Begin()
	defer tx.Rollback()

	var status, returnNote string
	var returnPhotos []byte
	var rentalID, riID, itemID int64
	if err := tx.QueryRow(`SELECT status, rental_id, rental_item_id, item_id, return_note, return_photos
		FROM edge_appraisals WHERE id=$1 FOR UPDATE`, id).
		Scan(&status, &rentalID, &riID, &itemID, &returnNote, &returnPhotos); err != nil {
		writeError(w, http.StatusNotFound, "鉴定单不存在")
		return
	}
	if status != "pending" {
		writeError(w, http.StatusConflict, "该鉴定已确认（申诉复核请在柜台处理）")
		return
	}
	var deposit float64
	if err := tx.QueryRow(`SELECT deposit FROM rental_items WHERE id=$1`, riID).Scan(&deposit); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	decision := rules.EdgeAppraisal(req.Responsibility, req.Severity, deposit)
	needRepair := decision.CreateRepair || (req.Responsibility == rules.RespNone && req.CreateRepair)

	var damageID, repairOrderID, claimID int64
	if needRepair {
		rule, _ := rules.DamageRuleOf("edge_damage")
		// 损坏档案（preexist/none 不扣款，仅留维修依据）
		if err := tx.QueryRow(`INSERT INTO damage_reports
			(rental_item_id, reporter_id, stage, damage_type, severity, description, photos,
			 status, deduction, assessed_by, responsibility)
			VALUES ($1,$2,'at_return','edge_damage',$3,$4,$5,'repair_created',$6,$7,$8)
			RETURNING id`,
			riID, c.UserID, decision.Severity,
			"板刃缺口鉴定："+returnNote+"；"+req.ConfirmNote, string(returnPhotos),
			decision.Deduction, c.UserID, req.Responsibility).Scan(&damageID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.QueryRow(`INSERT INTO repair_orders(item_id, damage_id, issue, advice, status)
			VALUES ($1,$2,$3,$4,'queued') RETURNING id`,
			itemID, damageID, "板刃缺口（鉴定确认）："+returnNote, rule.Advice).
			Scan(&repairOrderID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		tx.Exec(`UPDATE damage_reports SET repair_order_id=$1 WHERE id=$2`, repairOrderID, damageID)
		// 更新库存：转维修，继续暂停出租（suspended_at 保留计时）
		tx.Exec(`UPDATE equipment_items SET status='repair' WHERE id=$1`, itemID)
		tx.Exec(`UPDATE rental_items SET status='repair' WHERE id=$1`, riID)
	} else {
		// 无责且无需维修：核验通过直接上架，恢复出租
		tx.Exec(`UPDATE equipment_items SET status='available', resumed_at=now() WHERE id=$1`, itemID)
		tx.Exec(`UPDATE rental_items SET status='returned_ok', returned_at=now() WHERE id=$1`, riID)
	}

	if req.Responsibility == rules.RespGuest {
		// 押金扣减
		tx.Exec(`INSERT INTO deposit_transactions(rental_id, type, amount, damage_id, appraisal_id, note, created_by)
			VALUES ($1,'deduct_damage',$2,$3,$4,$5,$6)`,
			rentalID, decision.Deduction, damageID, id,
			"板刃损伤鉴定：游客责任，按押金比例扣减", c.UserID)
		// 保险理赔立案（有效保单且覆盖板刃损伤时）
		claimID = s.fileClaimIfCovered(tx, rentalID, damageID, "edge_damage", decision.Deduction)
	}
	// 若归还结算时已暂扣该件押金，鉴定结论后结清暂扣部分
	var held float64
	tx.QueryRow(`SELECT COALESCE(sum(amount),0) FROM deposit_transactions
		WHERE appraisal_id=$1 AND type='hold_appraisal'`, id).Scan(&held)
	if held > 0 {
		settle := held
		if req.Responsibility == rules.RespGuest {
			settle = held - decision.Deduction
		}
		if settle > 0 {
			note := "板刃鉴定非游客责任，暂扣押金全额退还"
			if req.Responsibility == rules.RespGuest {
				note = "板刃鉴定确认，暂扣押金扣除赔偿后退还"
			}
			tx.Exec(`INSERT INTO deposit_transactions(rental_id, type, amount, damage_id, appraisal_id, note, created_by)
				VALUES ($1,'refund',$2,$3,$4,$5,$6)`,
				rentalID, settle, damageID, id, note, c.UserID)
		}
	}

	tx.Exec(`UPDATE edge_appraisals SET status='confirmed', responsibility=$1, severity=$2,
		confirmed_by=$3, confirmed_at=now(), confirm_note=$4, deduction=$5,
		damage_id=$6, repair_order_id=$7, claim_id=$8 WHERE id=$9`,
		req.Responsibility, decision.Severity, c.UserID, req.ConfirmNote, decision.Deduction,
		nz(damageID), nz(repairOrderID), nz(claimID), id)
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":         "鉴定已确认：" + decision.Message,
		"deduction":       decision.Deduction,
		"damage_id":       damageID,
		"repair_order_id": repairOrderID,
		"claim_id":        claimID,
	})
}

// ---------- 游客：提交说明 / 申诉 ----------

type statementReq struct {
	Statement string `json:"statement"`
}

func (s *Server) submitStatement(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	id := urlVar(r, "id")
	var req statementReq
	if err := decode(r, &req); err != nil || req.Statement == "" {
		writeError(w, http.StatusBadRequest, "说明内容必填")
		return
	}
	if _, ok := s.execOne(w, `UPDATE edge_appraisals SET guest_statement=$1
		WHERE id=$2 AND status='pending'
		  AND rental_id IN (SELECT id FROM rentals WHERE guest_id=$3)`,
		req.Statement, id, c.UserID); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "说明已提交，维修间鉴定时将参考"})
}

type appealReq struct {
	Reason string `json:"reason"`
}

func (s *Server) appealAppraisal(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	id := urlVar(r, "id")
	var req appealReq
	if err := decode(r, &req); err != nil || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "申诉理由必填")
		return
	}
	if _, ok := s.execOne(w, `UPDATE edge_appraisals
		SET status='disputed', appeal_status='requested', appeal_reason=$1, appealed_at=now()
		WHERE id=$2 AND status='confirmed' AND appeal_status='none'
		  AND rental_id IN (SELECT id FROM rentals WHERE guest_id=$3)`,
		req.Reason, id, c.UserID); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "申诉已提交，柜台将调出课程区域与归还照片复核",
	})
}

// ---------- 柜台：申诉复核（可调出课程区域与归还照片） ----------

type resolveAppealReq struct {
	Uphold bool   `json:"uphold"` // true=申诉成立撤销扣减；false=维持原判
	Note   string `json:"note"`
}

func (s *Server) resolveAppeal(w http.ResponseWriter, r *http.Request) {
	c := currentUser(r)
	id := urlVar(r, "id")
	var req resolveAppealReq
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	tx, _ := s.db.Begin()
	defer tx.Rollback()

	var status, appealStatus string
	var deduction float64
	var rentalID, claimID, damageID int64
	if err := tx.QueryRow(`SELECT status, appeal_status, deduction, rental_id,
		COALESCE(claim_id,0), COALESCE(damage_id,0)
		FROM edge_appraisals WHERE id=$1 FOR UPDATE`, id).
		Scan(&status, &appealStatus, &deduction, &rentalID, &claimID, &damageID); err != nil {
		writeError(w, http.StatusNotFound, "鉴定单不存在")
		return
	}
	if status != "disputed" || appealStatus != "requested" {
		writeError(w, http.StatusConflict, "该鉴定没有待复核的申诉")
		return
	}

	if req.Uphold {
		// 已赔付到账的理赔无法线上撤销
		if claimID > 0 {
			var claimStatus string
			tx.QueryRow(`SELECT status FROM insurance_claims WHERE id=$1`, claimID).Scan(&claimStatus)
			if claimStatus == "paid" {
				writeError(w, http.StatusConflict, "关联理赔已赔付到账，无法线上撤销，请线下协商处理")
				return
			}
			tx.Exec(`UPDATE insurance_claims SET status='rejected', note=note||'（申诉成立撤销）',
				updated_at=now() WHERE id=$1`, claimID)
		}
		// 撤销押金扣减：等额返还
		if deduction > 0 {
			tx.Exec(`INSERT INTO deposit_transactions(rental_id, type, amount, damage_id, appraisal_id, note, created_by)
				VALUES ($1,'deduct_reversal',$2,$3,$4,$5,$6)`,
				rentalID, deduction, damageID, id,
				"板刃鉴定申诉成立，撤销押金扣减："+req.Note, c.UserID)
		}
		if damageID > 0 {
			tx.Exec(`UPDATE damage_reports SET status='closed', responsibility='none' WHERE id=$1`, damageID)
		}
		tx.Exec(`UPDATE edge_appraisals SET status='reversed', appeal_status='upheld',
			handled_by=$1, handled_at=now(), handle_note=$2 WHERE id=$3`,
			c.UserID, req.Note, id)
	} else {
		tx.Exec(`UPDATE edge_appraisals SET status='confirmed', appeal_status='rejected',
			handled_by=$1, handled_at=now(), handle_note=$2 WHERE id=$3`,
			c.UserID, req.Note, id)
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	msg := "申诉已复核：维持原认定"
	if req.Uphold {
		msg = "申诉成立：押金扣减已撤销返还，关联理赔已撤销"
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// ---------- 保险自动立案（assessDamage 与鉴定确认共用） ----------

// fileClaimIfCovered 有效保单且覆盖该损坏类型时自动立案，返回理赔单 id（0 表示未立案）
func (s *Server) fileClaimIfCovered(tx *sql.Tx, rentalID, damageID int64, damageType string, deduction float64) int64 {
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
		if cv == damageType {
			covered = true
			break
		}
	}
	res := rules.CalcClaim(deduction, deductible, capAmt, covered)
	var claimID int64
	if err := tx.QueryRow(`INSERT INTO insurance_claims
		(policy_id, damage_id, amount_requested, deductible, amount_approved, status, note)
		VALUES ($1,$2,$3,$4,$5,'filed',$6) RETURNING id`,
		policyID, damageID, res.Requested, deductible, res.Approved, res.Message).
		Scan(&claimID); err != nil {
		return 0
	}
	tx.Exec(`UPDATE damage_reports SET claim_id=$1, status=
		CASE WHEN status='repair_created' THEN 'repair_created' ELSE 'assessed' END WHERE id=$2`,
		claimID, damageID)
	tx.Exec(`UPDATE insurance_policies SET status='claimed' WHERE id=$1`, policyID)
	return claimID
}
