-- 板刃损伤鉴定增强：四方证据（出库照片/课程区域/摔倒记录/游客说明）、
-- 责任认定、押金扣减/维修工单/保险理赔三单分离、雪具暂停出租计时、申诉

-- 1. 租赁明细：逐件出库照片（鉴定时与归还照片对比）
ALTER TABLE rental_items ADD COLUMN checkout_photos JSONB NOT NULL DEFAULT '[]';

-- 2. 租赁明细状态增加：板刃损伤鉴定中（雪具暂扣、暂停出租）
ALTER TABLE rental_items DROP CONSTRAINT IF EXISTS rental_items_status_check;
ALTER TABLE rental_items ADD CONSTRAINT rental_items_status_check
    CHECK (status IN ('suggested','assigned','out','exchanged','returned_ok',
                      'returned_damaged','repair','lost','edge_appraisal'));

-- 3. 单件雪具：暂停出租起止时间（发现缺口暂扣 -> 维修/核验完成恢复）
ALTER TABLE equipment_items ADD COLUMN suspended_at TIMESTAMPTZ;
ALTER TABLE equipment_items ADD COLUMN resumed_at   TIMESTAMPTZ;

-- 4. 损坏报告增加责任归属（鉴定结论：游客责任 / 出库前已存在 / 无责）
ALTER TABLE damage_reports ADD COLUMN responsibility TEXT NOT NULL DEFAULT 'guest'
    CHECK (responsibility IN ('guest','preexist','none'));

-- 5. 摔倒记录（教练登记或游客自报；课程区域是鉴定证据之一）
CREATE TABLE fall_records (
    id             BIGSERIAL PRIMARY KEY,
    rental_id      BIGINT NOT NULL REFERENCES rentals(id),
    rental_item_id BIGINT REFERENCES rental_items(id),
    guest_id       BIGINT NOT NULL REFERENCES users(id),
    course_area    TEXT NOT NULL DEFAULT '',     -- 课程区域：魔毯区/初级道/中级道/公园道具区…
    happened_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    description    TEXT NOT NULL DEFAULT '',
    source         TEXT NOT NULL DEFAULT 'coach'
                   CHECK (source IN ('coach','guest','system')),
    recorded_by    BIGINT REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_falls_rental ON fall_records(rental_id);

-- 6. 板刃损伤鉴定单
CREATE TABLE edge_appraisals (
    id              BIGSERIAL PRIMARY KEY,
    rental_id       BIGINT NOT NULL REFERENCES rentals(id),
    rental_item_id  BIGINT NOT NULL REFERENCES rental_items(id),
    item_id         BIGINT NOT NULL REFERENCES equipment_items(id),
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','confirmed','disputed','reversed','closed')),
    -- 四方证据
    checkout_photos JSONB NOT NULL DEFAULT '[]',   -- 出库照片快照
    fall_snapshot   JSONB NOT NULL DEFAULT '[]',   -- 摔倒记录快照（时间/区域/描述/来源）
    course_areas    TEXT NOT NULL DEFAULT '',       -- 课程区域汇总
    guest_statement TEXT NOT NULL DEFAULT '',       -- 游客说明
    return_photos   JSONB NOT NULL DEFAULT '[]',   -- 归还（发现缺口）照片
    return_note     TEXT NOT NULL DEFAULT '',       -- 柜台初检描述
    severity        TEXT NOT NULL DEFAULT 'medium'
                    CHECK (severity IN ('light','medium','heavy')),
    -- 维修间责任认定
    responsibility  TEXT NOT NULL DEFAULT 'pending'
                    CHECK (responsibility IN ('pending','guest','preexist','none')),
    confirmed_by    BIGINT REFERENCES users(id),
    confirmed_at    TIMESTAMPTZ,
    confirm_note    TEXT NOT NULL DEFAULT '',
    deduction       NUMERIC(10,2) NOT NULL DEFAULT 0,
    damage_id       BIGINT REFERENCES damage_reports(id),
    repair_order_id BIGINT REFERENCES repair_orders(id),
    claim_id        BIGINT REFERENCES insurance_claims(id),
    -- 游客申诉与柜台复核
    appeal_reason   TEXT NOT NULL DEFAULT '',
    appealed_at     TIMESTAMPTZ,
    appeal_status   TEXT NOT NULL DEFAULT 'none'
                    CHECK (appeal_status IN ('none','requested','upheld','rejected')),
    handled_by      BIGINT REFERENCES users(id),
    handled_at      TIMESTAMPTZ,
    handle_note     TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_edge_appraisals_rental ON edge_appraisals(rental_id);
CREATE INDEX idx_edge_appraisals_status ON edge_appraisals(status);

-- 7. 押金流水：鉴定暂扣 / 申诉撤销返还；并可关联到鉴定单
ALTER TABLE deposit_transactions DROP CONSTRAINT IF EXISTS deposit_transactions_type_check;
ALTER TABLE deposit_transactions ADD CONSTRAINT deposit_transactions_type_check
    CHECK (type IN ('hold','deduct_damage','deduct_lost','deduct_late',
                    'insurance_offset','deduct_reversal','hold_appraisal','refund'));
ALTER TABLE deposit_transactions ADD COLUMN appraisal_id BIGINT REFERENCES edge_appraisals(id);
