import React, { useEffect, useState, useCallback } from 'react'
import { api, yuan, TYPE_LABEL, DAMAGE_LABEL, RESP_LABEL, APPRAISAL_STATUS } from '../api.js'
import { Badge, Modal, Field, Loading, Empty, useToast } from '../components/ui.jsx'

export default function RepairPage({ page }) {
  if (page === 'maintenance') return <Maintenance />
  if (page === 'appraisals') return <AppraisalBench />
  return <Orders />
}

const RO_STATUS = { queued: ['排队待修', 'orange'], in_progress: ['维修中', 'blue'], waiting_part: ['等配件', 'purple'], done: ['修复可租', 'green'], scrapped: ['已报废', 'red'] }

function Orders() {
  const [orders, setOrders] = useState(null)
  const [detail, setDetail] = useState(null)
  const toast = useToast()
  const load = useCallback(async () => setOrders(await api('GET', '/repair/orders')), [])
  useEffect(() => { load() }, [])
  const [updating, setUpdating] = useState(null)

  const update = async (o, status) => {
    try {
      const d = await api('POST', `/repair/orders/${o.id}/update`, { status, advice: o.advice, repair_cost: Number(o.repair_cost) || 0 })
      toast(d.message); load()
    } catch (e) { toast(e.message, 'error') }
  }

  if (!orders) return <Loading />
  return (
    <div>
      <h2 style={{ fontSize: 18 }}>雪具维修工单</h2>
      <p className="muted small">柜台验损/游客上报自动开单。修复完成雪具重新上架可租；裂纹严重件可直接报废。</p>
      {orders.length === 0 && <Empty text="当前没有待修工单" />}
      <div className="grid grid-2">
        {orders.map(o => (
          <div className="card" key={o.id}>
            <div className="btn-row">
              <h3 style={{ margin: 0 }}>#{o.id} {o.brand} {o.name} {o.size_label}</h3>
              <span className="spacer" style={{ flex: 1 }} />
              <Badge color={(RO_STATUS[o.status] || [, 'gray'])[1]}>{RO_STATUS[o.status]?.[0] || o.status}</Badge>
            </div>
            <div className="small muted">{TYPE_LABEL[o.equipment_type]} · <span className="mono">{o.qr_code}</span> · 累计出租 {o.rental_count} 次</div>
            {o.suspended_at && <div className="small" style={{ color: '#b45309' }}>
              ⏸ 暂停出租：{o.suspended_at}{o.resumed_at ? ` → 恢复 ${o.resumed_at}` : '（进行中）'}
              {o.suspended_seconds > 0 && ` · 已暂停 ${formatDur(o.suspended_seconds)}`}
            </div>}
            <div style={{ margin: '8px 0' }}><b>问题：</b>{o.issue}</div>
            <div className="small">系统维修建议：{o.advice}</div>
            {o.damage_type && <div className="small">损坏类型：{DAMAGE_LABEL[o.damage_type]}（{o.severity}）</div>}
            {o.photos?.length > 0 && <div className="photos">{o.photos.map((p, i) => <img key={i} src={p} alt="" />)}</div>}
            <div className="row" style={{ marginTop: 8 }}>
              <Field label="实际维修成本（元）"><input type="number" step="0.01" defaultValue={o.repair_cost}
                onChange={e => { o.repair_cost = e.target.value }} /></Field>
              <Field label="维修说明/保养建议"><input defaultValue={o.advice} onChange={e => { o.advice = e.target.value }} /></Field>
            </div>
            <div className="btn-row" style={{ marginTop: 8 }}>
              {o.status === 'queued' && <button className="btn sm" onClick={() => update(o, 'in_progress')}>开始维修</button>}
              {o.status !== 'done' && o.status !== 'scrapped' && <>
                <button className="btn sm" onClick={() => update(o, 'waiting_part')}>等配件</button>
                <button className="btn sm" onClick={() => update(o, 'done')}>修复上架</button>
              </>}
              {o.status !== 'scrapped' && o.status !== 'done' && <button className="btn sm danger" onClick={() => update(o, 'scrapped')}>报废</button>}
              <span className="spacer" style={{ flex: 1 }} />
              {o.damage_id && <button className="btn sm ghost" onClick={() => setDetail(o)}>关联单据</button>}
            </div>
          </div>
        ))}
      </div>
      {detail && <RelatedRental order={detail} onClose={() => { setDetail(null); load() }} />}
    </div>
  )
}

