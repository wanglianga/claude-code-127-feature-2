import React, { useEffect, useState, useCallback } from 'react'
import { api, yuan, TYPE_LABEL, DAMAGE_LABEL, EDGE_STATUS, RESPONSIBILITY, uploadPhoto } from '../api.js'
import { Badge, Modal, Field, Loading, Empty, useToast } from '../components/ui.jsx'

export default function RepairPage({ page }) {
  if (page === 'maintenance') return <Maintenance />
  if (page === 'edge') return <EdgeAssessments />
  return <Orders />
}

// ---------- 板刃损伤鉴定工作台 ----------

const EDGE_TABS = [['open', '待判定'], ['appealed', '申诉中'], ['confirmed', '已判责'], ['closed', '已闭环'], ['', '全部']]

function EdgeAssessments() {
  const [tab, setTab] = useState('open')
  const [list, setList] = useState(null)
  const [detail, setDetail] = useState(null)
  const toast = useToast()
  const load = useCallback(async () => {
    setList(await api('GET', '/repair/edge-assessments' + (tab ? '?status=' + tab : '')))
  }, [tab])
  useEffect(() => { load() }, [tab])

  return (
    <div>
      <div className="btn-row">
        <h2 style={{ margin: 0, fontSize: 18 }}>板刃损伤鉴定工作台</h2>
        <span className="spacer" style={{ flex: 1 }} />
        <NewEdgeButton onCreated={load} />
        <div className="seg">{EDGE_TABS.map(([k, v]) =>
          <button key={k} className={tab === k ? 'on' : ''} onClick={() => setTab(k)}>{v}</button>)}</div>
      </div>
      <p className="muted small">维修间发现板刃缺口后建立鉴定：平台自动汇总<strong>出库照片、课程区域、摔倒记录、游客说明</strong>四项证据；
        确认责任后押金扣减、维修工单、保险理赔分别生成，雪具同步暂停出租（影响库存推荐），修复上架后恢复。</p>
      {!list ? <Loading /> : list.length === 0 ? <Empty text="暂无该状态的鉴定单" /> : (
        <div className="grid grid-2">
          {list.map(a => <EdgeCard key={a.id} a={a} onOpen={setDetail} />)}
        </div>
      )}
      {detail && <EdgeModal a={detail} onClose={() => { setDetail(null); load() }} />}
    </div>
  )
}

function EdgeCard({ a, onOpen }) {
  const color = { open: 'orange', confirmed: 'red', appealed: 'purple', upheld: 'gray', overturned: 'green', closed: 'green' }[a.status]
  return (
    <div className="card clickable" onClick={() => onOpen(a)}>
      <div className="btn-row">
        <h3 style={{ margin: 0 }}>{a.code}</h3>
        <span className="spacer" style={{ flex: 1 }} />
        <Badge color={color}>{EDGE_STATUS[a.status] || a.status}</Badge>
      </div>
      <div style={{ margin: '6px 0' }}>{a.guest_name} · <span className="mono small">{a.rental_code}</span></div>
      <div className="small">{TYPE_LABEL[a.equipment_type]} · {a.brand} {a.name} {a.size_label} · <span className="mono">{a.qr_code}</span></div>
      <div className="small muted" style={{ marginTop: 6 }}>
        {a.stage === 'during_rental' ? '使用中发现' : '归还时发现'} · {a.created_at}
        {a.suspended_at && !a.resumed_at ? ` · ⏸ 已暂停出租 ${a.suspended_at} 起` : a.resumed_at ? ` · ▶ ${a.resumed_at} 恢复出租` : ''}
      </div>
      {a.responsibility !== 'pending' && <div className="small" style={{ marginTop: 6 }}>
        <Badge color={a.responsibility === 'guest_liable' ? 'red' : 'green'}>{RESPONSIBILITY[a.responsibility]}</Badge>
        {a.deduction > 0 && <> 扣减 <b className="danger-text">{yuan(a.deduction)}</b></>}
      </div>}
      {a.appeals?.length > 0 && <div className="alert warn small" style={{ marginTop: 6 }}>
        游客申诉 {a.appeals.length} 次（最新：{a.appeals[0].status === 'submitted' ? '待柜台复核' : a.appeals[0].status}）
      </div>}
    </div>
  )
}

function PhotoRow({ title, photos }) {
  if (!photos?.length) return <div className="small muted">{title}：无</div>
  return (
    <div>
      <div className="small muted">{title}（{photos.length}）</div>
      <div className="photos">{photos.map((p, i) => <img key={i} src={p} alt="" />)}</div>
    </div>
  )
}

