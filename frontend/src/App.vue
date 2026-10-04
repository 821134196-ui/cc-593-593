<template>
  <div class="app">
    <header class="topbar">
      <div class="brand" @click="$router.push('/')">🏨 遗留物保管与认领系统</div>
      <nav>
        <router-link to="/">物品搜索</router-link>
        <router-link to="/register">遗留登记</router-link>
      </nav>
      <div class="staff-picker">
        <span>当前员工：</span>
        <select v-model="staffId" @change="switchStaff">
          <option :value="null">未选择</option>
          <option v-for="s in staffList" :key="s.id" :value="s.id">
            {{ s.name }}（{{ roleLabel(s.role) }}）
          </option>
        </select>
      </div>
    </header>
    <main>
      <router-view :key="$route.fullPath" />
    </main>
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import { api, currentStaff, setCurrentStaff } from './api'

const staffList = ref([])
const staffId = ref(currentStaff()?.id ?? null)

function roleLabel(role) {
  return { housekeeper: '保洁', frontdesk: '前台', manager: '经理' }[role] || role
}

function switchStaff() {
  const s = staffList.value.find((x) => x.id === staffId.value)
  setCurrentStaff(s || null)
  // 员工身份影响敏感信息可见性，切换后刷新当前页
  location.reload()
}

onMounted(async () => {
  staffList.value = await api.listStaff()
  if (!currentStaff() && staffList.value.length) {
    // 默认以前台身份进入，便于演示
    const fd = staffList.value.find((s) => s.role === 'frontdesk') || staffList.value[0]
    staffId.value = fd.id
    setCurrentStaff(fd)
  }
})
</script>

<style>
* { box-sizing: border-box; }
body { margin: 0; font-family: 'PingFang SC', 'Microsoft YaHei', system-ui, sans-serif; background: #f3f5f7; color: #2c3e50; }
.topbar { display: flex; align-items: center; gap: 24px; padding: 0 24px; height: 56px; background: #1f3a5f; color: #fff; }
.brand { font-weight: 600; cursor: pointer; }
.topbar nav { display: flex; gap: 16px; flex: 1; }
.topbar nav a { color: #cfd8e3; text-decoration: none; font-size: 14px; }
.topbar nav a.router-link-active { color: #fff; font-weight: 600; }
.staff-picker { font-size: 13px; display: flex; align-items: center; gap: 6px; }
.staff-picker select { padding: 4px 8px; border-radius: 4px; border: none; }
main { max-width: 1080px; margin: 24px auto; padding: 0 16px; }
.card { background: #fff; border-radius: 8px; padding: 20px; box-shadow: 0 1px 3px rgba(0,0,0,.08); margin-bottom: 16px; }
.card h2 { margin-top: 0; font-size: 18px; }
button { padding: 7px 14px; border: none; border-radius: 5px; background: #1f6feb; color: #fff; cursor: pointer; font-size: 13px; }
button:disabled { background: #9db8d9; cursor: not-allowed; }
button.danger { background: #d64545; }
button.ghost { background: #eef2f7; color: #2c3e50; }
input, select, textarea { padding: 7px 10px; border: 1px solid #d0d7de; border-radius: 5px; font-size: 14px; font-family: inherit; }
label { font-size: 13px; color: #57606a; display: block; margin-bottom: 4px; }
.form-row { margin-bottom: 14px; }
.badge { display: inline-block; padding: 2px 8px; border-radius: 10px; font-size: 12px; }
.badge.stored { background: #e3f2e5; color: #1a7f37; }
.badge.hold { background: #fff1e0; color: #b35900; }
.badge.delivered, .badge.shipped { background: #e8ecf1; color: #57606a; }
.badge.sensitive { background: #fde8e8; color: #c0392b; }
.error-msg { background: #fde8e8; color: #c0392b; padding: 10px 14px; border-radius: 6px; margin-bottom: 12px; font-size: 13px; }
.ok-msg { background: #e3f2e5; color: #1a7f37; padding: 10px 14px; border-radius: 6px; margin-bottom: 12px; font-size: 13px; }
table { width: 100%; border-collapse: collapse; font-size: 14px; }
th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid #eef2f7; }
th { color: #57606a; font-weight: 500; font-size: 13px; }
</style>