// 从维修工单反查租赁单：借助 equipment-trace 找 current_rental_item_id 已被置空，这里展示损坏详情即可
function RelatedRental({ order, onClose }) {
  const toast = useToast()
  return (
    <Modal title={`工单 #${order.id} 关联信息`} onClose={onClose}>
      <p>损坏报告 #{order.damage_id}：{DAMAGE_LABEL[order.damage_type]}（{order.severity}）</p>
      <p className="small muted">{order.issue}</p>
      {order.photos?.length > 0 && <div className="photos">{order.photos.map((p, i) => <img key={i} src={p} alt="" />)}</div>}
      <div className="modal-foot"><button className="btn" onClick={onClose}>关闭</button></div>
    </Modal>
  )
}

function Maintenance() {
  const [list, setList] = useState(null)
  const toast = useToast()
  const load = useCallback(async () => setList(await api('GET', '/repair/maintenance')), [])
  useEffect(() => { load() }, [])
  const generate = async () => {
    try { toast((await api('POST', '/repair/maintenance/generate', {})).message); load() }
    catch (e) { toast(e.message, 'error') }
  }
  const resolve = async (id, action) => {
    try { await api('POST', `/repair/maintenance/${id}/resolve`, { action }); toast('提醒已处理'); load() }
    catch (e) { toast(e.message, 'error') }
  }
  if (!list) return <Loading />
  return (
    <div>
      <div className="btn-row">
        <h2 style={{ margin: 0, fontSize: 18 }}>高损耗型号保养提醒</h2>
        <span className="spacer" style={{ flex: 1 }} />
        <button className="btn" onClick={generate}>🔧 按近 30 天损耗数据生成提醒</button>
      </div>
      <p className="muted small">规则：近 30 天同型号损坏 ≥3 起，或损坏率 ≥20%，或发生丢失 → 生成整批保养/盘点提醒。</p>
      {list.length === 0 && <Empty text="暂无保养提醒，点击右上角按钮生成" />}
      <div className="grid grid-2">
        {list.map(m => (
          <div className="card" key={m.id} style={{ opacity: m.status === 'open' ? 1 : 0.6 }}>
            <div className="btn-row">
              <b>{m.brand} {m.name} {m.size_label}</b>
              <span className="spacer" style={{ flex: 1 }} />
              <Badge color={m.status === 'open' ? 'orange' : 'gray'}>{m.status === 'open' ? '待处理' : m.status === 'done' ? '已保养' : '已忽略'}</Badge>
            </div>
            <div className="alert warn" style={{ margin: '8px 0' }}>{m.reason}</div>
            <div className="small">建议行动：{m.action}</div>
            <div className="small muted" style={{ margin: '6px 0' }}>
              库存 {m.metrics.stock} · 损坏 {m.metrics.damages} · 丢失 {m.metrics.lost} ·
              出库 {m.metrics.rentals} · 损坏率 {m.metrics.damage_rate}% · 维修费 {yuan(m.metrics.repair_cost)}
            </div>
            <div className="small muted">生成于 {m.created_at}</div>
            {m.status === 'open' && <div className="btn-row" style={{ marginTop: 8 }}>
              <button className="btn sm" onClick={() => resolve(m.id, 'done')}>已完成保养</button>
              <button className="btn sm ghost" onClick={() => resolve(m.id, 'dismissed')}>忽略</button>
            </div>}
          </div>
        ))}
      </div>
    </div>
  )
}

function formatDur(sec) {
  const h = Math.floor(sec / 3600), m = Math.round((sec % 3600) / 60)
  if (h <= 0) return `${m} 分钟`
  return `${h} 小时 ${m} 分`
}

