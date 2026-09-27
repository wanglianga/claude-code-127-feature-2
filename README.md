# 滑雪场雪具租赁尺码调换与损坏赔付平台

围绕「同一套雪具」把 **游客、柜台、教练、维修间、保险、财务、管理员** 七个角色串到同一条数据链路上的全栈演示项目：
购票/报名课程后按身高体重、脚码、头围、水平、课程时段生成雪板 / 雪鞋 / 头盔 / 雪杖 / 护具租赁建议；
柜台扫码出库（可逐件出库拍照留档），教练课前确认合脚与适配、可按课程区域登记学员摔倒；游客中途可换码、延时、报损；
归还时登记板刃损伤、护具裂纹、雪鞋内衬潮湿等，**发现板刃缺口可转维修间做四方证据鉴定（出库照片 / 课程区域与摔倒记录 / 归还照片 / 游客说明），
认定责任后押金扣减、维修工单、保险理赔分别生成，雪具暂停出租计时并影响库存推荐，游客可申诉、柜台调出证据复核**；
维修间开工单并给高损耗型号生成保养提醒，保险自动立案与理赔状态同步，财务留痕押金扣减/冲抵/撤销/退还，
管理员一屏掌握「每套雪具在谁手里、是否适配课程、是否可继续出租、暂停出租多久」与换码拥堵/安全提醒分析。

技术栈：**React 18 + Vite（前端） · Go 1.19 net/http（后端，仅 pq/bcrypt 两个依赖） · PostgreSQL 15**。

---

## 一、目录结构

```
.
├── backend/
│   ├── cmd/server            # HTTP 服务（同时托管前端构建产物）
│   ├── cmd/seed             # 建库种子数据（-reset 清空重建）
│   ├── internal/
│   │   ├── rules/           # 纯函数规则引擎：尺码推荐、押金/赔付/保险/延时（含单测）
│   │   ├── db/              # 连接与嵌入式 SQL 迁移（migrations_sql/）
│   │   ├── auth/            # bcrypt + 自实现 HS256 JWT
│   │   ├── api/             # 全部 HTTP 处理器（含真实 PG 集成测试 api_test.go）
│   │   ├── config/          # 环境变量配置
│   │   └── seed/            # 7 角色账号、雪具、课程、时段、保险、示例订单
│   ├── scripts/e2e.sh       # 端到端冒烟脚本（curl 驱动七角色全流程）
│   └── go.mod
├── frontend/
│   ├── src/pages            # Guest / Counter / Coach / Repair / Insurance / Finance / Admin
│   ├── src/components       # 租赁单详情（含换码/延时/损坏/反馈弹窗）、通用 UI
│   └── package.json
└── scripts/dev-postgres.sh  # 无 root 权限时用户态初始化 PostgreSQL
```

---

## 二、本地启动

### 1. 准备 PostgreSQL（任选一种）

**A. 本机已有 PostgreSQL**：建一个账号可连的库即可，连接串通过 `DATABASE_URL` 覆盖。

**B. 没有系统库的 root 权限（本项目开发机就是这种情况）**，用项目脚本在用户目录初始化：

```bash
bash scripts/dev-postgres.sh start     # 默认 127.0.0.1:5433，trust 免密，自动建 skirental 库
```

默认连接串：`postgres://postgres@127.0.0.1:5433/skirental?sslmode=disable`
（可用环境变量 `DATABASE_URL` 改成任意库；`scripts/dev-postgres.sh stop` 停止）

### 2. 启动后端（自动迁移 + 写种子数据）

```bash
cd backend
go run ./cmd/seed -reset              # 首次：建表 + 演示数据；以后重复执行会清空重建
go run ./cmd/server                   # 默认 :8080
```

环境变量（均可缺省）：`PORT`（8080）、`DATABASE_URL`、`JWT_SECRET`、`UPLOAD_DIR`、
`STATIC_DIR`（默认 `../frontend/dist`，存在该目录时同时托管前端）。

