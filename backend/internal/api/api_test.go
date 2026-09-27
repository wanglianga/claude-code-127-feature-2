package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"skirental/internal/config"
	"skirental/internal/db"
	"skirental/internal/seed"

	_ "github.com/lib/pq"
)

var (
	ts     *httptest.Server
	client = &http.Client{}
)

const testDBName = "skirental_test"

func TestMain(m *testing.M) {
	if err := setup(); err != nil {
		fmt.Fprintln(os.Stderr, "集成测试初始化失败（需要本地 PostgreSQL）:", err)
		os.Exit(1)
	}
	code := m.Run()
	teardown()
	os.Exit(code)
}

func setup() error {
	// 从 DATABASE_URL 推导 postgres 维护库连接
	raw := os.Getenv("DATABASE_URL")
	if raw == "" {
		raw = "postgres://postgres@127.0.0.1:5433/skirental?sslmode=disable"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	adminURL := *u
	adminURL.Path = "/postgres"
	adm, err := sql.Open("postgres", adminURL.String())
	if err != nil {
		return err
	}
	defer adm.Close()
	if err := adm.Ping(); err != nil {
		return fmt.Errorf("无法连接 PostgreSQL（跳过集成测试需先启动本地库）: %w", err)
	}
	adm.Exec(`DROP DATABASE IF EXISTS ` + testDBName + ` WITH (FORCE)`)
	if _, err := adm.Exec(`CREATE DATABASE ` + testDBName); err != nil {
		return err
	}

	target := *u
	target.Path = "/" + testDBName
	testDB, err := db.Open(target.String())
	if err != nil {
		return err
	}
	if err := db.Migrate(testDB); err != nil {
		return err
	}
	if err := seed.Run(testDB); err != nil {
		return err
	}

	cfg := config.Get()
	cfg.UploadDir = os.TempDir() + "/ski-test-uploads"
	os.MkdirAll(cfg.UploadDir, 0o755)
	handler := NewServer(testDB, cfg)
	ts = httptest.NewServer(handler)
	return nil
}

func teardown() {
	if ts != nil {
		ts.Close()
	}
}

// ---------- HTTP 辅助 ----------

func login(t *testing.T, phone string) string {
	t.Helper()
	var d struct {
		Token string `json:"token"`
	}
	req(t, "POST", "/auth/login", "", map[string]string{"phone": phone, "password": "ski123456"}, &d)
	if d.Token == "" {
		t.Fatal("登录未返回 token")
	}
	return d.Token
}

func req(t *testing.T, method, path, token string, body interface{}, out interface{}) (int, map[string]string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	r, err := http.NewRequest(method, ts.URL+"/api"+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && len(data) > 0 {
		_ = json.Unmarshal(data, out)
	}
	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &errBody)
	return resp.StatusCode, map[string]string{"error": errBody.Error, "raw": string(data)}
}

func mustOK(t *testing.T, code int, errMsg string) {
	t.Helper()
	if code < 200 || code >= 300 {
		t.Fatalf("期望 2xx，实际 %d：%s", code, errMsg)
	}
}

// ---------- 测试用例 ----------

func TestAuthAndProfile(t *testing.T) {
	// 错误密码
	code, _ := req(t, "POST", "/auth/login", "", map[string]string{"phone": "13900000001", "password": "wrong"}, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401，实际 %d", code)
	}
	g := login(t, "13900000001")
	// 无 token 访问受保护接口
	if code, _ = req(t, "GET", "/rentals/mine", "", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401，实际 %d", code)
	}
	var me map[string]interface{}
	code, _ = req(t, "GET", "/me", g, nil, &me)
	mustOK(t, code, "")
	if me["role"] != "guest" || me["height_cm"].(float64) != 170 {
		t.Fatalf("个人资料异常: %+v", me)
	}
	// 角色越权：游客不能访问财务
	code, _ = req(t, "GET", "/finance/summary", g, nil, nil)
	if code != http.StatusForbidden {
		t.Fatalf("游客访问财务应 403，实际 %d", code)
	}
}

func TestSuggestions(t *testing.T) {
	g := login(t, "13900000001") // 170/65/42/57 beginner
	var d struct {
		Suggestions []struct {
			EquipmentType string `json:"equipment_type"`
			Size          string `json:"size"`
		} `json:"suggestions"`
	}
	code, e := req(t, "POST", "/guest/suggestions", g, map[string]string{"session_type": "day"}, &d)
	mustOK(t, code, e["error"])
	got := map[string]string{}
	for _, s := range d.Suggestions {
		got[s.EquipmentType] = s.Size
	}
	if got["ski"] != "145cm" || got["boot"] != "EU42" || got["helmet"] != "M" || got["pole"] != "120cm" || got["protector"] != "L" {
		t.Fatalf("初学者建议尺码异常: %+v", got)
	}
}

// 全流程：下单 → 确认预约 → 扫码出库 → 教练确认 → 跨码换鞋 →
// 中途板刃损伤（自动换发+维修+保险立案）→ 日场归还结算 → 保险赔付冲抵押金
func TestFullRentalLifecycle(t *testing.T) {
	g := login(t, "13900000004") // 刘队长 180/88/45/60 advanced
	counter := login(t, "13800000003")
	coach := login(t, "13800000002")
	repair := login(t, "13800000004")
	ins := login(t, "13800000005")
	fin := login(t, "13800000006")
	admin := login(t, "13800000001")

	// 选一个有余量的日场取具槽
	var slots []struct {
		ID        int64 `json:"id"`
		Remaining int   `json:"remaining"`
	}
	req(t, "GET", "/slots?session=day", g, nil, &slots)
	var slotID int64
	for _, s := range slots {
		if s.Remaining >= 2 {
			slotID = s.ID
			break
		}
	}
	if slotID == 0 {
		t.Fatal("没有可用日场时段")
	}

	// 1. 下单（5 件含雪杖，安心险）
	var created struct {
		Rentals []struct {
			RentalID     int64   `json:"rental_id"`
			DepositTotal float64 `json:"deposit_total"`
			FeeTotal     float64 `json:"fee_total"`
			PremiumTotal float64 `json:"premium_total"`
		} `json:"rentals"`
	}
	code, e := req(t, "POST", "/rentals", g, map[string]interface{}{
		"session_type": "day", "slot_id": slotID, "insurance_product_id": 2,
		"include": map[string]bool{"pole": true, "protector": true},
	}, &created)
	mustOK(t, code, e["error"])
	if len(created.Rentals) != 1 {
		t.Fatalf("个人下单应生成 1 单，实际 %d", len(created.Rentals))
	}
	rid := created.Rentals[0].RentalID
	if created.Rentals[0].PremiumTotal != 30 || created.Rentals[0].DepositTotal <= 0 {
		t.Fatalf("费用异常: %+v", created.Rentals[0])
	}

	// 2. 确认预约
	code, e = req(t, "POST", fmt.Sprintf("/rentals/%d/confirm", rid), g, map[string]string{}, nil)
	mustOK(t, code, e["error"])

	// 3. 柜台整单出库
	var out struct {
		DepositHold float64 `json:"deposit_hold"`
	}
	code, e = req(t, "POST", "/counter/checkout", counter,
		map[string]interface{}{"rental_id": rid}, &out)
	mustOK(t, code, e["error"])
	if out.DepositHold != created.Rentals[0].DepositTotal {
		t.Fatalf("冻结押金 %.2f 与押金合计 %.2f 不一致", out.DepositHold, created.Rentals[0].DepositTotal)
	}

	// 4. 扫码：雪具应显示在刘队长手中
	var detail struct {
		Items []struct {
			ID            int64   `json:"id"`
			EquipmentType string  `json:"equipment_type"`
			QRCode        string  `json:"qr_code"`
			FitStatus     string  `json:"fit_status"`
			Deposit       float64 `json:"deposit"`
		} `json:"items"`
	}
	req(t, "GET", fmt.Sprintf("/rentals/%d", rid), g, nil, &detail)
	if len(detail.Items) != 5 {
		t.Fatalf("应有 5 件雪具，实际 %d", len(detail.Items))
	}
	var bootID, helmetID, skiID int64
	var skiQR string
	for _, i := range detail.Items {
		var scanned struct {
			HolderName string `json:"holder_name"`
		}
		code, e = req(t, "POST", "/counter/scan", counter, map[string]string{"qr_code": i.QRCode}, &scanned)
		mustOK(t, code, e["error"])
		if !strings.Contains(scanned.HolderName, "刘") {
			t.Fatalf("扫码持有人应为刘队长，实际 %q", scanned.HolderName)
		}
		switch i.EquipmentType {
		case "boot":
			bootID = i.ID
		case "helmet":
			helmetID = i.ID
		case "ski":
			skiID = i.ID
			skiQR = i.QRCode
		}
	}

	// 5. 教练课前确认：雪鞋合脚通过；头盔标不合规 -> 安全提醒
	var fit struct {
		FitStatus string `json:"fit_status"`
	}
	code, e = req(t, "POST", "/coach/fit-check", coach,
		map[string]interface{}{"rental_item_id": bootID, "fit_status": "ok"}, &fit)
	mustOK(t, code, e["error"])
	if fit.FitStatus != "ok" {
		t.Fatal("雪鞋确认应通过")
	}
	req(t, "POST", "/coach/fit-check", coach,
		map[string]interface{}{"rental_item_id": helmetID, "fit_status": "issue", "note": "头盔偏松"}, nil)
	var rd struct {
		SafetyReminders []struct {
			Message string `json:"message"`
		} `json:"safety_reminders"`
	}
	req(t, "GET", fmt.Sprintf("/rentals/%d", rid), g, nil, &rd)
	reminded := false
	for _, s := range rd.SafetyReminders {
		if strings.Contains(s.Message, "头盔") {
			reminded = true
		}
	}
	if !reminded {
		t.Fatal("不合规后应收到安全提醒")
	}

	// 6. 换码：雪鞋 EU45 -> EU45.5
	var ex struct {
		ExchangeID int64 `json:"exchange_id"`
	}
	code, e = req(t, "POST", fmt.Sprintf("/rentals/%d/exchange-request", rid), g,
		map[string]interface{}{"rental_item_id": bootID, "reason": "挤脚", "desired_size": "EU45.5"}, &ex)
	mustOK(t, code, e["error"])
	var proc struct {
		NewSize string `json:"new_size"`
	}
	code, e = req(t, "POST", fmt.Sprintf("/counter/exchanges/%d/process", ex.ExchangeID), counter,
		map[string]interface{}{"approve": true}, &proc)
	mustOK(t, code, e["error"])
	if proc.NewSize != "EU45.5" {
		t.Fatalf("换发尺码应为 EU45.5，实际 %q", proc.NewSize)
	}

	// 7. 游客中途上报雪板板刃损伤，柜台评定
	var dmg struct {
		DamageID int64 `json:"damage_id"`
	}
	code, e = req(t, "POST", fmt.Sprintf("/rentals/%d/damage", rid), g,
		map[string]interface{}{"rental_item_id": skiID, "damage_type": "edge_damage",
			"severity": "medium", "description": "测试板刃缺口"}, &dmg)
	mustOK(t, code, e["error"])
	var assessed struct {
		DeductionHint float64 `json:"deduction_hint"`
	}
	code, e = req(t, "POST", fmt.Sprintf("/counter/damages/%d/assess", dmg.DamageID), counter,
		map[string]interface{}{"severity": "medium"}, &assessed)
	mustOK(t, code, e["error"])
	// 雪板押金 1500，板刃 35% = 525
	if assessed.DeductionHint != 525 {
		t.Fatalf("板刃损伤扣减应为 525，实际 %.2f", assessed.DeductionHint)
	}
	// 原雪板应进入维修，工单存在
	var orders []struct {
		Issue string `json:"issue"`
	}
	req(t, "GET", "/repair/orders", repair, nil, &orders)
	foundRO := false
	for _, o := range orders {
		if strings.Contains(o.Issue, "板刃") {
			foundRO = true
		}
	}
	if !foundRO {
		t.Fatal("应生成板刃维修工单")
	}
	// 自动换发的新雪板仍可扫码，且当前雪具不是原来那件
	var d2 struct {
		Items []struct {
			EquipmentType string `json:"equipment_type"`
			QRCode        string `json:"qr_code"`
			Status        string `json:"status"`
		} `json:"items"`
	}
	req(t, "GET", fmt.Sprintf("/rentals/%d", rid), g, nil, &d2)
	var newSkiQR string
	for _, i := range d2.Items {
		if i.EquipmentType == "ski" {
			newSkiQR = i.QRCode
			if i.Status != "out" {
				t.Fatalf("换发的雪板应处于 out，实际 %s", i.Status)
			}
		}
	}
	if newSkiQR == skiQR {
		t.Fatal("换发后雪板二维码应变化")
	}

	// 8. 排队日场归还，逐件完好验具后结算
	code, e = req(t, "POST", fmt.Sprintf("/rentals/%d/queue-return", rid), g,
		map[string]interface{}{"night": false}, nil)
	mustOK(t, code, e["error"])
	for {
		var cur struct {
			Items []struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
			} `json:"items"`
		}
		req(t, "GET", fmt.Sprintf("/rentals/%d", rid), g, nil, &cur)
		next := int64(0)
		for _, x := range cur.Items {
			if x.Status == "out" {
				next = x.ID
				break
			}
		}
		if next == 0 {
			break
		}
		code, e = req(t, "POST", fmt.Sprintf("/counter/returns/%d/inspect", rid), counter,
			map[string]interface{}{"rental_item_id": next, "result": "ok"}, nil)
		mustOK(t, code, e["error"])
	}
	var finish struct {
		Deducted float64 `json:"deducted"`
		Refund   float64 `json:"refund"`
	}
	code, e = req(t, "POST", fmt.Sprintf("/counter/returns/%d/finish", rid), counter,
		map[string]string{}, &finish)
	mustOK(t, code, e["error"])
	if finish.Deducted != 525 {
		t.Fatalf("归还时扣减应为 525，实际 %.2f", finish.Deducted)
	}
	if finish.Refund != created.Rentals[0].DepositTotal-525 {
		t.Fatalf("保险赔付前退还金额应为押金-525，实际 %.2f", finish.Refund)
	}

	// 9. 保险审核 → 核定（525-50 免赔=475）→ 赔付冲抵
	var claims []struct {
		ID       int64  `json:"id"`
		Status   string `json:"status"`
		RentalID int64  `json:"rental_id"`
	}
	req(t, "GET", "/insurance/claims", ins, nil, &claims)
	var claimID int64
	for _, c := range claims {
		if c.Status == "filed" && c.RentalID == rid {
			claimID = c.ID
		}
	}
	if claimID == 0 {
		t.Fatal("本单应有 filed 状态的理赔单")
	}
	req(t, "POST", fmt.Sprintf("/insurance/claims/%d/review", claimID), ins,
		map[string]interface{}{"status": "under_review", "note": "集成测试审核"}, nil)
	req(t, "POST", fmt.Sprintf("/insurance/claims/%d/review", claimID), ins,
		map[string]interface{}{"status": "approved", "note": "核定"}, nil)
	var pay struct {
		Payout float64 `json:"payout"`
	}
	code, e = req(t, "POST", fmt.Sprintf("/insurance/claims/%d/pay", claimID), ins,
		map[string]string{}, &pay)
	mustOK(t, code, e["error"])
	if pay.Payout != 475 {
		t.Fatalf("赔付应为 475（525-50 免赔），实际 %.2f", pay.Payout)
	}
	// 赔付后应有保险冲抵流水
	var ledger []struct {
		Type   string  `json:"type"`
		Amount float64 `json:"amount"`
	}
	req(t, "GET", "/finance/ledger", fin, nil, &ledger)
	var offset float64
	for _, l := range ledger {
		if l.Type == "insurance_offset" {
			offset += l.Amount
		}
	}
	if offset != 475 {
		t.Fatalf("保险冲抵流水应为 475，实际 %.2f", offset)
	}

	// 10. 反馈
	code, e = req(t, "POST", fmt.Sprintf("/rentals/%d/feedback", rid), g,
		map[string]interface{}{"rating": 5, "text": "集成测试好评"}, nil)
	mustOK(t, code, e["error"])

	// 11. 管理分析接口可用且安全提醒名单包含该游客
	var analytics map[string]json.RawMessage
	code, e = req(t, "GET", "/admin/analytics", admin, nil, &analytics)
	mustOK(t, code, e["error"])
	if _, ok := analytics["guest_safety"]; !ok {
		t.Fatal("分析结果缺少 guest_safety")
	}
}