function EdgeModal({ a, onClose }) {
  const toast = useToast()
  const [resp, setResp] = useState('guest_liable')
  const [severity, setSeverity] = useState(a.severity || 'medium')
  const [finding, setFinding] = useState(a.staff_finding || '')
  const [conclusion, setConclusion] = useState('')
  const [busy, setBusy] = useState(false)

  const ruleRate = { light: 0.175, medium: 0.35, heavy: 0.525 }[severity]
  const suggestDeduct = Math.round((a.deposit || 1500) * ruleRate * 100) / 100

  const confirm = async () => {
    setBus(true)
    try {
      const d = await api('POST', `/repair/edge-assessments/${a.id}/confirm`, {
        responsibility: resp, severity, staff_finding: finding, conclusion,
      })
      toast(d.message)
      onClose()
    } catch (e) { toast(e.message, 'error') } finally { setBus(false) }
  }

  return (
    <Modal title={`板刃损伤鉴定 ${a.code}`} onClose={onClose} width={820}>
      <div className="btn-row" style={{ marginBottom: 10 }}>
        <Badge color="blue">{a.rental_code} · {a.guest_name}</Badge>
        <Badge color={a.session_type === 'night' ? 'gray' : 'cyan'}>{a.session_type === 'night' ? '🌙 夜场' : '☀️ 日场'}</Badge>
        <Badge color="orange">{EDGE_STATUS[a.status] || a.status}</Badge>
        <span className="spacer" style={{ flex: 1 }} />
        <span className="small muted">{a.brand} {a.name} {a.size_label} · {a.qr_code}</span>
      </div>

      <div className="grid grid-2">
        <PhotoRow title="① 出库留档照片（对比基准）" photos={a.checkout_photos} />
        <PhotoRow title="② 归还/缺口照片" photos={a.return_photos} />
      </div>

      <div className="card" style={{ background: '#f8fafc', marginTop: 10 }}>
        <h4 style={{ margin: '0 0 6px' }}>③ 课程区域 & ④ 摔倒记录</h4>
        <div className="small">课程滑行区域：<b>{a.course_zones || '自由滑（未关联课程）'}</b></div>
        <div className="small" style={{ marginTop: 4 }}>摔倒/碰撞记录：{a.fall_summary}</div>
        <div className="small" style={{ marginTop: 6 }}><b>游客说明：</b>{a.guest_statement || '（游客未填写）'}</div>
        <div className="small" style={{ marginTop: 4 }}><b>维修间目检：</b>{a.staff_finding || '（待填写）'}</div>
      </div>

      {a.status !== 'open' && <div className="alert info small" style={{ marginTop: 10 }}>
        <b>鉴定结论：</b>{RESPONSIBILITY[a.responsibility]}
        {a.deduction > 0 && <> · 扣减 <b>{yuan(a.deduction)}</b></>}
        {a.repair_order_id > 0 && <> · 维修工单 #{a.repair_order_id}（{a.repair_status}）</>}
        {a.claim_id > 0 && <> · 保险理赔 #{a.claim_id}</>}
        <div>{a.conclusion}</div>
        {a.appeals?.length > 0 && a.appeals.map(ap => <div key={ap.id} className="alert warn" style={{ marginTop: 6 }}>
          申诉（{ap.created_at}）：{ap.reason} — 柜台意见：{ap.counter_note || '待处理'}
        </div>)}
      </div>}

      {a.status === 'open' && <>
        <div className="row" style={{ marginTop: 12 }}>
          <Field label="目检发现（缺口位置/长度/形态）">
            <input value={finding} onChange={e => setFinding(e.target.value)} placeholder="如：左板刃中段 3cm 缺口，卷边" />
          </Field>
        </div>
        <div className="row">
          <Field label="严重程度（板刃基准 35%）">
            <select value={severity} onChange={e => setSeverity(e.target.value)}>
              <option value="light">轻微 ×50%（试算 {yuan(suggestDeduct)}）</option>
              <option value="medium">中等 标准（试算 {yuan(suggestDeduct)}）</option>
              <option value="heavy">严重 ×150%（试算 {yuan(suggestDeduct)}）</option>
            </select>
          </Field>
          <Field label="责任判定">
            <select value={resp} onChange={e => setResp(e.target.value)}>
              <option value="guest_liable">游客担责（扣押金 + 维修工单 + 保险立案）</option>
              <option value="not_liable">游客无责（不扣押金，平台承担维修）</option>
            </select>
          </Field>
        </div>
        <Field label="结论说明（可留空，系统按证据自动生成）"><textarea rows={2} value={conclusion} onChange={e => setConclusion(e.target.value)} /></Field>
        <div className="alert warn small">确认后：① 生成维修工单 ② 游客担责时写入押金扣减并自动发起保险理赔 ③ 雪具已暂停出租，修复上架后恢复。</div>
        <div className="modal-foot">
          <button className="btn ghost" onClick={onClose}>取消</button>
          <button className={resp === 'guest_liable' ? 'btn danger' : 'btn'} disabled={busy} onClick={confirm}>
            确认{resp === 'guest_liable' ? '游客担责' : '游客无责'}并生成单据
          </button>
        </div>
      </>}
    </Modal>
  )
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
            {(o.suspended_at || o.suspended_hours > 0) && <div className="small" style={{ margin: '4px 0' }}>
              <Badge color="orange">⏸ {o.resumed_at ? `曾暂停出租，${o.resumed_at} 恢复` : `暂停出租中 · ${(o.suspended_hours || 0).toFixed(1)} 小时`}</Badge>
              <span className="muted"> {o.suspended_at} 起{o.resumed_at ? ` → ${o.resumed_at}` : ''}</span>
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

// 维修间扫码/按明细发起板刃鉴定
function NewEdgeButton({ onCreated }) {
  const [open, setOpen] = useState(false)
  const [ri, setRI] = useState('')
  const [qr, setQR] = useState('')
  const [stage, setStage] = useState('during_rental')
  const [finding, setFinding] = useState('')
  const [statement, setStatement] = useState('')
  const [photos, setPhotos] = useState([])
  const toast = useToast()
  const onFile = async (f) => {
    try { const url = await uploadPhoto(f); setPhotos(p => [...p, url]); toast('照片已上传') }
    catch (e) { toast(e.message, 'error') }
  }
  const submit = async () => {
    try {
      const d = await api('POST', '/repair/edge-assessments', {
        rental_item_id: ri ? Number(ri) : undefined,
        qr_code: qr.trim() || undefined,
        stage, staff_finding: finding, guest_statement: statement,
        return_photos: photos,
      })
      toast(d.message)
      setOpen(false); setRI(''); setQR(''); setFinding(''); setStatement(''); setPhotos([])
      onCreated()
    } catch (e) { toast(e.message, 'error') }
  }
  return <>
    <button className="btn" onClick={() => setOpen(true)}>🔍 发现板刃缺口·发起鉴定</button>
    {open && <Modal title="新建板刃损伤鉴定" onClose={() => setOpen(false)} width={640}>
      <p className="small muted">提交后雪具立即暂停出租并记录暂停时间；系统自动汇总出库照片、课程区域、摔倒记录，稍后在列表中打开做证据对比与责任判定。</p>
      <div className="row">
        <Field label="雪板租赁明细 ID（与二维码二选一）"><input value={ri} onChange={e => setRI(e.target.value)} placeholder="如 6" /></Field>
        <Field label="或扫描雪板二维码"><input value={qr} onChange={e => setQR(e.target.value)} placeholder="QR-SKI160-..." /></Field>
      </div>
      <div className="row">
        <Field label="发现环节">
          <select value={stage} onChange={e => setStage(e.target.value)}>
            <option value="during_rental">使用中（自动换发，课程继续）</option>
            <option value="at_return">归还验具时</option>
          </select>
        </Field>
      </div>
      <Field label="目检发现"><textarea rows={2} value={finding} onChange={e => setFinding(e.target.value)} placeholder="缺口位置/长度/形态" /></Field>
      <Field label="游客说明（可代填）"><textarea rows={2} value={statement} onChange={e => setStatement(e.target.value)} placeholder="游客自述摔倒经过等" /></Field>
      <Field label="缺口照片（可多张）"><input type="file" accept="image/*" onChange={e => e.target.files[0] && onFile(e.target.files[0])} /></Field>
      {photos.length > 0 && <div className="photos">{photos.map((p, i) => <img key={i} src={p} alt="" />)}</div>}
      <div className="modal-foot"><button className="btn ghost" onClick={() => setOpen(false)}>取消</button>
        <button className="btn warn" onClick={submit}>暂停出租并建立鉴定</button></div>
    </Modal>}
  </>
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