### 3. 启动前端

开发模式（热更新，`/api` 与 `/uploads` 自动代理到 8080）：

```bash
cd frontend
npm install --cache /tmp/npm-cache   # 若 ~/.npm 无写权限，指定临时缓存
npm run dev                          # http://localhost:5173
```

生产构建（产物由 Go 直接托管，访问 http://localhost:8080 即可）：

```bash
cd frontend && npm run build
cd ../backend && go run ./cmd/server
```

> 若修改后端后想立即在 8080 看到最新行为：`go build -o /tmp/ski-server ./cmd/server && STATIC_DIR=$PWD/../frontend/dist /tmp/ski-server`

### 4. 一键端到端验证（需要后端跑在 8080、种子数据为初始状态）

```bash
cd backend
go run ./cmd/seed -reset
bash scripts/e2e.sh
```

---

## 三、测试账号（密码均为 `ski123456`）

| 角色 | 姓名 | 手机号 | 能做什么 |
|---|---|---|---|
| 游客（初学者） | 张明 | 13900000001 | 看建议、下单预约、换码/延时/报损、排队归还、看押金依据、评价 |
| 游客（进阶） | 李华 | 13900000002 | 已出库示例单 R-DEMO-B，可直接体验课前确认、换码 |
| 游客（夜场） | 王芳 | 13900000003 | 夜场单 R-DEMO-C：板刃损伤 + 维修工单 + 保险理赔 + 夜场归还队列 |
| 游客（队长） | 刘队长 | 13900000004 | 「追风滑雪团」3 人团队单 |
| 游客（鉴定） | 赵雪 | 13900000007 | 日场单 R-DEMO-D：归还发现板刃缺口，**四方证据鉴定中**，可提交说明/申诉 |
| 教练 | 陈教练 | 13800000002 | 课前逐件确认雪鞋合脚/雪板适合初学者，按课程区域登记学员摔倒，不合规推送安全提醒 |
| 柜台 | 孙柜台 | 13800000003 | 排队大屏、扫码出库（出库拍照）、换码处理、归还验损结算、板刃转鉴定、申诉复核 |
| 维修间 | 周师傅 | 13800000004 | 板刃鉴定工作台（四方证据定责）、维修工单流转、高损耗型号保养提醒 |
| 保险 | 吴理赔 | 13800000005 | 理赔立案→审核→核定→赔付到账（自动冲抵押金）、保单查询 |
| 财务 | 郑财务 | 13800000006 | 收支总览、押金流水台账（冻结/扣减/冲抵/退还） |
| 管理员 | 赵店长 | 13800000001 | 型号损耗排行、课程换码拥堵、分时取具压力、安全提醒名单、雪具全程追踪、型号/入库 |

种子数据包含：3 堂课程（今日日场初学、今夜夜场进阶、明日进阶）、未来 3 天日/夜分时段取具槽（部分已约满演示旺季）、
3 档保险套餐（基础险/安心险/夜场全险）、5 类雪具 × 全尺码库存（每件唯一二维码）、3 个处于不同状态的示例订单。

---

## 四、推荐的手工验证路径（约 5 分钟）

1. **游客 张明** 登录 →「尺码建议」看 170cm 初学者为何推荐 145cm 短板/EU42 雪鞋/M 头盔；
   「新建租赁预约」选课程 + 分时段取具槽 + 保险，生成建议单；在「我的租赁」确认预约（雪具预留并入取具队列）。
2. **柜台** → 排队大屏看到取具票 → 「扫码出库」输入单号 `1` 整单出库（或逐个扫 `QR-...` 核验）。
3. **教练** → 把张明雪板标记「不合规」→ 切回游客单据能看到**安全提醒**。
4. **游客** → 申请雪鞋换码（EU42→EU42.5）、课程延时、或上报「雪鞋内衬潮湿」；
   **柜台** → 换码队列叫号并自动选码换发；损坏队列打开单据「柜台评定定级」，系统自动开维修工单、有保险自动立案、同型号雪具自动换发。