func TestNightReturnSeparateQueue(t *testing.T) {
	// 团队成员-小钱 155/48/37 beginner，夜场
	g := login(t, "13900000005")
	counter := login(t, "13800000003")

	var slots []struct {
		ID        int64 `json:"id"`
		Remaining int   `json:"remaining"`
	}
	req(t, "GET", "/slots?session=night", g, nil, &slots)
	var slotID int64
	for _, s := range slots {
		if s.Remaining >= 1 {
			slotID = s.ID
			break
		}
	}
	var created struct {
		Rentals []struct {
			RentalID int64   `json:"rental_id"`
			FeeTotal float64 `json:"fee_total"`
		} `json:"rentals"`
	}
	code, e := req(t, "POST", "/rentals", g, map[string]interface{}{
		"session_type": "night", "slot_id": slotID,
		"include": map[string]bool{"protector": true},
	}, &created)
	mustOK(t, code, e["error"])
	rid := created.Rentals[0].RentalID
	// 夜场四件：板360+鞋120+盔72+护具96 = 648
	if created.Rentals[0].FeeTotal != 648 {
		t.Fatalf("夜场加价后租金应为 648，实际 %.2f", created.Rentals[0].FeeTotal)
	}
	req(t, "POST", fmt.Sprintf("/rentals/%d/confirm", rid), g, map[string]string{}, nil)
	req(t, "POST", "/counter/checkout", counter, map[string]interface{}{"rental_id": rid}, nil)
	req(t, "POST", fmt.Sprintf("/rentals/%d/queue-return", rid), g,
		map[string]interface{}{"night": true}, nil)

	var tickets []struct {
		QueueType string `json:"queue_type"`
		RentalID  int64  `json:"rental_id"`
	}
	req(t, "GET", "/counter/queue?type=return_night", counter, nil, &tickets)
	ok := false
	for _, tk := range tickets {
		if tk.RentalID == rid && tk.QueueType == "return_night" {
			ok = true
		}
	}
	if !ok {
		t.Fatal("夜场归还应进入独立的 return_night 队列")
	}
}

