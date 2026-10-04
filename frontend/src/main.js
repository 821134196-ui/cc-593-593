import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import Login from './views/Login.vue'
import RegisterItem from './views/RegisterItem.vue'
import Search from './views/Search.vue'
import ItemDetail from './views/ItemDetail.vue'
import './styles.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: Login },
    { path: '/', redirect: '/items' },
    { path: '/register', component: RegisterItem },
    { path: '/items', component: Search },
    { path: '/items/:id', component: ItemDetail, props: true }
  ]
})

router.beforeEach((to) => {
  const token = localStorage.getItem('token')
  if (!token && to.path !== '/login') return '/login'
  if (token && to.path === '/login') return '/items'
})

createApp(App).use(router).mount('#app')
