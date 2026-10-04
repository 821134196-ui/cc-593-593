// 统一 API 访问：自动携带当前员工身份
export function currentStaff() {
  try {
    return JSON.parse(localStorage.getItem('staff') || 'null')
  } catch {
    return null
  }
}

export function setCurrentStaff(s) {
  localStorage.setItem('staff', JSON.stringify(s))
}

async function request(path, { method = 'GET', body, formData } = {}) {
  const headers = {}
  const staff = currentStaff()
  if (staff) headers['X-Staff-Id'] = String(staff.id)
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const resp = await fetch(path, {
    method,
    headers,
    body: formData ?? (body !== undefined ? JSON.stringify(body) : undefined)
  })
  const data = await resp.json().catch(() => ({}))
  if (!resp.ok) {
    const err = new Error(data.message || `请求失败 (${resp.status})`)
    err.code = data.error
    err.status = resp.status
    throw err
  }
  return data
}

export const api = {
  listStaff: () => request('/api/staff'),
  searchItems: (params) => {
    const qs = new URLSearchParams(Object.entries(params).filter(([, v]) => v)).toString()
    return request('/api/items' + (qs ? '?' + qs : ''))
  },
  getItem: (id) => request(`/api/items/${id}`),
  createItem: (formData) => request('/api/items', { method: 'POST', formData }),
  moveItem: (id, storageLoc) => request(`/api/items/${id}/move`, { method: 'POST', body: { storage_loc: storageLoc } }),
  resumeItem: (id) => request(`/api/items/${id}/resume`, { method: 'POST', body: {} }),
  createClaim: (itemId, payload) => request(`/api/items/${itemId}/claims`, { method: 'POST', body: payload }),
  verifyClaim: (id, approve, rejectReason) =>
    request(`/api/claims/${id}/verify`, { method: 'POST', body: { approve, reject_reason: rejectReason } }),
  createAuthorization: (claimId, payload) =>
    request(`/api/claims/${claimId}/authorization`, { method: 'POST', body: payload }),
  revokeAuthorization: (claimId) =>
    request(`/api/claims/${claimId}/authorization/revoke`, { method: 'POST', body: {} }),
  checkout: (itemId, claimId, sealIntact) =>
    request(`/api/items/${itemId}/checkout`, { method: 'POST', body: { claim_id: claimId, seal_intact: sealIntact } }),
  ship: (itemId, claimId, sealIntact, address, trackingNo) =>
    request(`/api/items/${itemId}/ship`, {
      method: 'POST',
      body: { claim_id: claimId, seal_intact: sealIntact, address, tracking_no: trackingNo }
    })
}

export const CATEGORY_LABELS = {
  general: '普通物品',
  electronics: '电子产品',
  document: '证件',
  cash: '现金',
  valuable: '贵重物品'
}

export const STATUS_LABELS = {
  stored: '在库待认领',
  hold: '暂停交付',
  delivered: '已交付',
  shipped: '已寄出'
}

export const CLAIM_STATUS_LABELS = {
  pending: '待核验',
  verified: '已核验',
  rejected: '已拒绝',
  completed: '已完成'
}

export const PICKUP_LABELS = { self: '失主自取', proxy: '授权代领', ship: '快递寄送' }

export const ACTION_LABELS = {
  register: '登记入库',
  move: '变更位置',
  hold: '暂停交付',
  resume: '恢复交付',
  checkout: '出库交付',
  ship: '快递寄出'
}