func TestTeamOrderAndPickupQueue(t *testing.T) {
	leader := login(t, "13900000004")
	var teams []struct {
		ID int64 `json:"id"`
	}
	req(t, "GET", "/teams/mine", leader, nil, &teams)
	if len(teams) != 1 || teams[0].ID != 1 {
		t.Fatalf("队长应属于 1 个团队，实际 %+v", teams)
	}
	var slots []struct {
		ID        int64 `json:"id"`
		Remaining int   `json:"remaining"`
	}
	req(t, "GET", "/slots?session=day", leader, nil, &slots)
	var slotID int64
	for _, s := range slots {
		if s.Remaining >= 3 {
			slotID = s.ID
			break
		}
	}
	if slotID == 0 {
		t.Skip("没有容量≥3的日场时段，跳过团队单测试")
	}
	var created struct {
		Rentals []struct {
			RentalID int64 `json:"rental_id"`
		} `json:"rentals"`
	}
	code, e := req(t, "POST", "/rentals", leader, map[string]interface{}{
		"team_id": 1, "session_type": "day", "slot_id": slotID,
		"include": map[string]bool{"protector": true},
	}, &created)
	mustOK(t, code, e["error"])
	if len(created.Rentals) != 3 {
		t.Fatalf("团队单应为 3 名成员各 1 单，实际 %d", len(created.Rentals))
	}
	// 队长确认本人订单 -> 取具队列出现团队优先票
	rid := created.Rentals[0].RentalID
	code, e = req(t, "POST", fmt.Sprintf("/rentals/%d/confirm", rid), leader,
		map[string]string{}, nil)
	mustOK(t, code, e["error"])
	var tickets []struct {
		RentalID int64 `json:"rental_id"`
		Priority int   `json:"priority"`
	}
	req(t, "GET", "/counter/queue?type=pickup", login(t, "13800000003"), nil, &tickets)
	found := false
	for _, tk := range tickets {
		if tk.RentalID == rid && tk.Priority == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("团队取具票应为高优先级")
	}
}