5. **游客** → 排队**日场归还**；**柜台** → 逐件验具（完好上架 / 选板刃损伤等登记）→ 完成归还，看到使用时长、扣减、退款。
6. **维修间** → 处理工单（开始/等配件/修复上架/报废）→「生成保养提醒」看到高损耗型号与损坏率。
7. **保险** → 对夜场单 R-DEMO-C 的板刃理赔走 立案→审核→核定→**赔付到账**（自动向押金流水写入冲抵返还）。
8. **财务** → 总览与台账核对冻结/扣减/冲抵/退还；**管理员** → 看型号损耗排行、该课程换码拥堵、分时取具占用率、需安全提醒游客、雪具「在谁手里/是否适配/能否出租/暂停出租多久」。
9. 夜场归还在柜台大屏是与日场**分开的独立队列**（`return_night` vs `return_day`）。
10. **板刃损伤鉴定（赵雪 R-DEMO-D）**：
    - **柜台** →「归还验损」打开 R-DEMO-D，雪板已显示「🔍 板刃鉴定中」（或在新单验具时选「板刃缺口存疑」并拍归还照片）；雪具立即**暂停出租**。
    - **维修间** →「板刃鉴定工作台」打开鉴定单，左看出库照片/右看归还照片/下方看课程区域摔倒记录与游客说明 → 认定「游客责任 / 出库前已存在 / 无责」：
      游客责任→**押金扣减 + 维修工单 + 保险理赔三单分别生成**；出库前已存在→平台承担维修、不扣押金；无责→直接上架恢复出租。
    - 未鉴定先点「完成归还」时该件押金自动**暂扣**（`appraisal_hold`），定责后多退少补；
      「型号目录」可见该型号「暂停 1 件」、可用库存减少（暂停出租影响库存推荐）。
    - **游客 赵雪** → 单据中提交说明/申诉；**柜台** →「板刃鉴定」页调出课程区域与归还照片，维持或撤销（撤销自动写 `deduct_reversal` 返还扣减、撤回理赔）。
    - **维修间** → 工单「修复上架」后雪具恢复出租，管理员雪具追踪可见暂停起止时间。

---

## 五、自动化测试

```bash
cd backend
go test ./...                       # 规则引擎单测 + 真实 PostgreSQL HTTP 集成测试
go test -race ./internal/api/       # 竞态检测
```

- `internal/rules/rules_test.go`：雪板长度（初学者 0.85/进阶 0.90/高级 0.95 身高系数 + BMI 调整 + 5cm 取整）、
  雪鞋欧码 ±0.5 容差、头盔 S/M/L/XL 头围档、雪杖 0.7 身高、护具分档、
  损坏矩阵（板刃 35%、内衬 5%、裂纹 50%、丢失 100%，轻微 ×0.5 / 严重 ×1.5）、保险免赔/保额、延时费。
- `internal/api/api_test.go`：自动重建 `skirental_test` 库、跑 HTTP 全链路——
  登录鉴权/越权、尺码建议、**个人单完整生命周期**（下单→预约→出库→扫码持有人→教练确认与安全提醒→
  EU45→EU45.5 跨码换鞋→中途板刃损伤自动换发/维修/立案→逐件归还→结算扣减 525→
  保险核定赔 475 冲抵→评价→管理分析）、**夜场独立归还队列**（夜场租金 +20% 校验）、**团队 3 单 + 高优先级取具票**、保养提醒生成、
  **板刃损伤鉴定完整生命周期**（出库拍照→课程区域摔倒登记→归还转鉴定暂停出租→型号 suspended 影响推荐→
  游客说明→结算暂扣 1500→维修间四方证据定游客责任（扣 525/工单/理赔分别生成）→暂扣余款 975 退还→
  申诉维持→理赔 475 冲抵→修复记录 resumed_at；无责直接上架；种子单暂扣+申诉成立撤销返还 525）。