// ---------- 板刃损伤鉴定工作台 ----------
function AppraisalBench() {
  const [list, setList] = useState(null)
  const [open, setOpen] = useState(null)
  const load = useCallback(async () => setList(await api('GET', '/repair/edge-appraisals')), [])
  useEffect(() => { load() }, [])
  if (!list) return <Loading />
  const pending = list.filter(a => a.status === 'pending')
  const others = list.filter(a => a.status !== 'pending')
  return (
    <div>
      <h2 style={{ fontSize: 18 }}>板刃损伤鉴定工作台</h2>
      <p className="muted small">
        归还发现板刃缺口后雪具暂扣、暂停出租。对比 <b>出库照片 / 归还照片 / 课程区域与摔倒记录 / 游客说明</b> 四方证据认定责任：
        游客责任（扣押金+工单+保险立案）、出库前已存在（平台承担维修）、无责（核验上架）。
      </p>
      {list.length === 0 && <Empty text="当前没有待鉴定板刃" />}
      {pending.length > 0 && <>
        <h3 style={{ marginTop: 14 }}>待认定（{pending.length}）</h3>
        <div className="grid grid-2">{pending.map(a => <BenchCard key={a.id} a={a} onOpen={setOpen} />)}</div>
      </>}
      {others.length > 0 && <>
        <h3 style={{ marginTop: 14 }}>历史鉴定（{others.length}）</h3>
        <div className="grid grid-2">{others.map(a => <BenchCard key={a.id} a={a} onOpen={setOpen} />)}</div>
      </>}
      {open && <ConfirmModal a={list.find(x => x.id === open)} onClose={() => setOpen(null)} onDone={() => { setOpen(null); load() }} />}
    </div>
  )
}

function BenchCard({ a, onOpen }) {
  return (
    <div className="card" style={a.status === 'pending' ? { borderColor: '#fdba74' } : undefined}>
      <div className="btn-row">
        <h3 style={{ margin: 0, fontSize: 15 }}>鉴定 #{a.id} · {a.rental_code}</h3>
        <span className="spacer" style={{ flex: 1 }} />
        <Badge color={(APPRAISAL_STATUS[a.status] || [, 'gray'])[1]}>{(APPRAISAL_STATUS[a.status] || [a.status])[0]}</Badge>
      </div>
      <div className="small">{a.guest_name} {a.guest_phone} · {a.brand} {a.name} {a.size_label} <span className="mono">{a.qr_code}</span></div>
      <div className="small muted">发起 {a.created_at} · 暂停 {a.suspended_at || '—'}{a.resumed_at ? ' → 恢复 ' + a.resumed_at : ''}</div>
      <div className="btn-row" style={{ marginTop: 6 }}>
        <Badge color="cyan">出库照 {a.checkout_photos?.length || 0}</Badge>
        <Badge color="red">归还照 {a.return_photos?.length || 0}</Badge>
        <Badge color="orange">摔倒 {a.fall_snapshot?.length || 0} · {a.course_areas || '无区域'}</Badge>
        <Badge color={a.guest_statement ? 'green' : 'gray'}>{a.guest_statement ? '游客已说明' : '游客未说明'}</Badge>
      </div>
      {a.responsibility !== 'pending' && <div className="small" style={{ marginTop: 6 }}>
        结论：<Badge color={a.responsibility === 'guest' ? 'red' : 'green'}>{RESP_LABEL[a.responsibility]}</Badge>
        {a.deduction > 0 && <span> 扣 {yuan(a.deduction)}</span>}
      </div>}
      <div className="modal-foot" style={{ paddingRight: 0 }}>
        <button className="btn sm" onClick={() => onOpen(a.id)}>{a.status === 'pending' ? '打开证据并定责' : '查看证据/结论'}</button>
      </div>
    </div>
  )
}