func TestMaintenanceGeneration(t *testing.T) {
	repair := login(t, "13800000004")
	var gen struct {
		Created int `json:"created"`
	}
	code, e := req(t, "POST", "/repair/maintenance/generate", repair, map[string]string{}, &gen)
	mustOK(t, code, e["error"])
	// 种子数据 + 全流程用例已产生板刃/内衬等损坏，至少应生成 1 条高损耗提醒
	if gen.Created < 1 {
		t.Fatalf("应生成至少 1 条保养提醒，实际 %d", gen.Created)
	}
}

// 板刃损伤鉴定全链路：
// 出库拍照 + 教练课程区域摔倒登记 -> 归还发现缺口转鉴定（暂停出租）-> 游客说明 ->
// 结算暂扣押金 -> 维修间四方证据定责（扣减/工单/理赔分别生成）-> 修复恢复 ->
// 申诉维持后理赔冲抵；另覆盖无责上架与种子单的申诉成立撤销。
func TestEdgeAppraisalLifecycle(t *testing.T) {
	counter := login(t, "13800000003")
	coach := login(t, "13800000002")
	repair := login(t, "13800000004")
	ins := login(t, "13800000005")

	// ---------- 场景 A：新单走游客责任 ----------
	g := login(t, "13900000001")
	rid, skiRI := createDayRentalWithSki(t, g, counter, 2) // 安心险

	// 教练在课程区域登记摔倒
	code, e := req(t, "POST", "/coach/fall-record", coach, map[string]interface{}{
		"rental_id": rid, "rental_item_id": skiRI,
		"course_area": "魔毯区", "description": "下魔毯时侧摔一次",
	}, nil)
	mustOK(t, code, e["error"])
	// 游客自报一条
	code, e = req(t, "POST", fmt.Sprintf("/rentals/%d/fall-record", rid), g, map[string]interface{}{
		"rental_item_id": skiRI, "course_area": "初级道", "description": "中途没站稳",
	}, nil)
	mustOK(t, code, e["error"])
	// 柜台出库拍照留档
	code, e = req(t, "POST", "/counter/checkout-photos", counter, map[string]interface{}{
		"rental_item_id": skiRI, "photos": []string{"/uploads/checkout-ski.jpg"},
	}, nil)
	mustOK(t, code, e["error"])

	// 排队日场归还
	code, e = req(t, "POST", fmt.Sprintf("/rentals/%d/queue-return", rid), g,
		map[string]interface{}{"night": false}, nil)
	mustOK(t, code, e["error"])
	// 雪板验具：板刃缺口存疑 -> 转鉴定
	code, e = req(t, "POST", fmt.Sprintf("/counter/returns/%d/inspect", rid), counter,
		map[string]interface{}{
			"rental_item_id": skiRI, "result": "suspect_edge", "severity": "medium",
			"description": "左板刃 2mm 缺口",
			"photos":      []string{"/uploads/return-edge.jpg"},
		}, nil)
	mustOK(t, code, e["error"])

	// 雪板明细应处于鉴定中；型号目录出现暂停出租计数
	var cur struct {
		Items []struct {
			ID            int64  `json:"id"`
			EquipmentType string `json:"equipment_type"`
			Status        string `json:"status"`
			ModelID       int64  `json:"model_id"`
		} `json:"items"`
	}
	req(t, "GET", fmt.Sprintf("/rentals/%d", rid), g, nil, &cur)
	var skiModelID int64
	for _, it := range cur.Items {
		if it.EquipmentType == "ski" {
			if it.Status != "edge_appraisal" {
				t.Fatalf("雪板应为 edge_appraisal，实际 %s", it.Status)
			}
			skiModelID = it.ModelID
		}
	}
	var models []struct {
		ID        int `json:"id"`
		Suspended int `json:"suspended"`
	}
	req(t, "GET", "/models?type=ski", g, nil, &models)
	foundSuspended := false
	for _, m := range models {
		if int64(m.ID) == skiModelID && m.Suspended >= 1 {
			foundSuspended = true
		}
	}
	if !foundSuspended {
		t.Fatal("暂停出租的雪板应计入型号 suspended，影响库存推荐")
	}

	// 鉴定单进入维修间工作台
	var bench []struct {
		ID       int64  `json:"id"`
		RentalID int64  `json:"rental_id"`
		Status   string `json:"status"`
	}
	req(t, "GET", "/repair/edge-appraisals", repair, nil, &bench)
	var appraisalID int64
	for _, a := range bench {
		if a.RentalID == rid && a.Status == "pending" {
			appraisalID = a.ID
		}
	}
	if appraisalID == 0 {
		t.Fatal("维修间工作台应有该单待鉴定")
	}

	// 游客提交说明
	code, e = req(t, "POST", fmt.Sprintf("/appraisals/%d/statement", appraisalID), g,
		map[string]string{"statement": "都在教学区域滑行"}, nil)
	mustOK(t, code, e["error"])

	// 其余雪具完好验具后结算：雪板押金 1500 暂扣
	for _, it := range cur.Items {
		if it.Status == "out" {
			req(t, "POST", fmt.Sprintf("/counter/returns/%d/inspect", rid), counter,
				map[string]interface{}{"rental_item_id": it.ID, "result": "ok"}, nil)
		}
	}
	var finish struct {
		AppraisalHold float64 `json:"appraisal_hold"`
		Refund        float64 `json:"refund"`
		DepositHeld   float64 `json:"deposit_held"`
	}
	code, e = req(t, "POST", fmt.Sprintf("/counter/returns/%d/finish", rid), counter,
		map[string]string{}, &finish)
	mustOK(t, code, e["error"])
	if finish.AppraisalHold != 1500 {
		t.Fatalf("应暂扣雪板押金 1500，实际 %.2f", finish.AppraisalHold)
	}
	if finish.Refund != finish.DepositHeld-1500 {
		t.Fatalf("暂扣部分不应退还：%.2f vs %.2f", finish.Refund, finish.DepositHeld-1500)
	}

	// 维修间认定游客责任（中等）：扣减 525、工单、理赔分别生成
	var conf struct {
		Deduction     float64 `json:"deduction"`
		DamageID      int64   `json:"damage_id"`
		RepairOrderID int64   `json:"repair_order_id"`
		ClaimID       int64   `json:"claim_id"`
	}
	code, e = req(t, "POST", fmt.Sprintf("/repair/appraisals/%d/confirm", appraisalID), repair,
		map[string]interface{}{
			"responsibility": "guest", "severity": "medium",
			"confirm_note": "缺口形态与硬磕碰一致",
		}, &conf)
	mustOK(t, code, e["error"])
	if conf.Deduction != 525 || conf.DamageID == 0 || conf.RepairOrderID == 0 || conf.ClaimID == 0 {
		t.Fatalf("三单应分别生成：%+v", conf)
	}
	// 暂扣押金扣除 525 后退还 975
	logs := rentalLogs(t, rid, g)
	assertLogAmount(t, logs, "deduct_damage", 525)
	assertLogAmount(t, logs, "refund", 975)

	// 游客申诉，柜台维持原判
	code, e = req(t, "POST", fmt.Sprintf("/appraisals/%d/appeal", appraisalID), g,
		map[string]string{"reason": "我不认可"}, nil)
	mustOK(t, code, e["error"])
	code, e = req(t, "POST", fmt.Sprintf("/counter/appraisals/%d/resolve-appeal", appraisalID), counter,
		map[string]interface{}{"uphold": false, "note": "证据充分，维持"}, nil)
	mustOK(t, code, e["error"])

	// 理赔审核核定赔付（525-50 免赔=475）冲抵
	var claims []struct {
		ID       int64  `json:"id"`
		Status   string `json:"status"`
		RentalID int64  `json:"rental_id"`
	}
	req(t, "GET", "/insurance/claims", ins, nil, &claims)
	var claimID int64
	for _, cl := range claims {
		if cl.RentalID == rid && cl.Status == "filed" {
			claimID = cl.ID
		}
	}
	if claimID == 0 {
		t.Fatal("本单应有 filed 理赔")
	}
	req(t, "POST", fmt.Sprintf("/insurance/claims/%d/review", claimID), ins,
		map[string]interface{}{"status": "approved"}, nil)
	var pay struct {
		Payout float64 `json:"payout"`
	}
	code, e = req(t, "POST", fmt.Sprintf("/insurance/claims/%d/pay", claimID), ins,
		map[string]string{}, &pay)
	mustOK(t, code, e["error"])
	if pay.Payout != 475 {
		t.Fatalf("赔付应为 475，实际 %.2f", pay.Payout)
	}
	logs = rentalLogs(t, rid, g)
	assertLogAmount(t, logs, "insurance_offset", 475)

	// 修复上架：恢复出租并记录 resumed_at
	code, e = req(t, "POST", fmt.Sprintf("/repair/orders/%d/update", conf.RepairOrderID), repair,
		map[string]interface{}{"status": "done", "repair_cost": 100}, nil)
	mustOK(t, code, e["error"])
	var orders []struct {
		ID        int64  `json:"id"`
		ResumedAt string `json:"resumed_at"`
	}
	req(t, "GET", "/repair/orders?all=1", repair, nil, &orders)
	resumed := false
	for _, o := range orders {
		if o.ID == conf.RepairOrderID && o.ResumedAt != "" {
			resumed = true
		}
	}
	if !resumed {
		t.Fatal("修复上架应记录恢复出租时间")
	}

	// ---------- 场景 B：认定无责，雪具直接恢复上架 ----------
	g2 := login(t, "13900000003")
	rid2, skiRI2 := createDayRentalWithSki(t, g2, counter, 0)
	req(t, "POST", fmt.Sprintf("/rentals/%d/queue-return", rid2), g2,
		map[string]interface{}{"night": false}, nil)
	req(t, "POST", fmt.Sprintf("/counter/returns/%d/inspect", rid2), counter,
		map[string]interface{}{
			"rental_item_id": skiRI2, "result": "suspect_edge",
			"description": "疑似旧痕",
		}, nil)
	var bench2 []struct {
		ID       int64 `json:"id"`
		RentalID int64 `json:"rental_id"`
	}
	req(t, "GET", "/repair/edge-appraisals", repair, nil, &bench2)
	var a2 int64
	for _, a := range bench2 {
		if a.RentalID == rid2 {
			a2 = a.ID
		}
	}
	code, e = req(t, "POST", fmt.Sprintf("/repair/appraisals/%d/confirm", a2), repair,
		map[string]interface{}{"responsibility": "none", "confirm_note": "正常磨损"}, nil)
	mustOK(t, code, e["error"])
	var d2 struct {
		Items []struct {
			EquipmentType string `json:"equipment_type"`
			Status        string `json:"status"`
		} `json:"items"`
	}
	req(t, "GET", fmt.Sprintf("/rentals/%d", rid2), g2, nil, &d2)
	for _, it := range d2.Items {
		if it.EquipmentType == "ski" && it.Status != "returned_ok" {
			t.Fatalf("无责且不需维修时雪板应完好上架，实际 %s", it.Status)
		}
	}

	// ---------- 场景 C：种子单 R-DEMO-D（rental_id=4）暂扣 + 申诉成立撤销 ----------
	g3 := login(t, "13900000007")
	var pending struct {
		AppraisalHold float64 `json:"appraisal_hold"`
	}
	code, e = req(t, "POST", "/counter/returns/4/finish", counter, map[string]string{}, &pending)
	mustOK(t, code, e["error"])
	if pending.AppraisalHold != 1500 {
		t.Fatalf("种子鉴定单暂扣应为 1500，实际 %.2f", pending.AppraisalHold)
	}
	var bench3 []struct {
		ID       int64 `json:"id"`
		RentalID int64 `json:"rental_id"`
	}
	req(t, "GET", "/repair/edge-appraisals", repair, nil, &bench3)
	var a3 int64
	for _, a := range bench3 {
		if a.RentalID == 4 {
			a3 = a.ID
		}
	}
	req(t, "POST", fmt.Sprintf("/repair/appraisals/%d/confirm", a3), repair,
		map[string]interface{}{"responsibility": "guest", "severity": "medium"}, nil)
	req(t, "POST", fmt.Sprintf("/appraisals/%d/appeal", a3), g3,
		map[string]string{"reason": "出库照片可见旧缺口"}, nil)
	code, e = req(t, "POST", fmt.Sprintf("/counter/appraisals/%d/resolve-appeal", a3), counter,
		map[string]interface{}{"uphold": true, "note": "复核出库照片成立"}, nil)
	mustOK(t, code, e["error"])
	logs3 := rentalLogs(t, 4, g3)
	assertLogAmount(t, logs3, "deduct_reversal", 525)
}