测试需要可连 PostgreSQL（默认 127.0.0.1:5433），用 `DATABASE_URL` 指向任意可建库的实例即可；
每次运行自动 `DROP/CREATE DATABASE skirental_test WITH (FORCE)`，不影响开发库。

---

## 六、核心业务规则（实现于 `internal/rules`）

- **雪板长度**：初学者身高×0.85（短板易控）、进阶×0.90、高级×0.95，BMI≥26 +5cm、BMI<18 −3cm，按 5cm 取整，夹在 100–185cm。
- **雪鞋**：直接按 EU 脚码，合脚容差 ±0.5；教练确认超出容差会强制「不合规」。
- **头盔**：头围 ≤54 S / ≤57 M / ≤60 L / 更大 XL，并提示佩戴贴合检查。
- **雪杖**：身高×0.7 按 5cm 取整（95–130cm）；初学者首课标记为「可选」。
- **护具**：按身高体重分 S/M/L/XL，初学者强制建议佩戴。
- **费用**：单件按型号租金/押金；**夜场租金 +20%**（押金不变）；延时按租金折算时租×小时。
- **损坏赔付矩阵**：板刃损伤 35%（磨刃/补刃，2 天）、板底划伤 15%、固定器故障 30%、内衬潮湿 5%（烘干消毒）、
  壳体裂纹 50%（停止出租/换壳）、绑带断裂 12%、丢失 100%；轻微 ×0.5、严重 ×1.5，封顶不超押金。
- **保险**：套餐含免赔额、保额上限、保障损坏类型与适用场次；理赔额 = min(扣减−免赔, 保额上限)，
  赔付到账后以「保险冲抵返还」退回游客押金；低于免赔不赔但仍留状态依据。
- **排队**：取具 / 换码 / 日场归还 / 夜场归还 / 损坏处理五类独立队列，团队票高优先级，配合分时段取具槽削峰。
- **保养提醒**：近 30 天同型号损坏 ≥3 起、或损坏率 ≥20%、或发生丢失即生成，附库存/损坏/维修成本指标。
- **板刃损伤鉴定**（归还发现缺口、责任不明时）：
  - **四方证据**：出库照片（`rental_items.checkout_photos`，柜台出库/补拍）、课程区域与摔倒记录（`fall_records`，教练登记或游客自报）、
    归还照片（柜台初检拍摄）、游客说明（鉴定中可补充）。
  - **三种责任结论**：`guest` 游客责任→按板刃矩阵扣押金（轻微 ×0.5/严重 ×1.5）、开维修工单、有效保单覆盖板刃自动立案；
    `preexist` 出库前已存在→平台承担维修费、不扣押金、不走保险；`none` 证据不足/正常磨损→不扣不立案，雪具核验上架（可勾选转保养维修）。
  - **暂扣与结清**：完成归还时仍在鉴定中的雪具暂扣该件押金（`hold_appraisal`），定责后游客责任「暂扣−扣减」退还、
    非游客责任全额退还；申诉成立写 `deduct_reversal` 等额返还并撤回未赔付理赔（已赔付需线下处理）。
  - **暂停出租**：转鉴定即把雪具置 `inspection` 并记 `suspended_at`，修复上架/无责上架记 `resumed_at`；
    暂停件不计入型号 `available`，选件与推荐只认 `available`，即「暂停出租影响库存推荐」。
  - 柜台与维修间共用同一份鉴定数据（`GET /counter/edge-appraisals` 与 `GET /repair/edge-appraisals`）实现同步查看。

---

## 七、主要 HTTP 接口（前缀 `/api`）

