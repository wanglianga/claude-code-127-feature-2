-- 板刃损伤鉴定：证据对比（出库照片 / 课程区域 / 摔倒记录 / 游客说明）、
-- 责任判定与申诉、雪具暂停出租时间记录。金额单位：人民币元。

-- 课程滑行区域（鉴定时用于核对板刃缺口可能发生的区域）
ALTER TABLE courses ADD COLUMN zones TEXT[] NOT NULL DEFAULT '{}';

-- 摔倒 / 碰撞记录（教练课前课中登记，作为板刃损伤鉴定的客观依据）
CREATE TABLE fall_records (
    id            BIGSERIAL PRIMARY KEY,
    rental_id     BIGINT REFERENCES rentals(id),
    course_id     BIGINT REFERENCES courses(id),
    guest_id      BIGINT NOT NULL REFERENCES users(id),
    zone          TEXT NOT NULL DEFAULT '',        -- 发生区域，如 初级道A段 / 魔毯区
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    severity      TEXT NOT NULL DEFAULT 'light'
                  CHECK (severity IN ('light','medium','heavy')),
    description   TEXT NOT NULL DEFAULT '',
    recorded_by   BIGINT REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_fall_guest ON fall_records(guest_id);
CREATE INDEX idx_fall_rental ON fall_records(rental_id);

-- 出库留档照片（柜台扫码出库时逐件拍摄，归还鉴定时与损伤照片对比）
CREATE TABLE checkout_photos (
    id              BIGSERIAL PRIMARY KEY,
    rental_item_id  BIGINT NOT NULL REFERENCES rental_items(id),
    item_id         BIGINT NOT NULL REFERENCES equipment_items(id),
    url             TEXT NOT NULL,
    taken_by        BIGINT REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_checkout_photos_ri ON checkout_photos(rental_item_id);
CREATE INDEX idx_checkout_photos_item ON checkout_photos(item_id);

-- 板刃损伤鉴定单
CREATE TABLE edge_assessments (
    id              BIGSERIAL PRIMARY KEY,
    code            TEXT NOT NULL UNIQUE,
    rental_id       BIGINT NOT NULL REFERENCES rentals(id),
    rental_item_id  BIGINT NOT NULL REFERENCES rental_items(id),
    item_id         BIGINT NOT NULL REFERENCES equipment_items(id),
    damage_id       BIGINT REFERENCES damage_reports(id),
    stage           TEXT NOT NULL CHECK (stage IN ('during_rental','at_return')),
    -- 鉴定证据包
    checkout_photos JSONB NOT NULL DEFAULT '[]',   -- 出库照片（对比基准）
    return_photos   JSONB NOT NULL DEFAULT '[]',   -- 归还/缺口照片
    course_zones    TEXT NOT NULL DEFAULT '',      -- 课程区域（自动汇总课程 zones 与摔倒区域）
    fall_summary    TEXT NOT NULL DEFAULT '',      -- 摔倒记录摘要（自动汇总，可人工补充）
    guest_statement TEXT NOT NULL DEFAULT '',      -- 游客说明
    staff_finding   TEXT NOT NULL DEFAULT '',      -- 维修间目检发现（缺口位置/长度等）
    -- 责任判定
    severity        TEXT NOT NULL DEFAULT 'medium'
                    CHECK (severity IN ('light','medium','heavy')),
    responsibility  TEXT NOT NULL DEFAULT 'pending'
                    CHECK (responsibility IN ('pending','guest_liable','not_liable','disputed')),
    deduction       NUMERIC(10,2) NOT NULL DEFAULT 0,
    conclusion      TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open','confirmed','appealed','upheld','overturned','closed')),
    -- 确认责任后分别生成的下游单据
    repair_order_id BIGINT,
    claim_id        BIGINT,
    deposit_tx_id   BIGINT,
    opened_by       BIGINT REFERENCES users(id),
    confirmed_by    BIGINT REFERENCES users(id),
    confirmed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_edge_rental ON edge_assessments(rental_id);
CREATE INDEX idx_edge_item ON edge_assessments(item_id);
CREATE INDEX idx_edge_status ON edge_assessments(status);

-- 鉴定申诉（游客不认可责任判定时提交，柜台调课程区域与归还照片复核）
CREATE TABLE assessment_appeals (
    id            BIGSERIAL PRIMARY KEY,
    assessment_id BIGINT NOT NULL REFERENCES edge_assessments(id) ON DELETE CASCADE,
    guest_id      BIGINT NOT NULL REFERENCES users(id),
    reason        TEXT NOT NULL DEFAULT '',
    evidence_note TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'submitted'
                  CHECK (status IN ('submitted','reviewing','upheld','overturned')),
    counter_note  TEXT NOT NULL DEFAULT '',
    handled_by    BIGINT REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    handled_at    TIMESTAMPTZ
);
CREATE INDEX idx_appeals_assessment ON assessment_appeals(assessment_id);

-- 雪具暂停出租时间窗：维修间确认板刃损伤即停租（同时 item.status='repair'），
-- 修复上架时写 resumed_at；暂停期间不可被库存推荐/自动选码命中。
ALTER TABLE equipment_items ADD COLUMN suspended_at TIMESTAMPTZ;
ALTER TABLE equipment_items ADD COLUMN suspended_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE equipment_items ADD COLUMN resumed_at TIMESTAMPTZ;

-- 押金流水增加「申诉翻案退回」类型
ALTER TABLE deposit_transactions DROP CONSTRAINT IF EXISTS deposit_transactions_type_check;
ALTER TABLE deposit_transactions ADD CONSTRAINT deposit_transactions_type_check
    CHECK (type IN ('hold','deduct_damage','deduct_lost','deduct_late',
                    'insurance_offset','appeal_reversal','refund'));
