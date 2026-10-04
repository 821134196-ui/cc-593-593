<template>
  <div v-if="data">
    <div v-if="error" class="error-msg">{{ error }}</div>
    <div v-if="notice" class="ok-msg">{{ notice }}</div>

    <!-- 物品信息 -->
    <div class="card">
      <h2>
        封袋号 {{ item.bag_no }}
        <span class="badge" :class="item.status">{{ STATUS_LABELS[item.status] }}</span>
        <span v-if="item.sensitive" class="badge sensitive">敏感物品</span>
      </h2>
      <p v-if="item.hold_reason" class="error-msg">暂停原因：{{ item.hold_reason }}（需经理复核后恢复）</p>
      <table>
        <tbody>
          <tr><th style="width: 110px">房号</th><td>{{ item.room_no }}</td><th>发现时间</th><td>{{ fmtTime(item.found_at) }}</td></tr>
          <tr><th>类别</th><td>{{ CATEGORY_LABELS[item.category] }}</td><th>保管期至</th><td>{{ fmtTime(item.retain_until) }}</td></tr>
          <tr><th>保管位置</th><td>{{ item.storage_loc }}</td><th>登记人</th><td>{{ item.created_by }}</td></tr>
          <tr><th>描述</th><td colspan="3">{{ item.description }}</td></tr>
        </tbody>
      </table>
      <div v-if="item.photos?.length" style="margin-top: 12px; display: flex; gap: 10px; flex-wrap: wrap">
        <img v-for="p in item.photos" :key="p" :src="p" style="height: 120px; border-radius: 6px; border: 1px solid #eee" />
      </div>
      <div style="margin-top: 14px; display: flex; gap: 10px; align-items: center; flex-wrap: wrap">
        <template v-if="!delivered">
          <input v-model="moveLoc" placeholder="新保管位置" style="width: 160px" />
          <button class="ghost" @click="doMove">变更位置</button>
          <button v-if="item.status === 'hold'" @click="doResume">经理复核恢复</button>
        </template>
        <button v-if="!showClaimForm && !delivered" @click="showClaimForm = true">登记认领人</button>
      </div>
    </div>

    <!-- 新认领单 -->
    <div v-if="showClaimForm" class="card">
      <h2>登记认领人</h2>
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px">
        <div class="form-row"><label>认领人姓名 *</label><input v-model="claimForm.claimant_name" /></div>
        <div class="form-row"><label>证件号 *</label><input v-model="claimForm.claimant_id_no" /></div>
        <div class="form-row"><label>联系电话</label><input v-model="claimForm.phone" /></div>
        <div class="form-row">
          <label>领取方式 *</label>
          <select v-model="claimForm.pickup_type">
            <option value="self">失主自取</option>
            <option value="proxy">授权代领</option>
            <option value="ship">快递寄送</option>
          </select>
        </div>
      </div>
      <div class="form-row"><label>认领人提供的物品描述 *（用于核对）</label>
        <textarea v-model="claimForm.description" rows="2" style="width: 100%"></textarea>
      </div>
      <button @click="doCreateClaim">提交</button>
      <button class="ghost" style="margin-left: 8px" @click="showClaimForm = false">取消</button>
    </div>

    <!-- 认领记录 -->
    <div class="card">
      <h2>认领记录（{{ data.claims.length }}）</h2>
      <p v-if="!data.claims.length" style="color: #8b949e">暂无认领人</p>
      <div v-for="cl in data.claims" :key="cl.id" class="claim-box">
        <div style="display: flex; justify-content: space-between; align-items: center">
          <div>
            <b>{{ cl.claimant }}</b>
            <span class="badge" :class="claimBadge(cl.status)" style="margin-left: 8px">{{ CLAIM_STATUS_LABELS[cl.status] }}</span>
            <span style="margin-left: 8px; font-size: 13px; color: #57606a">{{ PICKUP_LABELS[cl.pickup_type] }}</span>
          </div>
          <span style="font-size: 12px; color: #8b949e">{{ fmtTime(cl.created_at) }}</span>
        </div>
        <p style="font-size: 13px; margin: 8px 0">
          证件号：{{ cl.id_no }}　电话：{{ cl.phone || '—' }}<br />
          认领描述：{{ cl.description }}
          <template v-if="cl.reject_reason"><br /><span style="color: #c0392b">拒绝原因：{{ cl.reject_reason }}</span></template>
        </p>

        <!-- 代领授权信息 -->
        <div v-if="cl.authorization" style="font-size: 13px; background: #f6f8fa; padding: 8px 10px; border-radius: 6px; margin-bottom: 8px">
          代领授权：授权人 {{ cl.authorization.grantor_name }} → 代领人 {{ cl.authorization.proxy_name }}（{{ cl.authorization.proxy_id_no }}）
          <span class="badge" :class="cl.authorization.status === 'active' ? 'stored' : 'delivered'">
            {{ cl.authorization.status === 'active' ? '授权有效' : '已撤销' }}
          </span>
          <button v-if="cl.authorization.status === 'active' && cl.status === 'verified'" class="danger" style="margin-left: 10px; padding: 3px 10px"
            @click="doRevoke(cl)">撤销授权</button>
        </div>

        <!-- 认领单操作 -->
        <div style="display: flex; gap: 8px; flex-wrap: wrap; align-items: center">
          <template v-if="cl.status === 'pending'">
            <button @click="doVerify(cl, true)">核验通过</button>
            <button class="danger" @click="doVerify(cl, false)">核验不通过</button>
          </template>

          <template v-if="cl.status === 'verified' && !delivered">
            <template v-if="cl.pickup_type === 'proxy' && !cl.authorization">
              <input v-model="authForms[cl.id].grantor_name" placeholder="授权人（失主）" style="width: 120px" />
              <input v-model="authForms[cl.id].proxy_name" placeholder="代领人姓名" style="width: 110px" />
              <input v-model="authForms[cl.id].proxy_id_no" placeholder="代领人证件号" style="width: 160px" />
              <button @click="doAuthorize(cl)">登记授权</button>
            </template>
            <template v-if="cl.pickup_type !== 'ship'">
              <label style="display: inline-flex; align-items: center; gap: 4px; margin: 0">
                <input type="checkbox" v-model="sealChecks[cl.id]" /> 封签完好
              </label>
              <button @click="doCheckout(cl)">办理出库</button>
            </template>
            <template v-else>
              <label style="display: inline-flex; align-items: center; gap: 4px; margin: 0">
                <input type="checkbox" v-model="sealChecks[cl.id]" /> 封签完好
              </label>
              <input v-model="shipForms[cl.id].address" placeholder="收件地址" style="width: 180px" />
              <input v-model="shipForms[cl.id].tracking_no" placeholder="快递单号" style="width: 130px" />
              <button @click="doShip(cl)">办理寄送</button>
            </template>
          </template>
        </div>
      </div>
    </div>

    <!-- 交接时间线 -->
    <div class="card">
      <h2>交接台账（只追加）</h2>
      <div class="timeline">
        <div v-for="h in data.timeline" :key="h.id" class="timeline-item">
          <div class="timeline-dot" :class="h.action"></div>
          <div>
            <b>{{ ACTION_LABELS[h.action] }}</b>
            <span style="font-size: 12px; color: #8b949e; margin-left: 8px">{{ fmtTime(h.created_at) }}</span>
            <div style="font-size: 13px; color: #57606a">
              经手：{{ h.staff?.name }}
              <template v-if="h.counterparty">　对方：{{ h.counterparty }}</template>
              <template v-if="h.note">　{{ h.note }}</template>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api, ACTION_LABELS, CATEGORY_LABELS, CLAIM_STATUS_LABELS, PICKUP_LABELS, STATUS_LABELS } from '../api'

