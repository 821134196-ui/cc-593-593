<template>
  <div v-if="data">
    <!-- 物品信息 -->
    <div class="panel">
      <h2>
        物品详情
        <span class="badge" :class="statusClass(data.item.status)" style="margin-left:8px">{{ statusLabel(data.item.status) }}</span>
        <span v-if="data.item.sensitivity !== 'normal'" class="badge sensitive" style="margin-left:6px">敏感物品</span>
      </h2>
      <div style="display:flex;gap:20px;flex-wrap:wrap">
        <img v-if="photoUrl" :src="photoUrl" style="width:160px;height:160px;object-fit:cover;border-radius:10px;border:1px solid var(--line)" />
        <dl class="kv" style="flex:1;min-width:280px">
          <dt>房号</dt><dd>{{ data.item.room_number }}</dd>
          <dt>发现时间</dt><dd>{{ fmt(data.item.found_at) }}</dd>
          <dt>登记保洁</dt><dd>{{ data.found_by_name }}</dd>
          <dt>封袋号</dt><dd>{{ data.item.bag_seal_number }}</dd>
          <dt>当前封签</dt><dd>{{ data.item.current_seal }}</dd>
          <dt>保管位置</dt><dd>{{ data.item.storage_location }}</dd>
          <dt>保管期限</dt><dd>{{ fmtDate(data.item.retention_until) }}</dd>
          <dt v-if="data.item.released_at">出库时间</dt><dd v-if="data.item.released_at">{{ fmt(data.item.released_at) }}</dd>
          <dt>物品描述</dt>
          <dd>
            <template v-if="isDesk">{{ data.item.description }}</template>
            <template v-else>
              {{ data.item.public_summary }}
              <div class="hint">完整详情仅前台在认领核实环节可见</div>
            </template>
          </dd>
        </dl>
      </div>
    </div>

    <!-- 暂停交付告警 -->
    <div class="panel" v-for="h in openHolds" :key="h.id" style="border-color:var(--warn)">
      <h3 style="color:var(--warn)">⚠ 交付已暂停（{{ h.hold_type === 'seal_mismatch' ? '封签不符' : '超过保管期' }}）</h3>
      <div class="muted">{{ h.reason }} ｜ {{ fmt(h.created_at) }}</div>
      <div v-if="isDesk" style="margin-top:10px;display:flex;gap:10px;align-items:center;flex-wrap:wrap">
        <input v-model="resolveNotes[h.id]" placeholder="核查说明" style="flex:2;min-width:200px" />
        <input v-model.number="extendDays[h.id]" type="number" placeholder="延长保管天数（超期时填写）" style="flex:1;min-width:160px" />
        <button class="ghost" @click="resolveHold(h.id)">核查无误，解除暂停</button>
      </div>
    </div>

    <div v-if="flash" class="msg" :class="flash.type">{{ flash.text }}</div>

    <!-- 交接时间线 -->
    <div class="panel">
      <h2>交接时间线（只追加台账，共 {{ data.handovers.length }} 条）</h2>
      <ul class="timeline">
        <li v-for="r in data.handovers" :key="r.id">
          <div class="t-time">#{{ r.seq }} ｜ {{ fmt(r.created_at) }} ｜ {{ actionLabel(r.action) }}</div>
          <div class="t-note">{{ r.note }}</div>
          <div class="t-who">
            <template v-if="r.from_name">经手：{{ r.from_name }}</template>
            <template v-if="r.to_name"> → {{ r.to_name }}</template>
            <template v-if="r.location"> ｜ 位置：{{ r.location }}</template>
          </div>
        </li>
      </ul>
    </div>

    <template v-if="isDesk">
      <!-- 认领人 -->
      <div class="panel">
        <h2>认领人与身份核实（一件物品可分别验证多位认领人）</h2>
        <table>
          <thead>
            <tr><th>认领人</th><th>联系方式/证件</th><th>自述描述</th><th>状态</th><th>核实</th></tr>
          </thead>
          <tbody>
            <tr v-for="c in data.claims" :key="c.id">
              <td>{{ c.claimant.full_name }}</td>
              <td style="font-size:13px">
                {{ c.claimant.phone }}<br />
                {{ c.claimant.id_type }}：{{ c.claimant.id_number }}
              </td>
              <td>{{ c.description }}<div v-if="c.reject_note" class="muted">驳回原因：{{ c.reject_note }}</div></td>
              <td><span class="badge" :class="c.status">{{ claimStatus(c.status) }}</span></td>
              <td>
                <template v-if="c.status === 'pending'">
                  <button class="small" @click="verify(c.id, true)">核实通过</button>
                  <button class="small danger" style="margin-left:6px" @click="verify(c.id, false)">不符驳回</button>
                </template>
                <span v-else class="muted">已处理</span>
              </td>
            </tr>
          </tbody>
        </table>

        <h3>登记新认领人</h3>
        <div class="row">
          <label class="field"><span>姓名</span><input v-model="newClaim.full_name" /></label>
          <label class="field"><span>手机号</span><input v-model="newClaim.phone" /></label>
        </div>
        <div class="row">
          <label class="field"><span>证件类型</span>
            <select v-model="newClaim.id_type">
              <option value="idcard">身份证</option><option value="passport">护照</option><option value="other">其他</option>
            </select>
          </label>
          <label class="field"><span>证件号</span><input v-model="newClaim.id_number" /></label>
        </div>
        <label class="field"><span>住客自述物品特征</span><textarea v-model="newClaim.description"></textarea></label>
        <button class="ghost" @click="fileClaim">登记认领</button>
      </div>

      <!-- 代领授权 -->
      <div class="panel">
        <h2>代领授权</h2>
        <table>
          <thead><tr><th>代领人</th><th>证件</th><th>状态</th><th>有效期至</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="a in data.authorizations" :key="a.id">
              <td>{{ a.proxy_name }}</td>
              <td>{{ a.proxy_id_type }} {{ a.proxy_id_number }}</td>
              <td><span class="badge" :class="a.status">{{ authStatus(a.status) }}</span></td>
              <td>{{ fmt(a.expires_at) }}</td>
              <td>
                <button v-if="a.status === 'active'" class="small danger" @click="revoke(a.id)">撤销授权</button>
                <span v-else class="muted">—</span>
              </td>
            </tr>
          </tbody>
        </table>
        <h3>为已核实失主建立授权</h3>
        <div class="row">
          <label class="field" style="flex:2"><span>失主（已核实的认领人）</span>
            <select v-model="authForm.owner_claim_id">
              <option value="" disabled>请选择</option>
              <option v-for="c in verifiedClaims" :key="c.id" :value="c.id">{{ c.claimant.full_name }}</option>
            </select>
          </label>
          <label class="field"><span>代领人姓名</span><input v-model="authForm.proxy_name" /></label>
        </div>
        <div class="row">
          <label class="field"><span>代领人证件类型</span>
            <select v-model="authForm.proxy_id_type"><option value="idcard">身份证</option><option value="passport">护照</option><option value="other">其他</option></select>
          </label>
          <label class="field"><span>代领人证件号</span><input v-model="authForm.proxy_id_number" /></label>
          <label class="field"><span>授权有效小时</span><input v-model.number="authForm.ttl_hours" type="number" /></label>
        </div>
        <button class="ghost" @click="createAuth">建立代领授权</button>
      </div>

      <!-- 出库交付 -->
      <div class="panel" v-if="data.item.status === 'in_storage'">
        <h2>办理出库交付</h2>
        <div v-if="openHolds.length" class="msg warn">存在未解除的暂停交付，出库将被拦截。请先核查封签/保管期并解除暂停。</div>
        <div class="row">
          <label class="field" style="flex:2"><span>交付给哪位认领人</span>
            <select v-model="release.claim_id">
              <option value="" disabled>请选择（须已核实通过）</option>
              <option v-for="c in data.claims" :key="c.id" :value="c.id">
                {{ c.claimant.full_name }}（{{ claimStatus(c.status) }}）
              </option>
            </select>
          </label>
          <label class="field"><span>交付方式</span>
            <select v-model="release.method">
              <option value="self_pickup">本人自取</option>
              <option value="proxy">代领</option>
              <option value="shipping">寄送</option>
            </select>
          </label>
        </div>
        <label class="field">
          <span>出库现场核验封签号（须与登记封袋号一致，否则自动暂停交付）</span>
          <input v-model="release.seal_presented" :placeholder="data.item.current_seal" />
        </label>
        <template v-if="release.method === 'proxy'">
          <div class="row">
            <label class="field" style="flex:2"><span>使用哪份有效授权</span>
              <select v-model="release.authorization_id">
                <option value="" disabled>请选择</option>
                <option v-for="a in activeAuths" :key="a.id" :value="a.id">
                  {{ a.proxy_name }}（有效期至 {{ fmt(a.expires_at) }}）
                </option>
              </select>
            </label>
            <label class="field"><span>到场代领人姓名</span><input v-model="release.proxy_name" /></label>
          </div>
          <label class="field"><span>代领人证件号</span><input v-model="release.proxy_id_number" /></label>
        </template>
        <label class="field" v-if="release.method === 'shipping'">
          <span>寄送地址</span><input v-model="release.shipping_addr" placeholder="收件人详细地址" />
        </label>
        <button @click="doRelease" :disabled="releasing">{{ releasing ? '办理中…' : '确认出库（封签核验一致）' }}</button>
      </div>

      <!-- 出库记录 -->
      <div class="panel" v-if="data.deliveries.length">
        <h2>出库记录</h2>
        <table>
          <thead><tr><th>时间</th><th>方式</th><th>领取人</th><th>经办员工</th><th>封签</th><th>物流单号</th></tr></thead>
          <tbody>
            <tr v-for="d in data.deliveries" :key="d.id">
              <td>{{ fmt(d.created_at) }}</td>
              <td>{{ methodLabel(d.method) }}</td>
              <td>{{ d.recipient_name }}</td>
              <td>{{ d.operator_name }}</td>
              <td>{{ d.seal_verified ? '核验一致' : '不符' }}</td>
              <td>{{ d.tracking_no || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { api, api as apiClient, getUser, getToken } from '../lib/api'

const route = useRoute()
const data = ref(null)
const flash = ref(null)
const releasing = ref(false)
const user = getUser()
const isDesk = computed(() => user?.role === 'front_desk')

const newClaim = reactive({ full_name: '', phone: '', id_type: 'idcard', id_number: '', description: '' })
const authForm = reactive({ owner_claim_id: '', proxy_name: '', proxy_id_type: 'idcard', proxy_id_number: '', ttl_hours: 72 })
const release = reactive({ claim_id: '', method: 'self_pickup', seal_presented: '', authorization_id: '', proxy_name: '', proxy_id_number: '', shipping_addr: '' })
const resolveNotes = reactive({})
const extendDays = reactive({})

const photoUrl = computed(() => data.value?.photo_url ? `${data.value.photo_url}?token=${encodeURIComponent(getToken() || '')}` : '')
const verifiedClaims = computed(() => data.value.claims.filter(c => c.status === 'verified'))
const activeAuths = computed(() => data.value.authorizations.filter(a => a.status === 'active'))
const openHolds = computed(() => (data.value?.holds || []).filter(h => !h.resolved))

const fmt = (t) => t ? new Date(t).toLocaleString('zh-CN', { hour12: false }) : ''
const fmtDate = (t) => t ? new Date(t).toLocaleDateString('zh-CN') : ''
const statusLabel = (s) => ({ in_storage: '待认领/在库', released: '已出库', expired: '已过保管期' }[s] || s)
const statusClass = (s) => ({ in_storage: 'storage', released: 'released', expired: 'expired' }[s] || '')
const claimStatus = (s) => ({ pending: '待核实', verified: '已核实', rejected: '已驳回', delivered: '已交付' }[s] || s)
const authStatus = (s) => ({ active: '有效', revoked: '已撤销', used: '已使用', expired: '已过期' }[s] || s)
const methodLabel = (m) => ({ self_pickup: '本人自取', proxy: '代领', shipping: '寄送' }[m] || m)
const ACTIONS = {
  found: '发现登记', move: '位置变更', seal_check: '封签核验',
  claim_filed: '认领登记', claim_verify: '身份核实通过', claim_reject: '核实驳回',
  auth_create: '授权代领', auth_revoke: '撤销授权', outbound: '出库交付',
  delivery_hold: '暂停交付', ship_notify: '寄送通知', expire: '保管到期'
}
const actionLabel = (a) => ACTIONS[a] || a

async function load() {
  data.value = await api.get(`/items/${route.params.id}`)
}

function notify(type, text) {
  flash.value = { type, text }
  setTimeout(() => (flash.value = null), 4000)
}

async function call(fn, okMsg) {
  try {
    await fn()
    notify('ok', okMsg)
    await load()
  } catch (e) {
    notify('error', e.status === 409 ? `交付拦截：${e.message}` : e.message)
    await load()
  }
}

function fileClaim() {
  call(() => apiClient.post(`/items/${route.params.id}/claims`, { ...newClaim }), '认领已登记，等待核实')
  Object.assign(newClaim, { full_name: '', phone: '', id_number: '', description: '' })
}
function verify(id, approved) {
  const note = approved ? '描述与物品一致' : prompt('请填写驳回原因', '描述与物品不符') || ''
  call(() => apiClient.post(`/claims/${id}/verify`, { approved, note }), approved ? '已核实通过' : '已驳回')
}
function createAuth() {
  call(() => apiClient.post(`/claims/${authForm.owner_claim_id}/authorization`, { ...authForm }), '代领授权已建立（撤销后立即失效）')
}
function revoke(id) {
  if (!confirm('确定撤销该代领授权？撤销后代领人将无法领取。')) return
  call(() => apiClient.post(`/authorizations/${id}/revoke`, {}), '授权已撤销')
}
function resolveHold(id) {
  call(() => apiClient.post(`/holds/${id}/resolve`, { note: resolveNotes[id] || '核查无误', extend_days: Number(extendDays[id]) || 0 }), '暂停交付已解除')
}
async function doRelease() {
  releasing.value = true
  try {
    await call(() => apiClient.post(`/items/${route.params.id}/release`, { ...release }), '出库成功，台账已追加出库记录')
  } finally {
    releasing.value = false
  }
}

onMounted(load)
</script>