| 模块 | 接口示例 |
|---|---|
| 认证/目录 | `POST /auth/login` `GET /me` `GET /courses` `GET /slots` `GET /models` `GET /insurance-products` |
| 游客 | `POST /guest/suggestions` `POST /teams` `POST /rentals` `POST /rentals/{id}/confirm` `POST /rentals/{id}/exchange-request` `POST /rentals/{id}/extend` `POST /rentals/{id}/damage` `POST /rentals/{id}/fall-record` `POST /rentals/{id}/queue-return` `POST /rentals/{id}/feedback` `POST /appraisals/{id}/statement` `POST /appraisals/{id}/appeal` |
| 柜台 | `GET /counter/queue` `POST /counter/call` `POST /counter/scan` `POST /counter/checkout` `POST /counter/checkout-photos` `POST /counter/exchanges/{id}/process` `POST /counter/damages/{id}/assess` `POST /counter/returns/{rid}/inspect`（result 含 `suspect_edge` 转鉴定）`POST /counter/returns/{rid}/finish`（返回 `appraisal_hold`）`GET /counter/edge-appraisals` `POST /counter/appraisals/{id}/resolve-appeal` |
| 教练 | `GET /coach/courses` `POST /coach/fit-check` `POST /coach/fall-record` |
| 维修间 | `GET /repair/orders` `POST /repair/orders/{id}/update` `GET /repair/edge-appraisals` `POST /repair/appraisals/{id}/confirm` `GET/repair/maintenance` `POST /repair/maintenance/generate` `POST /repair/maintenance/{id}/resolve` |
| 保险 | `GET /insurance/claims` `POST /insurance/claims/{id}/review` `POST /insurance/claims/{id}/pay` `GET /insurance/policies` |
| 财务 | `GET /finance/summary` `GET /finance/ledger` |
| 管理 | `GET /admin/analytics` `GET /admin/equipment-trace` `POST /admin/models` `POST /admin/items` |
| 照片 | `POST /uploads`（multipart，损坏取证） |

---

## 八、本次实际执行过的验证（2026-09-26）

1. `go vet ./...`、`gofmt -l .` 均无问题；`go test ./...` 全部通过（规则引擎 11 个用例 + 7 个真实 PG 集成测试用例），`go test -race ./...` 通过。
2. 前端 `npm run build` 成功（单 JS ~235KB，gzip 71KB），由 Go 服务在 `http://localhost:8080` 托管，根路径与静态资源均返回 200。
3. 重置种子后执行 `backend/scripts/e2e.sh` 全流程通过：团队 3 人批量下单、分时预约、出库、教练不合规安全提醒、
   EU43.5→EU44 跨型号换码、中途内衬潮湿自动换发（扣 25 元）、日场结算退 2825、夜场板刃定损 525 + 夜场全险赔付 525 冲抵后退 2700（游客零损失）、
   维修工单完成、高损耗保养提醒、财务台账、管理分析、游客反馈，
   以及新增的 **J 段板刃损伤鉴定**：教练课程区域摔倒登记 + 游客说明、柜台同步查看四方证据（出库照 1/归还照 1/摔倒 2）、
   未鉴定先结算暂扣雪板押金 1500（退 950）、维修间认定游客责任后扣减 525/损坏单/维修工单/保险理赔分别生成、
   SKI135 型号库存 4/可用 3/暂停 1（暂停件不计入推荐库存）、游客申诉柜台复核维持、工单修复后雪具恢复出租（暂停 16:37→恢复 16:50）。
4. 集成测试 `TestEdgeAppraisalLifecycle` 额外覆盖：无责认定直接上架恢复出租、申诉成立撤销扣减（`deduct_reversal` 525）并撤回理赔、
   暂停雪具计入型号 `suspended` 影响 `GET /models` 推荐库存、修复上架写 `resumed_at`。

> 说明：本项目为演示/作业工程，JWT 密钥与数据库 trust 认证仅适用于本地，生产部署需替换密钥、收紧数据库权限并加上 HTTPS 与文件上传校验。