const route = useRoute()
const itemId = route.params.id

const data = ref(null)
const error = ref('')
const notice = ref('')
const showClaimForm = ref(false)
const moveLoc = ref('')
const claimForm = reactive({ claimant_name: '', claimant_id_no: '', phone: '', description: '', pickup_type: 'self' })
const authForms = reactive({})
const shipForms = reactive({})
const sealChecks = reactive({})

const item = computed(() => data.value?.item || {})
const delivered = computed(() => data.value?.delivered)

function fmtTime(t) {
  return t ? new Date(t).toLocaleString('zh-CN', { hour12: false }) : ''
}
function claimBadge(s) {
  return { pending: 'hold', verified: 'stored', rejected: 'delivered', completed: 'delivered' }[s]
}

async function load() {
  data.value = await api.getItem(itemId)
  for (const cl of data.value.claims) {
    if (!authForms[cl.id]) authForms[cl.id] = { grantor_name: '', proxy_name: '', proxy_id_no: '' }
    if (!shipForms[cl.id]) shipForms[cl.id] = { address: '', tracking_no: '' }
  }
}

async function run(fn, okMsg) {
  error.value = ''
  notice.value = ''
  try {
    const r = await fn()
    notice.value = okMsg
    await load()
    return r
  } catch (e) {
    error.value = e.message
    await load().catch(() => {})
  }
}

const doMove = () => run(() => api.moveItem(itemId, moveLoc.value), '保管位置已变更')
const doResume = () => run(() => api.resumeItem(itemId), '已恢复可交付')
const doCreateClaim = () =>
  run(async () => {
    await api.createClaim(itemId, { ...claimForm })
    showClaimForm.value = false
    Object.assign(claimForm, { claimant_name: '', claimant_id_no: '', phone: '', description: '', pickup_type: 'self' })
  }, '认领人已登记，待核验身份')

const doVerify = (cl, approve) => {
  const reason = approve ? '' : prompt('请输入核验不通过原因：')
  if (!approve && !reason) return
  run(() => api.verifyClaim(cl.id, approve, reason), approve ? '核验通过' : '已标记核验不通过')
}
const doAuthorize = (cl) => run(() => api.createAuthorization(cl.id, { ...authForms[cl.id] }), '代领授权已登记')
const doRevoke = (cl) => run(() => api.revokeAuthorization(cl.id), '授权已撤销，代领交付将被拦截')
const doCheckout = (cl) =>
  run(async () => {
    const r = await api.checkout(itemId, cl.id, !!sealChecks[cl.id])
    return (notice.value = `已出库交付给：${r.delivered_to}`)
  }, '')
const doShip = (cl) =>
  run(async () => {
    const f = shipForms[cl.id]
    await api.ship(itemId, cl.id, !!sealChecks[cl.id], f.address, f.tracking_no)
  }, '已寄出，寄送通知已发送（模拟）')

onMounted(async () => {
  try {
    await load()
  } catch (e) {
    error.value = e.message
  }
})
</script>

<style scoped>
.claim-box { border: 1px solid #eef2f7; border-radius: 8px; padding: 12px 14px; margin-bottom: 12px; }
.timeline { margin-top: 8px; }
.timeline-item { display: flex; gap: 12px; padding: 10px 0; border-bottom: 1px dashed #eef2f7; }
.timeline-dot { width: 12px; height: 12px; border-radius: 50%; background: #1f6feb; margin-top: 5px; flex: none; }
.timeline-dot.hold { background: #d64545; }
.timeline-dot.checkout, .timeline-dot.ship { background: #1a7f37; }
.timeline-dot.resume { background: #b35900; }
</style>
