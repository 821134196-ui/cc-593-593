<template>
  <div>
    <div class="panel">
      <h2>{{ isDesk ? '按住客描述搜索遗留物' : '遗留物台账' }}</h2>
      <div class="row" style="align-items:flex-end">
        <label class="field" style="flex:3">
          <span>关键词（房号 / 必要信息描述 / 封袋号）</span>
          <input v-model="q" @keyup.enter="load" placeholder="例如：黑色钱包、1206、SEAL-" />
        </label>
        <label class="field" style="flex:1">
          <span>分类</span>
          <select v-model="category" @change="load">
            <option value="">全部分类</option>
            <option value="electronics">电子产品</option>
            <option value="clothing">衣物</option>
            <option value="jewelry">首饰/贵重物品</option>
            <option value="id">证件</option>
            <option value="cash">现金</option>
            <option value="other">其他</option>
          </select>
        </label>
        <div style="flex:0 0 auto">
          <button @click="load">搜索</button>
        </div>
      </div>
      <div class="hint">
        证件、现金等敏感物品在搜索结果中仅显示必要信息，完整证件号/金额需进入物品详情页由前台在核实环节查看。
      </div>
    </div>

    <div v-if="error" class="msg error">{{ error }}</div>

    <router-link v-for="it in items" :key="it.id" :to="`/items/${it.id}`" class="item-card">
      <img v-if="it.photo_url" :src="withToken(it.photo_url)" alt="物品照片" />
      <div v-else style="width:72px;height:72px;border-radius:8px;background:#f3f4f6;display:flex;align-items:center;justify-content:center;color:#9ca3af;font-size:12px">无照片</div>
      <div class="meta">
        <div class="title">{{ it.description }}</div>
        <div class="sub">
          房号 {{ it.room_number }} ｜ 发现于 {{ fmt(it.found_at) }} ｜ 封袋号 {{ it.bag_seal_number }}
          ｜ 位置 {{ it.storage_location }} ｜ 登记人 {{ it.found_by_name }}
        </div>
        <div class="tags">
          <span class="badge" :class="statusClass(it.status)">{{ statusLabel(it.status) }}</span>
          <span v-if="it.sensitive" class="badge sensitive">敏感物品 · 信息已脱敏</span>
          <span class="badge pending">{{ categoryLabel(it.category) }}</span>
        </div>
      </div>
    </router-link>

    <div v-if="items.length === 0 && !error" class="panel muted" style="text-align:center">没有匹配的物品</div>
  </div>
</template>

<script setup>
import { ref, onMounted, computed } from 'vue'
import { api, getUser, getToken } from '../lib/api'

const withToken = (url) => `${url}?token=${encodeURIComponent(getToken() || '')}`

const q = ref('')
const category = ref('')
const items = ref([])
const error = ref('')
const user = getUser()
const isDesk = computed(() => user?.role === 'front_desk')

const CATEGORIES = { electronics: '电子产品', clothing: '衣物', jewelry: '首饰/贵重物品', id: '证件', cash: '现金', other: '其他' }
const categoryLabel = (c) => CATEGORIES[c] || c
const statusLabel = (s) => ({ in_storage: '待认领/在库', released: '已出库', expired: '已过保管期' }[s] || s)
const statusClass = (s) => ({ in_storage: 'storage', released: 'released', expired: 'expired' }[s] || '')
const fmt = (t) => new Date(t).toLocaleString('zh-CN', { hour12: false })

async function load() {
  error.value = ''
  try {
    const res = await api.get(`/items?q=${encodeURIComponent(q.value)}&category=${encodeURIComponent(category.value)}`)
    items.value = res.items
  } catch (e) {
    error.value = e.message
  }
}
onMounted(load)
</script>
