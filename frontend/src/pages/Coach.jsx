import React, { useEffect, useState } from 'react'
import { api, TYPE_LABEL } from '../api.js'
import { Badge, Modal, Field, Loading, useToast } from '../components/ui.jsx'

export default function CoachPage() {
  const [courses, setCourses] = useState(null)
  const [fallTarget, setFallTarget] = useState(null)
  const toast = useToast()
  const load = async () => setCourses(await api('GET', '/coach/courses'))
  useEffect(() => { load() }, [])

  const check = async (rentalItemId, fitStatus, note = '') => {
    try {
      const d = await api('POST', '/coach/fit-check', { rental_item_id: rentalItemId, fit_status: fitStatus, note })
      toast(d.note ? `${d.message} ${d.note}` : d.message)
      load()
    } catch (e) { toast(e.message, 'error') }
  }

  if (!courses) return <Loading />
  return (
    <div>
      <h2 style={{ fontSize: 18 }}>我的课程 · 课前雪具合适合规确认</h2>
      <p className="muted small">上课前逐件确认雪鞋是否合脚、雪板长度是否适合初学者。标记「不合规」会向游客推送安全提醒并入换码统计。</p>
      {courses.map(co => (
        <div className="card" key={co.id}>
          <div className="btn-row">
            <h3 style={{ margin: 0 }}>{co.name}</h3>
            <span className="spacer" style={{ flex: 1 }} />
            <Badge color={co.session_type === 'night' ? 'gray' : 'blue'}>{co.session_type === 'night' ? '🌙 夜场' : '☀️ 日场'}</Badge>
            <Badge color="cyan">{co.level === 'beginner' ? '初学者' : co.level === 'intermediate' ? '进阶' : '高级'}</Badge>
            <span className="small muted">{co.start_at} ~ {co.end_at}</span>
          </div>
          <div className="small muted" style={{ marginBottom: 6 }}>
            🚩 课程滑行区域：{(co.zones || []).join('、') || '未划定'}（摔倒记录将按区域进入板刃损伤鉴定证据包）
          </div>
          <div className="table-wrap">
            <table>
              <thead><tr><th>游客</th><th>类型</th><th>建议</th><th>实际</th><th>脚码/身高</th><th>确认状态</th><th>操作</th></tr></thead>
              <tbody>
                {(co.rentals || []).flatMap(rr => rr.items.map(i => (
                  <tr key={i.ri_id}>
                    <td className="nowrap">{rr.guest_name}<div className="small muted mono">{rr.code}</div></td>
                    <td>{TYPE_LABEL[i.type]}</td>
                    <td><b>{i.recommended}</b></td>
                    <td>{i.actual}</td>
                    <td className="small">{i.type === 'boot' ? `脚 ${i.guest_foot_eu ? Number(i.guest_foot_eu).toString() : '—'}` : i.type === 'ski' ? `身高 ${i.guest_height_cm || '—'}` : '—'}</td>
                    <td>{i.fit_status === 'ok' ? <Badge color="green">✓ 合适合规</Badge>
                      : i.fit_status === 'issue' ? <Badge color="red">✗ 不合规</Badge>
                      : <Badge color="gray">待确认</Badge>}
                      {i.fit_note && <div className="small muted">{i.fit_note}</div>}</td>
                    <td>
                      <div className="btn-row">
                        <button className="btn sm" onClick={() => check(i.ri_id, 'ok')}>✓ 合适</button>
                        <button className="btn sm danger" onClick={() => check(i.ri_id, 'issue',
                          i.type === 'boot' ? '雪鞋不合脚，建议换半码' : '雪板长度不适合当前水平，建议换板')}>
                          ✗ 不合规
                        </button>
                        <button className="btn sm ghost" onClick={() => setFallTarget({ rental_id: rr.rental_id, zones: co.zones || [] })}>摔倒登记</button>
                      </div>
                    </td>
                  </tr>
                )))}
                {(!co.rentals || co.rentals.length === 0) && (
                  <tr><td colSpan={7} className="empty">暂无已出库待确认的学员</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      ))}
      {courses.length === 0 && <div className="empty">近期没有安排课程</div>}
      {fallTarget && <FallRecordModal target={fallTarget} onClose={() => setFallTarget(null)} />}
    </div>
  )
}

// 摔倒/碰撞登记：板刃损伤鉴定时作为课程区域客观依据
function FallRecordModal({ target, onClose }) {
  const toast = useToast()
  const [zone, setZone] = useState(target.zones[0] || '')
  const [customZone, setCustomZone] = useState('')
  const [severity, setSeverity] = useState('light')
  const [desc, setDesc] = useState('')
  const finalZone = zone === '__custom' ? customZone : zone
  const submit = async () => {
    try {
      const d = await api('POST', '/coach/fall-records', {
        rental_id: target.rental_id, zone: finalZone, severity, description: desc,
      })
      toast(d.message); onClose()
    } catch (e) { toast(e.message, 'error') }
  }
  return (
    <Modal title="摔倒 / 碰撞登记" onClose={onClose}>
      <p className="small muted">记录将关联课程区域，归还时如发现板刃缺口，会自动进入损伤鉴定的证据包供对比。</p>
      <Field label="发生区域（课程划定）">
        <select value={zone} onChange={e => setZone(e.target.value)}>
          {target.zones.map(z => <option key={z} value={z}>{z}</option>)}
          <option value="__custom">其他区域（手填）</option>
        </select>
      </Field>
      {zone === '__custom' && <Field label="区域名称"><input value={customZone} onChange={e => setCustomZone(e.target.value)} placeholder="如：索道下站平台" /></Field>}
      <Field label="程度">
        <select value={severity} onChange={e => setSeverity(e.target.value)}>
          <option value="light">轻微（未碰撞硬物）</option>
          <option value="medium">中等（撞到护栏/雪道边缘）</option>
          <option value="heavy">较重（高速碰撞/失控冲出雪道）</option>
        </select>
      </Field>
      <Field label="经过描述"><textarea rows={3} value={desc} onChange={e => setDesc(e.target.value)} placeholder="摔倒动作、碰撞物、雪具是否磕碰等" /></Field>
      <div className="modal-foot">
        <button className="btn ghost" onClick={onClose}>取消</button>
        <button className="btn" onClick={submit}>登记</button>
      </div>
    </Modal>
  )
}