// createDayRentalWithSki 下单（日场，可指定保险）、确认、整单出库，返回租赁单与雪板明细 id
func createDayRentalWithSki(t *testing.T, guest, counter string, insuranceID int64) (int64, int64) {
	t.Helper()
	var slots []struct {
		ID        int64 `json:"id"`
		Remaining int   `json:"remaining"`
	}
	req(t, "GET", "/slots?session=day", guest, nil, &slots)
	var slotID int64
	for _, s := range slots {
		if s.Remaining >= 1 {
			slotID = s.ID
			break
		}
	}
	if slotID == 0 {
		t.Skip("无可用日场时段")
	}
	body := map[string]interface{}{
		"session_type": "day", "slot_id": slotID,
		"include": map[string]bool{"protector": true},
	}
	if insuranceID > 0 {
		body["insurance_product_id"] = insuranceID
	}
	var created struct {
		Rentals []struct {
			RentalID int64 `json:"rental_id"`
		} `json:"rentals"`
	}
	code, e := req(t, "POST", "/rentals", guest, body, &created)
	mustOK(t, code, e["error"])
	rid := created.Rentals[0].RentalID
	req(t, "POST", fmt.Sprintf("/rentals/%d/confirm", rid), guest, map[string]string{}, nil)
	code, e = req(t, "POST", "/counter/checkout", counter,
		map[string]interface{}{"rental_id": rid}, nil)
	mustOK(t, code, e["error"])
	var d struct {
		Items []struct {
			ID            int64  `json:"id"`
			EquipmentType string `json:"equipment_type"`
		} `json:"items"`
	}
	req(t, "GET", fmt.Sprintf("/rentals/%d", rid), guest, nil, &d)
	for _, it := range d.Items {
		if it.EquipmentType == "ski" {
			return rid, it.ID
		}
	}
	t.Fatal("未找到雪板明细")
	return 0, 0
}

func rentalLogs(t *testing.T, rid int64, token string) []struct {
	Type   string  `json:"type"`
	Amount float64 `json:"amount"`
} {
	t.Helper()
	var d struct {
		DepositLogs []struct {
			Type   string  `json:"type"`
			Amount float64 `json:"amount"`
		} `json:"deposit_logs"`
	}
	req(t, "GET", fmt.Sprintf("/rentals/%d", rid), token, nil, &d)
	return d.DepositLogs
}

func assertLogAmount(t *testing.T, logs []struct {
	Type   string  `json:"type"`
	Amount float64 `json:"amount"`
}, typ string, want float64) {
	t.Helper()
	for _, l := range logs {
		if l.Type == typ && l.Amount == want {
			return
		}
	}
	t.Fatalf("押金流水缺少 %s=%.2f，实际 %+v", typ, want, logs)
}