function ConfirmModal({ a, onClose, onDone }) {
  const toast = useToast()
  const [resp, setResp] = useState('guest')
  const [severity, setSeverity] = useState(a.severity === 'light' || a.severity === 'heavy' ? a.severity : 'medium')
  const [note, setNote] = useState('')
  const [createRepair, setCreateRepair] = useState(false)
  const [busy, setBusy] = useState(false)
  const canConfirm = a.status === 'pending'

  const submit = async () => {
    setBusy(true)
    try {
      const d = await api('POST', `/repair/appraisals/${a.id}/confirm`, {
        responsibility: resp, severity, confirm_note: note, create_repair: resp === 'none' && createRepair
      })
      toast(d.message); onDone()
    } catch (e) { toast(e.message, 'error') } finally { setBusy(false) }
  }

  return (
    <Modal title={`板刃鉴定 #${a.id} · ${a.rental_code} · ${a.guest_name}`} onClose={onClose} width={760}>
      <div className="grid grid-2" style={{ gap: 10 }}>
        <div className="alert info" style={{ margin: 0 }}>
          <b>📷 出库照片</b>
          {a.checkout_photos?.length > 0
            ? <div className="photos">{a.checkout_photos.map((p, i) => <img key={i} src={p} alt="出库" />)}</div>
            : <div className="small muted">无出库留档照片（无法证明出库前状态）</div>}
        </div>
        <div className="alert red" style={{ margin: 0 }}>
          <b>📸 归还照片</b>
          {a.return_photos?.length > 0
            ? <div className="photos">{a.return_photos.map((p, i) => <img key={i} src={p} alt="归还" />)}</div>
            : <div className="small muted">无照片 · 柜台初检：{a.return_note}</div>}
        </div>
        <div className="alert warn" style={{ margin: 0 }}>
          <b>🗺 课程区域 · 摔倒记录（{a.fall_snapshot?.length || 0} 条）</b>
          <div className="small">涉及区域：{a.course_areas || '无'}</div>
          {(a.fall_snapshot || []).map((f, i) => (
            <div key={i} className="small" style={{ marginTop: 2 }}>
              {f.happened_at} · 📍{f.course_area} · {f.source === 'coach' ? '教练登记' : '游客自报'}：{f.description}
            </div>
          ))}
        </div>
        <div className="alert" style={{ margin: 0 }}>
          <b>🗣 游客说明</b>
          <div className="small" style={{ marginTop: 4 }}>{a.guest_statement || '（游客尚未提交说明）'}</div>
          {a.course_name && <div className="small muted" style={{ marginTop: 6 }}>关联课程：{a.course_name}</div>}
        </div>
      </div>

      {canConfirm ? <>
        <h4 style={{ marginTop: 12 }}>维修间责任认定</h4>
        <div className="seg">
          <button className={resp === 'guest' ? 'on' : ''} onClick={() => setResp('guest')}>游客使用责任</button>
          <button className={resp === 'preexist' ? 'on' : ''} onClick={() => setResp('preexist')}>出库前已存在</button>
          <button className={resp === 'none' ? 'on' : ''} onClick={() => setResp('none')}>无责/正常磨损</button>
        </div>
        <div className="row">
          <Field label="缺口严重程度">
            <select value={severity} onChange={e => setSeverity(e.target.value)}>
              <option value="light">轻微（扣减×50%）</option>
              <option value="medium">中等（标准比例 35%）</option>
              <option value="heavy">严重（扣减×150%）</option>
            </select>
          </Field>
          {resp === 'none' && <Field label="处置">
            <label className="checkbox" style={{ alignSelf: 'flex-end' }}>
              <input type="checkbox" checked={createRepair} onChange={e => setCreateRepair(e.target.checked)} /> 仍需开维修工单（平台保养）
            </label>
          </Field>}
        </div>
        <Field label="认定说明（写入单据，游客与保险可见）"><textarea rows={2} value={note} onChange={e => setNote(e.target.value)} placeholder="缺口形态、与证据的对应关系" /></Field>
        <div className="alert info small">
          {resp === 'guest' && '将生成：押金扣减（按板刃矩阵）、维修工单；有效保单覆盖板刃时自动立案保险理赔。雪具转维修，暂停出租计时继续，修复后恢复。'}
          {resp === 'preexist' && '不扣游客押金、不走保险；生成维修工单由平台承担，雪具修复后上架。'}
          {resp === 'none' && '不扣押金、不立保险；默认核验通过直接上架并恢复出租，勾选上方可转维修保养。'}
        </div>
        <div className="modal-foot">
          <button className="btn ghost" onClick={onClose}>取消</button>
          <button className="btn warn" disabled={busy} onClick={submit}>确认责任并生成三单</button>
        </div>
      </> : (
        <div className="small" style={{ marginTop: 10 }}>
          认定：<Badge color={a.responsibility === 'guest' ? 'red' : 'green'}>{RESP_LABEL[a.responsibility]}</Badge>
          {a.deduction > 0 && <span> 扣减 {yuan(a.deduction)}</span>}
          {a.repair_order_id && <span> · 工单 #{a.repair_order_id}</span>}
          {a.claim_id && <span> · 理赔 #{a.claim_id}</span>}
          {a.confirm_note && <div className="muted">说明：{a.confirm_note}</div>}
        </div>
      )}
    </Modal>
  )
}
