import React, { useEffect, useState } from 'react'
import { api, yuan, TYPE_LABEL, RENTAL_STATUS, ITEM_STATUS, DAMAGE_TYPES, APPEAL_STATUS, RESP_LABEL, APPRAISAL_STATUS, COURSE_AREAS, uploadPhoto } from '../api.js'
import { Badge, Modal, Field, useToast, Empty, Loading } from './ui.jsx'

function statusBadge(s) {
  const [label, color] = RENTAL_STATUS[s] || [s, 'gray']
  return <Badge color={color}>{label}</Badge>
}

// 租赁单详情：游客与各角色共用，guest 可执行本人操作
export default function RentalDetail({ id, onClose, onChanged, asGuest, canAssessDamage }) {
  const [r, setR] = useState(null)
  const toast = useToast()

  const load = async () => {
    try { setR(await api('GET', '/rentals/' + id)) } catch (e) { toast(e.message, 'error') }
  }
  useEffect(() => { load() }, [id])

  const act = async (method, path, body, okMsg) => {
    try {
      const d = await api(method, path, body)
      toast(d.message || okMsg || '操作成功')
      await load()
      onChanged?.()
    } catch (e) { toast(e.message, 'error') }
  }

  if (!r) return <Modal title="租赁单详情" onClose={onClose}><Loading /></Modal>

  return (
    <Modal title={`租赁单 ${r.code}`} onClose={onClose} width={760}>
      <div className="btn-row" style={{ marginBottom: 12 }}>
        {statusBadge(r.status)}
        <span className="muted small">{r.session_type === 'night' ? '🌙 夜场' : '☀️ 日场'}</span>
        {r.course_name && <span className="muted small">课程：{r.course_name} {r.course_start || ''}</span>}
        {r.insurance_name && <Badge color="purple">保险：{r.insurance_name}</Badge>}
        {r.night_return && <Badge color="cyan">夜场独立归还队列</Badge>}
      </div>

      <div className="grid grid-4" style={{ marginBottom: 14 }}>
        <div className="stat"><div className="k">冻结押金</div><div className="v">{yuan(r.deposit_total)}</div></div>
        <div className="stat"><div className="k">租金合计</div><div className="v">{yuan(r.fee_total)}</div></div>
        <div className="stat"><div className="k">保险费</div><div className="v">{yuan(r.premium_total)}</div></div>
        <div className="stat"><div className="k">延时</div><div className="v">{Number(r.extra_hours || 0)}h</div></div>
      </div>

      {r.safety_reminders?.length > 0 && (
        <div className="alert warn">
          <b>⚠️ 安全提醒：</b>
          {r.safety_reminders.map((s, i) => <div key={i} className="small">{s.message}（{s.created_at}）</div>)}
        </div>
      )}

      <h4>雪具清单与建议依据</h4>
      <div className="table-wrap">
        <table>
          <thead><tr><th>类型</th><th>建议尺码</th><th>实际分配</th><th>费用/押金</th><th>状态</th><th>课前确认</th></tr></thead>
          <tbody>
            {r.items.map(i => (
              <tr key={i.id}>
                <td>{TYPE_LABEL[i.equipment_type]}</td>
                <td><b>{i.recommended_size}</b>
                  <div className="small muted" style={{ maxWidth: 260 }}>{i.reason}</div></td>
                <td>
                  {i.model_label || '—'}
                  {i.qr_code && <div className="small mono muted">{i.qr_code}</div>}
                  {i.checkout_photos?.length > 0 && (
                    <div className="photos" style={{ marginTop: 4 }}>
                      {i.checkout_photos.map((p, k) => <img key={k} src={p} alt="出库照片" title="出库时留档照片" />)}
                    </div>
                  )}
                </td>
                <td className="small">{yuan(i.fee)} / {yuan(i.deposit)}</td>
                <td><Badge color={riColor(i.status)}>{ITEM_STATUS[i.status] || i.status}</Badge></td>
                <td>{fitBadge(i.fit_status)}{i.fit_note && <div className="small muted">{i.fit_note}</div>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* 游客操作区 */}
      {asGuest && (
        <div className="card" style={{ background: '#f8fafc', marginTop: 14 }}>
          <h4>我的操作</h4>
          <div className="btn-row">
            {r.status === 'draft' && <button className="btn" onClick={() => act('POST', `/rentals/${r.id}/confirm`, {})}>✅ 确认预约取具时段</button>}
            {(r.status === 'equipped') && (
              <>
                <ExchangeBtn rental={r} onDone={load} />
                <ExtendBtn rental={r} onDone={load} />
                <DamageBtn rental={r} onDone={load} />
                <FallBtn rental={r} onDone={load} />
                <button className="btn ghost" onClick={() => act('POST', `/rentals/${r.id}/queue-return`, { night: r.session_type === 'night' })}>
                  {r.session_type === 'night' ? '🌙 排队夜场归还' : '排队日场归还'}
                </button>
              </>
            )}
            {r.status === 'return_pending' && <Badge color="orange">已在归还队列，请等待柜台叫号验具</Badge>}
            {r.status === 'returned' && !r.feedback && <FeedbackBtn rental={r} onDone={load} />}
            {r.status === 'returned' && r.feedback && <Badge color="green">已评价 {r.feedback.rating} 星</Badge>}
          </div>
        </div>
      )}

      {/* 摔倒记录（课程区域证据） */}
      {r.fall_records?.length > 0 && (
        <>
          <h4>摔倒记录（课程区域 · 鉴定证据）</h4>
          <ul className="timeline">
            {r.fall_records.map(f => (
              <li key={f.id}>
                <b>📍 {f.course_area} <Badge color={f.source === 'coach' ? 'blue' : 'cyan'}>{f.source === 'coach' ? '教练登记' : '游客自报'}</Badge></b>
                <div className="small muted">{f.happened_at} · {f.description}</div>
              </li>
            ))}
          </ul>
        </>
      )}

      {/* 板刃损伤鉴定 */}
      {r.appraisals?.length > 0 && r.appraisals.map(a => (
        <AppraisalBlock key={a.id} appraisal={a} asGuest={asGuest} onChanged={load} />
      ))}

      {/* 损坏与赔付依据 */}
      {r.damages?.length > 0 && (
        <>
          <h4>损坏报告与赔付依据</h4>
          {r.damages.map(d => (
            <div key={d.id} className="alert red">
              <div className="btn-row" style={{ justifyContent: 'space-between' }}>
                <b>{d.type_label}（{d.severity === 'light' ? '轻微' : d.severity === 'heavy' ? '严重' : d.severity === 'lost' ? '丢失' : '中等'}）</b>
                <span className="inline">
                  {d.status === 'reported' && canAssessDamage && <AssessButton damage={d} onDone={load} />}
                  <Badge color="orange">{d.status}</Badge>
                </span>
              </div>
              <div className="small" style={{ margin: '4px 0' }}>{d.description}</div>
              <div className="small">建议扣减押金：<b className="danger-text">{yuan(d.deduction)}</b>
                {d.claim_id ? ` · 已发起保险理赔 #${d.claim_id}` : ' · 无保险或不在保障范围'}</div>
              {d.photos?.length > 0 && <div className="photos" style={{ marginTop: 6 }}>
                {d.photos.map((p, i) => <img key={i} src={p} alt="损坏照片" />)}
              </div>}
            </div>
          ))}
        </>
      )}

      {/* 保险理赔进度 */}
      {r.claims?.length > 0 && (
        <>
          <h4>保险理赔进度</h4>
          {r.claims.map(c => (
            <div key={c.id} className="alert info small">
              理赔 #{c.id}：申请 {yuan(c.amount_requested)}，免赔 {yuan(c.deductible)}，
              赔付 <b>{yuan(c.payout)}</b> — {c.note}
              <span style={{ marginLeft: 8 }}><Badge color={c.status === 'paid' ? 'green' : 'blue'}>{c.status}</Badge></span>
            </div>
          ))}
        </>
      )}

      {/* 押金流水 */}
      {r.deposit_logs?.length > 0 && (
        <>
          <h4>押金与赔付流水（依据可查）</h4>
          <ul className="timeline">
            {r.deposit_logs.map(t => (
              <li key={t.id} className={depositColor(t.type)}>
                <b>{depositLabel(t.type)} {yuan(t.amount)}</b>
                <div className="small muted">{t.note} · {String(t.created_at).slice(0, 16)}</div>
              </li>
            ))}
          </ul>
        </>
      )}

      {r.extensions?.length > 0 && (
        <p className="small muted">延时记录：{r.extensions.map((e, i) => `${e.hours}h/${yuan(e.fee)}`).join('，')}</p>
      )}
      {r.feedback && <p className="small muted">我的评价：{'⭐'.repeat(r.feedback.rating)} {r.feedback.text}</p>}
    </Modal>
  )
}

// 板刃鉴定区块：四方证据对比 + 责任结论 + 游客说明/申诉
function AppraisalBlock({ appraisal: a, asGuest, onChanged }) {
  const toast = useToast()
  const [, sc] = APPRAISAL_STATUS[a.status] || [a.status, 'gray']
  const [showStatement, setShowStatement] = useState(false)
  const [showAppeal, setShowAppeal] = useState(false)
  const [text, setText] = useState(a.guest_statement || '')

  const submitStatement = async () => {
    try {
      const d = await api('POST', `/appraisals/${a.id}/statement`, { statement: text })
      toast(d.message); setShowStatement(false); onChanged()
    } catch (e) { toast(e.message, 'error') }
  }
  const submitAppeal = async () => {
    try {
      const d = await api('POST', `/appraisals/${a.id}/appeal`, { reason: text })
      toast(d.message); setShowAppeal(false); onChanged()
    } catch (e) { toast(e.message, 'error') }
  }

  return (
    <div className="card" style={{ borderColor: '#fdba74', marginTop: 14 }}>
      <div className="btn-row">
        <h4 style={{ margin: 0 }}>🔍 板刃损伤鉴定 #{a.id} · {a.model_label} <span className="mono small">{a.qr_code}</span></h4>
        <span className="spacer" style={{ flex: 1 }} />
        <Badge color={sc}>{(APPRAISAL_STATUS[a.status] || [a.status])[0]}</Badge>
        {a.responsibility !== 'pending' && <Badge color={a.responsibility === 'guest' ? 'red' : 'green'}>{RESP_LABEL[a.responsibility]}</Badge>}
      </div>
      <div className="small muted" style={{ margin: '4px 0' }}>
        发起于 {a.created_at}{a.confirmed_at ? ` · 维修间确认于 ${a.confirmed_at}` : ''} ·
        暂停出租 {a.suspended_at || '—'}{a.resumed_at ? ` → 恢复 ${a.resumed_at}` : '（进行中）'}
      </div>

      <div className="grid grid-2" style={{ gap: 10 }}>
        <div className="alert info" style={{ margin: 0 }}>
          <b>📷 出库照片（对比基准）</b>
          {a.checkout_photos?.length > 0
            ? <div className="photos">{a.checkout_photos.map((p, i) => <img key={i} src={p} alt="出库照片" />)}</div>
            : <div className="small muted" style={{ marginTop: 4 }}>无出库留档照片</div>}
        </div>
        <div className="alert red" style={{ margin: 0 }}>
          <b>📸 归还照片（发现缺口）</b>
          {a.return_photos?.length > 0
            ? <div className="photos">{a.return_photos.map((p, i) => <img key={i} src={p} alt="归还照片" />)}</div>
            : <div className="small muted" style={{ marginTop: 4 }}>无照片 · 柜台初检：{a.return_note}</div>}
        </div>
        <div className="alert warn" style={{ margin: 0 }}>
          <b>🗺 课程区域 · 摔倒记录</b>
          {a.course_areas ? <div className="small">涉及区域：{a.course_areas}</div> : <div className="small muted">无摔倒区域记录</div>}
          {a.fall_snapshot?.length > 0 && a.fall_snapshot.map((f, i) => (
            <div key={i} className="small" style={{ marginTop: 2 }}>
              {f.happened_at} · {f.course_area} · {f.source === 'coach' ? '教练登记' : '游客自报'}：{f.description}
            </div>
          ))}
        </div>
        <div className="alert" style={{ margin: 0 }}>
          <b>🗣 游客说明</b>
          <div className="small" style={{ marginTop: 4 }}>{a.guest_statement || '（游客尚未提交说明）'}</div>
        </div>
      </div>

      {a.status !== 'pending' && (
        <div className="small" style={{ marginTop: 8 }}>
          维修间认定：<b>{RESP_LABEL[a.responsibility]}</b>（{a.severity === 'light' ? '轻微' : a.severity === 'heavy' ? '严重' : '中等'}） ·
          扣减押金 <b className={a.deduction > 0 ? 'danger-text' : 'ok-text'}>{yuan(a.deduction)}</b>
          {a.repair_order_id ? ` · 维修工单 #${a.repair_order_id}` : ''}
          {a.claim_id ? ` · 保险理赔 #${a.claim_id}` : ''}
          {a.confirm_note && <div className="muted">认定说明：{a.confirm_note}</div>}
        </div>
      )}

      {a.appeal_status !== 'none' && (
        <div className="small" style={{ marginTop: 6 }}>
          申诉状态：<Badge color={a.appeal_status === 'upheld' ? 'green' : a.appeal_status === 'rejected' ? 'gray' : 'red'}>{APPEAL_STATUS[a.appeal_status]}</Badge>
          {a.appeal_reason && <span className="muted"> 理由：{a.appeal_reason}</span>}
          {a.handle_note && <div className="muted">柜台复核：{a.handle_note}</div>}
        </div>
      )}

      {asGuest && a.status === 'pending' && (
        <div className="modal-foot" style={{ paddingRight: 0 }}>
          <button className="btn sm" onClick={() => { setShowStatement(v => !v); setText(a.guest_statement || '') }}>
            {a.guest_statement ? '补充说明' : '提交情况说明'}
          </button>
          {showStatement && <div className="inline" style={{ flex: 1 }}>
            <input style={{ width: '70%' }} value={text} onChange={e => setText(e.target.value)} placeholder="滑行过程、摔倒位置、对缺口的说明" />
            <button className="btn sm" onClick={submitStatement}>提交给维修间</button>
          </div>}
        </div>
      )}
      {asGuest && a.status === 'confirmed' && a.responsibility === 'guest' && a.appeal_status === 'none' && (
        <div className="modal-foot" style={{ paddingRight: 0 }}>
          <button className="btn sm warn" onClick={() => { setShowAppeal(v => !v); setText('') }}>我要申诉（柜台将复核证据）</button>
          {showAppeal && <div className="inline" style={{ flex: 1 }}>
            <input style={{ width: '70%' }} value={text} onChange={e => setText(e.target.value)} placeholder="申诉理由，如出库前已存在/课程区域无关" />
            <button className="btn sm warn" onClick={submitAppeal}>提交申诉</button>
          </div>}
        </div>
      )}
    </div>
  )
}

function riColor(s) {
  return { out: 'green', returned_ok: 'gray', returned_damaged: 'red', repair: 'orange',
    lost: 'red', assigned: 'blue', exchanged: 'purple', edge_appraisal: 'orange' }[s] || 'gray'
}
function fitBadge(f) {
  if (f === 'ok') return <Badge color="green">✓ 合适合规</Badge>
  if (f === 'issue') return <Badge color="red">✗ 不合规，建议换码</Badge>
  return <span className="muted small">待课前确认</span>
}
function depositLabel(t) {
  return { hold: '冻结押金', deduct_damage: '损坏扣减', deduct_lost: '丢失扣减',
    deduct_late: '超时扣减', insurance_offset: '保险冲抵返还', refund: '押金退还',
    hold_appraisal: '鉴定暂扣押金', deduct_reversal: '申诉撤销返还' }[t] || t
}
function depositColor(t) {
  if (t === 'hold' || t === 'hold_appraisal') return ''
  if (t === 'refund' || t === 'insurance_offset' || t === 'deduct_reversal') return 'green'
  return 'red'
}

/* ---------- 操作弹窗 ---------- */

function ExchangeBtn({ rental, onDone }) {
  const [open, setOpen] = useState(false)
  const [ri, setRI] = useState(rental.items[0]?.id)
  const [size, setSize] = useState('')
  const [reason, setReason] = useState('挤脚/不合脚')
  const toast = useToast()
  const submit = async () => {
    try {
      await api('POST', `/rentals/${rental.id}/exchange-request`, {
        rental_item_id: Number(ri), desired_size: size, reason
      })
      toast('换码申请已提交，请留意换码队列叫号')
      setOpen(false); onDone()
    } catch (e) { toast(e.message, 'error') }
  }
  return <>
    <button className="btn warn" onClick={() => setOpen(true)}>🔄 申请换码</button>
    {open && <Modal title="中途换码申请" onClose={() => setOpen(false)}>
      <Field label="要调换的雪具">
        <select value={ri} onChange={e => setRI(e.target.value)}>
          {rental.items.filter(i => i.status === 'out').map(i =>
            <option key={i.id} value={i.id}>{TYPE_LABEL[i.equipment_type]}（当前 {i.model_label}，建议 {i.recommended_size}）</option>)}
        </select>
      </Field>
      <Field label="期望尺码（如 EU43、140cm、M）"><input value={size} onChange={e => setSize(e.target.value)} placeholder="例如 EU43" /></Field>
      <Field label="原因">
        <select value={reason} onChange={e => setReason(e.target.value)}>
          <option>挤脚/不合脚</option><option>雪板太长不好控制</option>
          <option>头盔松动</option><option>雪杖长度不合适</option><option>其他</option>
        </select>
      </Field>
      <div className="modal-foot"><button className="btn ghost" onClick={() => setOpen(false)}>取消</button>
        <button className="btn" onClick={submit}>提交申请并入换码队列</button></div>
    </Modal>}
  </>
}

function ExtendBtn({ rental, onDone }) {
  const [open, setOpen] = useState(false)
  const [hours, setHours] = useState(1)
  const toast = useToast()
  const submit = async () => {
    try {
      const d = await api('POST', `/rentals/${rental.id}/extend`, { hours: Number(hours) })
      toast(`${d.message}，加收 ${yuan(d.extra_fee)}`)
      setOpen(false); onDone()
    } catch (e) { toast(e.message, 'error') }
  }
  return <>
    <button className="btn ghost" onClick={() => setOpen(true)}>⏱ 课程延时</button>
    {open && <Modal title="课程延时登记" onClose={() => setOpen(false)}>
      <Field label="延时时长（小时，最多 6）">
        <input type="number" min="0.5" max="6" step="0.5" value={hours} onChange={e => setHours(e.target.value)} />
      </Field>
      <p className="small muted">延时费按时租折算，归还时一并结算。</p>
      <div className="modal-foot"><button className="btn ghost" onClick={() => setOpen(false)}>取消</button>
        <button className="btn" onClick={submit}>确认延时</button></div>
    </Modal>}
  </>
}

function DamageBtn({ rental, onDone }) {
  const [open, setOpen] = useState(false)
  const [ri, setRI] = useState(rental.items[0]?.id)
  const [type, setType] = useState('liner_wet')
  const [severity, setSeverity] = useState('medium')
  const [desc, setDesc] = useState('')
  const [photos, setPhotos] = useState([])
  const toast = useToast()
  const onFile = async (file) => {
    try {
      const url = await uploadPhoto(file)
      setPhotos(p => [...p, url])
      toast('照片已上传')
    } catch (e) { toast(e.message, 'error') }
  }
  const submit = async () => {
    try {
      const d = await api('POST', `/rentals/${rental.id}/damage`, {
        rental_item_id: Number(ri), damage_type: type, severity, description: desc, photos
      })
      toast(`${d.message}（排队号 ${d.ticket_no}）`)
      setOpen(false); onDone()
    } catch (e) { toast(e.message, 'error') }
  }
  return <>
    <button className="btn danger" onClick={() => setOpen(true)}>🛠 上报损坏/丢失</button>
    {open && <Modal title="使用中损坏上报" onClose={() => setOpen(false)}>
      <Field label="问题雪具">
        <select value={ri} onChange={e => setRI(e.target.value)}>
          {rental.items.filter(i => i.status === 'out').map(i =>
            <option key={i.id} value={i.id}>{TYPE_LABEL[i.equipment_type]} {i.model_label}</option>)}
        </select>
      </Field>
      <div className="row">
        <Field label="损坏类型">
          <select value={type} onChange={e => setType(e.target.value)}>
            {DAMAGE_TYPES.map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </select>
        </Field>
        <Field label="严重程度">
          <select value={severity} onChange={e => setSeverity(e.target.value)}>
            <option value="light">轻微</option><option value="medium">中等</option>
            <option value="heavy">严重</option><option value="lost">丢失</option>
          </select>
        </Field>
      </div>
      <Field label="情况描述"><textarea rows={3} value={desc} onChange={e => setDesc(e.target.value)} placeholder="发现时间、位置、现象…" /></Field>
      <Field label="损坏照片（可多张）">
        <input type="file" accept="image/*" onChange={e => e.target.files[0] && onFile(e.target.files[0])} />
      </Field>
      {photos.length > 0 && <div className="photos">{photos.map((p, i) => <img key={i} src={p} alt="" />)}</div>}
      <p className="small muted">提交后进入损坏处理队列，柜台评定扣减依据；有保险将自动立案，全程可在本单查看。</p>
      <div className="modal-foot"><button className="btn ghost" onClick={() => setOpen(false)}>取消</button>
        <button className="btn danger" onClick={submit}>提交上报</button></div>
    </Modal>}
  </>
}

// 游客自报摔倒（记录课程区域，作为日后板刃鉴定证据）
function FallBtn({ rental, onDone }) {
  const [open, setOpen] = useState(false)
  const [ri, setRI] = useState(rental.items.find(i => i.equipment_type === 'ski' && i.status === 'out')?.id || rental.items[0]?.id)
  const [area, setArea] = useState(COURSE_AREAS[0])
  const [desc, setDesc] = useState('')
  const toast = useToast()
  const submit = async () => {
    try {
      const d = await api('POST', `/rentals/${rental.id}/fall-record`, {
        rental_item_id: Number(ri) || undefined, course_area: area, description: desc
      })
      toast(d.message)
      setOpen(false); setDesc(''); onDone()
    } catch (e) { toast(e.message, 'error') }
  }
  return <>
    <button className="btn ghost" onClick={() => setOpen(true)}>🤕 上报摔倒</button>
    {open && <Modal title="摔倒记录（课程区域将留档）" onClose={() => setOpen(false)}>
      <Field label="关联雪具（可选）">
        <select value={ri || ''} onChange={e => setRI(e.target.value)}>
          <option value="">不指定</option>
          {rental.items.filter(i => i.status === 'out').map(i =>
            <option key={i.id} value={i.id}>{TYPE_LABEL[i.equipment_type]} {i.model_label}</option>)}
        </select>
      </Field>
      <Field label="摔倒课程区域">
        <select value={area} onChange={e => setArea(e.target.value)}>
          {COURSE_AREAS.map(a => <option key={a}>{a}</option>)}
        </select>
      </Field>
      <Field label="情况描述"><textarea rows={2} value={desc} onChange={e => setDesc(e.target.value)} placeholder="摔倒过程、是否磕碰雪板" /></Field>
      <div className="modal-foot"><button className="btn ghost" onClick={() => setOpen(false)}>取消</button>
        <button className="btn" onClick={submit}>提交记录</button></div>
    </Modal>}
  </>
}

function AssessButton({ damage, onDone }) {
  const [open, setOpen] = useState(false)
  const [severity, setSeverity] = useState('medium')
  const [desc, setDesc] = useState(damage.description || '')
  const toast = useToast()
  const submit = async () => {
    try {
      const d = await api('POST', `/counter/damages/${damage.id}/assess`, {
        severity, description: desc || damage.description
      })
      toast(`${d.message} 扣减依据：¥${Number(d.deduction_hint).toFixed(2)}`)
      setOpen(false); onDone()
    } catch (e) { toast(e.message, 'error') }
  }
  return <>
    <button className="btn sm warn" onClick={() => setOpen(true)}>柜台评定定级</button>
    {open && <Modal title={`损坏评定 #${damage.id}`} onClose={() => setOpen(false)}>
      <div className="alert red small">{damage.type_label} · 游客描述：{damage.description}</div>
      <Field label="柜台定级严重程度">
        <select value={severity} onChange={e => setSeverity(e.target.value)}>
          <option value="light">轻微</option><option value="medium">中等</option><option value="heavy">严重</option>
        </select>
      </Field>
      <Field label="补充说明 / 维修建议"><textarea rows={2} value={desc} onChange={e => setDesc(e.target.value)} /></Field>
      <p className="small muted">评定后系统按押金比例计算扣减、自动开维修工单；若有保险将自动立案，旧雪具收回并自动换发同型号。</p>
      <div className="modal-foot"><button className="btn ghost" onClick={() => setOpen(false)}>取消</button>
        <button className="btn warn" onClick={submit}>确认评定</button></div>
    </Modal>}
  </>
}

function FeedbackBtn({ rental, onDone }) {
  const [open, setOpen] = useState(false)
  const [rating, setRating] = useState(5)
  const [text, setText] = useState('')
  const toast = useToast()
  return <>
    <button className="btn" onClick={() => setOpen(true)}>⭐ 归还后评价</button>
    {open && <Modal title="归还反馈" onClose={() => setOpen(false)}>
      <Field label={`评分：${rating} 星`}>
        <input type="range" min="1" max="5" value={rating} onChange={e => setRating(e.target.value)} />
      </Field>
      <Field label="反馈（雪具适配、排队等待、服务）"><textarea rows={3} value={text} onChange={e => setText(e.target.value)} /></Field>
      <div className="modal-foot"><button className="btn ghost" onClick={() => setOpen(false)}>取消</button>
        <button className="btn" onClick={async () => {
          try { await api('POST', `/rentals/${rental.id}/feedback`, { rating: Number(rating), text })
            toast('感谢反馈'); setOpen(false); onDone() } catch (e) { toast(e.message, 'error') }
        }}>提交</button></div>
    </Modal>}
  </>
}

export { statusBadge }
